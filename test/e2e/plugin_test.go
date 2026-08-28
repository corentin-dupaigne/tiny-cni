//go:build linux && e2e

// This file is the part of the harness that knows about tiny-cni: how a node is
// prepared, how the plugin is invoked, and what a pod looks like afterwards.
// The namespace machinery it builds on lives in netns_test.go.
package e2e

import (
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"testing"

	"github.com/corentin-dupaigne/tiny-cni/internal/cni"
	"github.com/corentin-dupaigne/tiny-cni/internal/network"
	"github.com/vishvananda/netlink"
)

const (
	// hardcoded in SetupVeth, mirrored here so the tests can reset it
	ipamStatePath = "/tmp/tinycni-counter"

	bridgeName = "tcni-bridge"

	// the name runtimes conventionally ask for
	defaultIfName = "eth0"
)

// newNode gives the calling test a clean node to work with: a private network
// namespace it is pinned to, and a fresh IPAM counter.
func newNode(t *testing.T) {
	t.Helper()

	requireRoot(t)
	resetIPAMState(t)
	enterNodeNetns(t)
}

// cniAdd runs the plugin's ADD against a namespace and reports what it returned,
// without failing the test. Tests covering error paths call it directly; the
// happy path goes through newPod.
//
// This is the single point where the suite reaches into the implementation.
// Once cmd/tiny-cni dispatches commands and reports failure through its exit
// code, this becomes an exec of the built binary with CNI_* in the environment
// and the network config on stdin, and nothing else in the suite changes.
func cniAdd(netns string, ifName string) error {
	return network.SetupVeth(&cni.Args{
		Command:     "ADD",
		ContainerId: filepath.Base(netns),
		IfName:      ifName,
		Netns:       netns,
	})
}

// pod is a namespace that has been through a successful ADD.
type pod struct {
	name   string
	netns  string
	ifName string
	addr   netlink.Addr
}

// newPod creates a pod namespace, runs ADD against it, and reads back the
// address the IPAM allocated. It is the happy path: any failure along the way
// fails the test.
func newPod(t *testing.T, name string) pod {
	t.Helper()

	p := pod{
		name:   name,
		netns:  newPodNetns(t, name),
		ifName: defaultIfName,
	}

	if err := cniAdd(p.netns, p.ifName); err != nil {
		t.Fatalf("ADD for pod %s: %v", name, err)
	}

	addr, err := podAddr(p.netns, p.ifName)
	if err != nil {
		t.Fatalf("reading address of pod %s: %v", name, err)
	}
	p.addr = addr

	return p
}

// podAddr reads the single IPv4 address the plugin is expected to have put on
// the pod's interface.
func podAddr(netns string, ifName string) (netlink.Addr, error) {
	var addr netlink.Addr

	err := inNetns(netns, func() error {
		link, err := netlink.LinkByName(ifName)
		if err != nil {
			return fmt.Errorf("looking up %s: %w", ifName, err)
		}

		addrs, err := netlink.AddrList(link, netlink.FAMILY_V4)
		if err != nil {
			return fmt.Errorf("listing addresses of %s: %w", ifName, err)
		}
		if len(addrs) != 1 {
			return fmt.Errorf("expected exactly 1 IPv4 address on %s, got %d", ifName, len(addrs))
		}

		addr = addrs[0]
		return nil
	})

	return addr, err
}

// do runs fn inside the pod's namespace.
func (p pod) do(fn func() error) error {
	return inNetns(p.netns, fn)
}

// listen opens a TCP listener inside the pod, on the address it was allocated.
// The socket outlives the thread that created it, so it can be accepted on from
// anywhere.
func (p pod) listen(t *testing.T) net.Listener {
	t.Helper()

	var ln net.Listener
	err := p.do(func() error {
		var err error
		ln, err = net.Listen("tcp", net.JoinHostPort(p.addr.IP.String(), "0"))
		return err
	})
	if err != nil {
		t.Fatalf("listening in pod %s: %v", p.name, err)
	}

	t.Cleanup(func() { ln.Close() })

	return ln
}

// resetIPAMState clears the allocator's counter so allocations are predictable,
// and puts back whatever was there once the test is done. It exists only
// because the storage path is hardcoded in SetupVeth; it goes away the day the
// allocator takes its path from the config.
func resetIPAMState(t *testing.T) {
	t.Helper()

	previous, err := os.ReadFile(ipamStatePath)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("reading IPAM state %s: %v", ipamStatePath, err)
	}
	hadState := err == nil

	if err := os.Remove(ipamStatePath); err != nil && !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("removing IPAM state %s: %v", ipamStatePath, err)
	}

	t.Cleanup(func() {
		if !hadState {
			os.Remove(ipamStatePath)
			return
		}
		if err := os.WriteFile(ipamStatePath, previous, 0644); err != nil {
			t.Errorf("restoring IPAM state %s: %v", ipamStatePath, err)
		}
	})
}
