//go:build linux && e2e

// Feature: ADD wires a pod up and reports what it did. The runtime never looks
// inside the namespace -- it believes the result -- so the result has to
// describe the wiring that is actually there.
package e2e

import (
	"net"
	"strings"
	"testing"

	"github.com/vishvananda/netlink"
)

// the pod comes back with the interface it asked for, up, and carrying exactly
// the address the result claims
func TestAddWiresPodInterface(t *testing.T) {
	n := newNode(t)

	p := newPod(t, n, "pod-a")

	err := p.do(func() error {
		link, err := netlink.LinkByName(p.ifName)
		if err != nil {
			return err
		}

		if link.Attrs().OperState == netlink.OperDown {
			t.Errorf("pod interface %s is down", p.ifName)
		}

		if _, ok := link.(*netlink.Veth); !ok {
			t.Errorf("pod interface %s is a %T, want a veth", p.ifName, link)
		}

		return nil
	})
	if err != nil {
		t.Fatalf("inspecting pod interface: %v", err)
	}

	if !subnetContains(t, p.addr.IP.String()) {
		t.Errorf("pod address %s is outside the configured subnet %s", p.addr.IP, subnet)
	}
}

// the result is the plugin's report to the runtime: the address it assigned,
// attached to the interface it assigned it to
func TestAddResultDescribesTheWiring(t *testing.T) {
	n := newNode(t)

	p := newPod(t, n, "pod-a")
	res := p.result

	if res.CNIVersion != cniVersion {
		t.Errorf("result reports CNI version %q, want %q", res.CNIVersion, cniVersion)
	}

	if len(res.IPs) != 1 {
		t.Fatalf("result carries %d IPs, want 1: %+v", len(res.IPs), res.IPs)
	}

	ip := res.IPs[0]
	if !ip.Address.IP.Equal(p.addr.IP) {
		t.Errorf("result announces %s but the pod carries %s", ip.Address.IP, p.addr.IP)
	}

	if ip.Interface == nil {
		t.Fatalf("result IP %s is not attached to any interface", ip.Address.IP)
	}
	if *ip.Interface < 0 || *ip.Interface >= len(res.Interfaces) {
		t.Fatalf("result IP %s points at interface %d, out of range of the %d reported",
			ip.Address.IP, *ip.Interface, len(res.Interfaces))
	}

	// the address belongs to the pod's interface, so the one it points at must
	// be the sandboxed one
	inf := res.Interfaces[*ip.Interface]
	if inf.Sandbox != p.netns {
		t.Errorf("result attaches %s to interface %q with sandbox %q, want the pod netns %q",
			ip.Address.IP, inf.Name, inf.Sandbox, p.netns)
	}
	if inf.Name != p.ifName {
		t.Errorf("result names the pod interface %q, want %q", inf.Name, p.ifName)
	}

	// and the host end is reported too, unsandboxed, under the configured prefix
	var host int
	for i, in := range res.Interfaces {
		if in.Sandbox == "" {
			host++
			if !strings.HasPrefix(in.Name, vethPrefix+"-") {
				t.Errorf("host interface %d is named %q, want the %q prefix", i, in.Name, vethPrefix)
			}
			if in.Mac == "" {
				t.Errorf("host interface %q is reported without a MAC", in.Name)
			}
		}
	}
	if host != 1 {
		t.Errorf("result reports %d host interfaces, want 1: %+v", host, res.Interfaces)
	}
}

// every pod gets an address of its own, and the ones the subnet reserves are
// never handed out
func TestAddAllocatesDistinctUsableAddresses(t *testing.T) {
	n := newNode(t)

	// the network and gateway addresses of 10.244.0.0/24
	reserved := map[string]string{
		"10.244.0.0": "network address",
		"10.244.0.1": "gateway address",
	}

	seen := map[string]string{}
	for _, name := range []string{"pod-a", "pod-b", "pod-c"} {
		p := newPod(t, n, name)
		ip := p.addr.IP.String()

		if what, ok := reserved[ip]; ok {
			t.Errorf("%s was given %s, the %s", name, ip, what)
		}

		if other, ok := seen[ip]; ok {
			t.Errorf("IPAM handed %s to both %s and %s", ip, other, name)
		}
		seen[ip] = name

		if !subnetContains(t, ip) {
			t.Errorf("%s was given %s, outside the configured subnet %s", name, ip, subnet)
		}
	}
}

// a pod that asks for an interface name other than the conventional one gets
// that name, since the runtime chooses it
func TestAddHonoursRequestedInterfaceName(t *testing.T) {
	n := newNode(t)

	const ifName = "net1"
	netns := newPodNetns(t, "pod-a")

	n.add(t, "pod-a", netns, ifName)

	if _, err := podAddr(netns, ifName); err != nil {
		t.Fatalf("pod interface %s: %v", ifName, err)
	}
}

// a namespace path that does not exist is the runtime's mistake, and the plugin
// has to report it rather than half-wire the node
func TestAddRejectsMissingNetns(t *testing.T) {
	n := newNode(t)

	before := len(hostVeths(t))

	if _, err := n.invoke("ADD", "pod-a", "/var/run/netns/does-not-exist", defaultIfName); err == nil {
		t.Fatal("ADD succeeded against a namespace that does not exist")
	}

	if after := len(hostVeths(t)); after != before {
		t.Errorf("failed ADD left the node with %d host veths, had %d before", after, before)
	}
}

func subnetContains(t *testing.T, ip string) bool {
	t.Helper()

	_, network, err := net.ParseCIDR(subnet)
	if err != nil {
		t.Fatalf("parsing subnet %s: %v", subnet, err)
	}

	parsed := net.ParseIP(ip)
	if parsed == nil {
		t.Fatalf("parsing address %s", ip)
	}

	return network.Contains(parsed)
}
