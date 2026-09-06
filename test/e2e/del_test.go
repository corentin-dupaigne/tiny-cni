//go:build linux && e2e

// Feature: DEL gives back everything ADD took. A pod's namespace is destroyed
// by the runtime, but the node-side veth and the pod's address live on the
// node, and leaking either of them is how a node slowly stops being able to
// schedule pods.
package e2e

import (
	"fmt"
	"testing"

	"github.com/vishvananda/netlink"
)

// the veth pair goes away with the pod, on both sides
func TestDelRemovesPodWiring(t *testing.T) {
	n := newNode(t)

	p := newPod(t, n, "pod-a")

	if got := len(hostVeths(t)); got != 1 {
		t.Fatalf("node has %d host veths after ADD, want 1", got)
	}

	n.del(t, p.containerID, p.netns, p.ifName)

	if got := len(hostVeths(t)); got != 0 {
		t.Errorf("node still has %d host veths after DEL, want 0", got)
	}

	err := p.do(func() error {
		_, err := netlink.LinkByName(p.ifName)
		return err
	})
	if err == nil {
		t.Errorf("pod interface %s is still present after DEL", p.ifName)
	}
}

// deleting one pod leaves the others alone
func TestDelLeavesOtherPodsWired(t *testing.T) {
	n := newNode(t)

	a := newPod(t, n, "pod-a")
	b := newPod(t, n, "pod-b")

	n.del(t, a.containerID, a.netns, a.ifName)

	addr, err := podAddr(b.netns, b.ifName)
	if err != nil {
		t.Fatalf("pod %s lost its interface when %s was deleted: %v", b.name, a.name, err)
	}
	if !addr.IP.Equal(b.addr.IP) {
		t.Errorf("pod %s now carries %s, had %s", b.name, addr.IP, b.addr.IP)
	}

	if got := len(hostVeths(t)); got != 1 {
		t.Errorf("node has %d host veths after deleting 1 of 2 pods, want 1", got)
	}
}

// the address goes back to the pool, so a node that churns pods does not run
// out of addresses it is no longer using.
//
// Which address the next pod gets is the allocator's business: it is only
// required to be a usable one, not the one just released.
func TestDelReturnsAddressToPool(t *testing.T) {
	n := newNode(t)

	a := newPod(t, n, "pod-a")
	t.Logf("%s=%s", a.name, a.addr.IP)

	n.del(t, a.containerID, a.netns, a.ifName)

	// the node is now empty; allocating again has to work
	b := newPod(t, n, "pod-b")
	t.Logf("%s=%s", b.name, b.addr.IP)

	if !subnetContains(t, b.addr.IP.String()) {
		t.Errorf("%s was given %s, outside the configured subnet %s", b.name, b.addr.IP, subnet)
	}
}

// pods coming and going must not drain the pool: the addresses of deleted pods
// have to become available again, and never collide with a pod still running
func TestDelSurvivesPodChurn(t *testing.T) {
	n := newNode(t)

	// one pod stays up for the whole run, so the churn happens around a live
	// allocation rather than on an empty node
	resident := newPod(t, n, "resident")
	t.Logf("%s=%s", resident.name, resident.addr.IP)

	for i := range 5 {
		name := fmt.Sprintf("churn-%d", i)

		p := newPod(t, n, name)
		t.Logf("%s=%s", p.name, p.addr.IP)

		if p.addr.IP.Equal(resident.addr.IP) {
			t.Fatalf("%s was given %s, already held by %s", name, p.addr.IP, resident.name)
		}
		if !subnetContains(t, p.addr.IP.String()) {
			t.Fatalf("%s was given %s, outside the configured subnet %s", name, p.addr.IP, subnet)
		}

		n.del(t, p.containerID, p.netns, p.ifName)
	}

	// the resident pod went through all of it untouched
	addr, err := podAddr(resident.netns, resident.ifName)
	if err != nil {
		t.Fatalf("resident pod lost its interface during the churn: %v", err)
	}
	if !addr.IP.Equal(resident.addr.IP) {
		t.Errorf("resident pod now carries %s, had %s", addr.IP, resident.addr.IP)
	}
}

// the runtime is allowed to call DEL for a pod that was never added, and on a
// pod it has already deleted; neither is an error it can act on, so neither may
// fail
func TestDelIsIdempotent(t *testing.T) {
	n := newNode(t)

	p := newPod(t, n, "pod-a")

	n.del(t, p.containerID, p.netns, p.ifName)

	if _, err := n.invoke("DEL", p.containerID, p.netns, p.ifName); err != nil {
		t.Errorf("second DEL for %s failed: %v", p.name, err)
	}

	unknown := newPodNetns(t, "pod-never-added")
	if _, err := n.invoke("DEL", "pod-never-added", unknown, defaultIfName); err != nil {
		t.Errorf("DEL for a pod that was never added failed: %v", err)
	}
}
