//go:build linux && e2e

// Feature: two pods scheduled on the same node reach each other over the node's
// bridge. Covers the datapath itself, the layer 2 wiring underneath it, and the
// node-side topology the plugin is expected to build to make it work.
package e2e

import (
	"errors"
	"fmt"
	"io"
	"net"
	"testing"
	"time"

	"github.com/vishvananda/netlink"
)

// the pods must exchange actual traffic, not merely look correctly wired
func TestPodsOnSameNodeExchangeTraffic(t *testing.T) {
	newNode(t)

	a := newPod(t, "pod-a")
	b := newPod(t, "pod-b")

	t.Logf("%s=%s %s=%s", a.name, a.addr.IPNet, b.name, b.addr.IPNet)

	if a.addr.IP.Equal(b.addr.IP) {
		t.Fatalf("IPAM handed the same address %s to both pods", a.addr.IP)
	}

	const payload = "tiny-cni"

	ln := b.listen(t)

	served := make(chan error, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			served <- fmt.Errorf("accepting on %s: %w", b.name, err)
			return
		}
		defer conn.Close()

		conn.SetDeadline(time.Now().Add(5 * time.Second))
		if _, err := io.WriteString(conn, payload); err != nil {
			served <- fmt.Errorf("writing from %s: %w", b.name, err)
			return
		}
		served <- nil
	}()

	var got string
	err := a.do(func() error {
		conn, err := net.DialTimeout("tcp", ln.Addr().String(), 5*time.Second)
		if err != nil {
			return fmt.Errorf("dialing %s from %s: %w", b.name, a.name, err)
		}
		defer conn.Close()

		conn.SetDeadline(time.Now().Add(5 * time.Second))
		payload, err := io.ReadAll(conn)
		if err != nil {
			return fmt.Errorf("reading from %s: %w", b.name, err)
		}

		got = string(payload)
		return nil
	})
	if err != nil {
		t.Fatalf("%s could not talk to %s: %v", a.name, b.name, err)
	}

	if err := <-served; err != nil {
		t.Fatalf("%s side of the exchange failed: %v", b.name, err)
	}

	if got != payload {
		t.Errorf("%s received %q from %s, want %q", a.name, got, b.name, payload)
	}
}

// the pods are on the same subnet, so they should find each other by ARP over
// the bridge rather than through some route out of the namespace
func TestPodsOnSameNodeResolveEachOtherAtLayer2(t *testing.T) {
	newNode(t)

	a := newPod(t, "pod-a")
	b := newPod(t, "pod-b")

	// a connection is enough to populate pod-a's neighbour table
	ln := b.listen(t)
	go func() {
		if conn, err := ln.Accept(); err == nil {
			conn.Close()
		}
	}()

	err := a.do(func() error {
		conn, err := net.DialTimeout("tcp", ln.Addr().String(), 5*time.Second)
		if err != nil {
			return fmt.Errorf("dialing %s from %s: %w", b.name, a.name, err)
		}
		conn.Close()

		link, err := netlink.LinkByName(a.ifName)
		if err != nil {
			return fmt.Errorf("looking up %s in %s: %w", a.ifName, a.name, err)
		}

		neighs, err := netlink.NeighList(link.Attrs().Index, netlink.FAMILY_V4)
		if err != nil {
			return fmt.Errorf("listing neighbours in %s: %w", a.name, err)
		}

		for _, n := range neighs {
			if n.IP.Equal(b.addr.IP) && n.HardwareAddr != nil {
				return nil
			}
		}

		return fmt.Errorf("%s has no ARP entry for %s (%s), neighbours: %v",
			a.name, b.name, b.addr.IP, neighs)
	})
	if err != nil {
		t.Fatalf("%v", err)
	}
}

// the node side of the wiring: one bridge, both host veth ends enslaved to it
// and up
func TestNodeBridgeEnslavesEveryPodInterface(t *testing.T) {
	newNode(t)

	newPod(t, "pod-a")
	newPod(t, "pod-b")

	br, err := netlink.LinkByName(bridgeName)
	if err != nil {
		t.Fatalf("looking up bridge %s: %v", bridgeName, err)
	}

	if _, ok := br.(*netlink.Bridge); !ok {
		t.Fatalf("%s is a %T, want a bridge", bridgeName, br)
	}

	if br.Attrs().OperState == netlink.OperDown {
		t.Errorf("bridge %s is down", bridgeName)
	}

	links, err := netlink.LinkList()
	if err != nil {
		t.Fatalf("listing links: %v", err)
	}

	enslaved := 0
	for _, l := range links {
		veth, ok := l.(*netlink.Veth)
		if !ok {
			continue
		}
		enslaved++

		if veth.Attrs().MasterIndex != br.Attrs().Index {
			t.Errorf("host veth %s has master index %d, want %d (%s)",
				veth.Attrs().Name, veth.Attrs().MasterIndex, br.Attrs().Index, bridgeName)
		}

		if veth.Attrs().Flags&net.FlagUp == 0 {
			t.Errorf("host veth %s is not up", veth.Attrs().Name)
		}
	}

	if enslaved != 2 {
		t.Errorf("found %d host veth interfaces on the node, want 2", enslaved)
	}
}

// the bridge is created by the first pod and looked up by every pod after it,
// instead of a second creation attempt failing the ADD
func TestNodeBridgeIsCreatedOnce(t *testing.T) {
	newNode(t)

	newPod(t, "pod-a")

	br, err := netlink.LinkByName(bridgeName)
	if err != nil {
		t.Fatalf("looking up bridge after first pod: %v", err)
	}
	index := br.Attrs().Index

	newPod(t, "pod-b")

	br, err = netlink.LinkByName(bridgeName)
	if err != nil {
		t.Fatalf("looking up bridge after second pod: %v", err)
	}

	if br.Attrs().Index != index {
		t.Errorf("bridge was recreated for the second pod: index %d then %d",
			index, br.Attrs().Index)
	}
}

// pods also need to reach themselves, which is what the loopback the plugin
// brings up is for
func TestPodReachesItselfOverLoopback(t *testing.T) {
	newNode(t)

	p := newPod(t, "pod-a")

	err := p.do(func() error {
		lo, err := netlink.LinkByName("lo")
		if err != nil {
			return fmt.Errorf("looking up lo: %w", err)
		}

		if lo.Attrs().Flags&net.FlagUp == 0 {
			return errors.New("lo is not up")
		}

		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			return fmt.Errorf("listening on loopback: %w", err)
		}
		defer ln.Close()

		conn, err := net.DialTimeout("tcp", ln.Addr().String(), 2*time.Second)
		if err != nil {
			return fmt.Errorf("dialing loopback: %w", err)
		}
		conn.Close()

		return nil
	})
	if err != nil {
		t.Fatalf("%s loopback: %v", p.name, err)
	}
}
