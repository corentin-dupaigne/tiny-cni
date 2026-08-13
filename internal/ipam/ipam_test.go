package ipam

import (
	"encoding/binary"
	"fmt"
	"net"
	"path/filepath"
	"strings"
	"testing"
)

// ---------- helpers ----------

func freshStorage(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "ipam.state")
}

func mustParseCIDR(t *testing.T, cidr string) *net.IPNet {
	t.Helper()
	_, ipnet, err := net.ParseCIDR(cidr)
	if err != nil {
		t.Fatalf("bad CIDR %q: %v", cidr, err)
	}
	return ipnet
}

func toUint32(s string) (uint32, error) {
	ip := net.ParseIP(strings.TrimSpace(s))
	if ip == nil {
		return 0, fmt.Errorf("invalid IP %q", s)
	}
	v4 := ip.To4()
	if v4 == nil {
		return 0, fmt.Errorf("not an IPv4 address: %q", s)
	}
	return binary.BigEndian.Uint32(v4), nil
}

func mustUint32(t *testing.T, s string) uint32 {
	t.Helper()
	u, err := toUint32(s)
	if err != nil {
		t.Fatalf("%v", err)
	}
	return u
}

func uint32ToIP(u uint32) string {
	b := make(net.IP, 4)
	binary.BigEndian.PutUint32(b, u)
	return b.String()
}

func networkAndBroadcast(t *testing.T, ipnet *net.IPNet) (uint32, uint32) {
	t.Helper()
	ip := ipnet.IP.To4()
	if ip == nil {
		t.Fatalf("test only supports IPv4 subnets, got %v", ipnet)
	}
	if len(ipnet.Mask) != 4 {
		t.Fatalf("unexpected mask length for %v", ipnet)
	}
	network := binary.BigEndian.Uint32(ip)
	mask := binary.BigEndian.Uint32(ipnet.Mask)
	broadcast := network | ^mask
	return network, broadcast
}

// firstUsable is the lowest allocatable address: network + 2,
// reserving the network address (.0) and the gateway (.1).
func firstUsable(network uint32) uint32 { return network + 2 }

// isUsable reports whether u is a valid pod address in the subnet:
// inside the range, and not the network, gateway, or broadcast address.
func isUsable(u, network, broadcast uint32) bool {
	return u >= firstUsable(network) && u < broadcast
}

func mustNewAllocator(t *testing.T, subnet, storagePath string) *Allocator {
	t.Helper()
	a, err := NewAllocator(subnet, storagePath)
	if err != nil {
		t.Fatalf("NewAllocator(%q, %q): %v", subnet, storagePath, err)
	}
	return a
}

// ---------- constructor / gateway ----------

func TestNewAllocatorRejectsInvalidSubnet(t *testing.T) {
	if _, err := NewAllocator("not-a-subnet", freshStorage(t)); err == nil {
		t.Fatalf("expected error for invalid subnet, got nil")
	}
}

func TestGatewayIP(t *testing.T) {
	subnet := "10.0.0.0/24"
	network, _ := networkAndBroadcast(t, mustParseCIDR(t, subnet))
	alloc := mustNewAllocator(t, subnet, freshStorage(t))

	want := uint32ToIP(network + 1)
	if got := alloc.GatewayIP(); got != want {
		t.Fatalf("GatewayIP() = %s, want %s", got, want)
	}
}

// ---------- sequential ----------

func TestSequentialAllocation(t *testing.T) {
	path := freshStorage(t)
	subnet := "10.0.0.0/24"
	network, broadcast := networkAndBroadcast(t, mustParseCIDR(t, subnet))
	alloc := mustNewAllocator(t, subnet, path)

	const n = 20
	var prev uint32
	for i := 0; i < n; i++ {
		ipStr, err := alloc.Allocate()
		if err != nil {
			t.Fatalf("allocation %d failed: %v", i, err)
		}
		u := mustUint32(t, ipStr)

		if !isUsable(u, network, broadcast) {
			t.Fatalf("allocation %d = %s is not a usable address in subnet %s", i, ipStr, subnet)
		}
		if i == 0 && u != firstUsable(network) {
			t.Fatalf("first allocation = %s, want %s", ipStr, uint32ToIP(firstUsable(network)))
		}
		if i > 0 && u != prev+1 {
			t.Fatalf("allocation %d not sequential: expected %s, got %s",
				i, uint32ToIP(prev+1), ipStr)
		}
		prev = u
	}
}

func TestStatePersistsAcrossCalls(t *testing.T) {
	path := freshStorage(t)
	subnet := "10.5.0.0/24"

	// Two separate Allocator instances sharing a storage path prove that
	// state lives in the file, not in the Allocator struct.
	first, err := mustNewAllocator(t, subnet, path).Allocate()
	if err != nil {
		t.Fatalf("first allocation failed: %v", err)
	}
	second, err := mustNewAllocator(t, subnet, path).Allocate()
	if err != nil {
		t.Fatalf("second allocation failed: %v", err)
	}
	if first == second {
		t.Fatalf("allocator did not advance: got %s twice", first)
	}
	if mustUint32(t, second) != mustUint32(t, first)+1 {
		t.Fatalf("expected %s after %s, got %s",
			uint32ToIP(mustUint32(t, first)+1), first, second)
	}
}

// ---------- different CIDRs ----------

func TestDifferentCIDRs(t *testing.T) {
	cases := []struct {
		name   string
		subnet string
		allocs int
	}{
		{"slash24", "10.1.2.0/24", 10},
		{"slash20", "172.16.0.0/20", 12},
		{"slash28", "10.0.0.0/28", 5},
		{"slash29", "192.168.1.0/29", 2},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := freshStorage(t)
			network, broadcast := networkAndBroadcast(t, mustParseCIDR(t, tc.subnet))
			alloc := mustNewAllocator(t, tc.subnet, path)

			var prev uint32
			for i := 0; i < tc.allocs; i++ {
				ipStr, err := alloc.Allocate()
				if err != nil {
					t.Fatalf("alloc %d in %s failed: %v", i, tc.subnet, err)
				}
				u := mustUint32(t, ipStr)

				if !isUsable(u, network, broadcast) {
					t.Fatalf("%s: %s is not a usable address", tc.subnet, ipStr)
				}
				if i == 0 && u != firstUsable(network) {
					t.Fatalf("%s: first allocation = %s, want %s",
						tc.subnet, ipStr, uint32ToIP(firstUsable(network)))
				}
				if i > 0 && u != prev+1 {
					t.Fatalf("%s: non-sequential, %s after %s",
						tc.subnet, ipStr, uint32ToIP(prev))
				}
				prev = u
			}
		})
	}
}

// ---------- different subnets ----------

func TestDifferentSubnets(t *testing.T) {
	pathA := freshStorage(t)
	pathB := freshStorage(t)
	subnetA := "10.10.0.0/24"
	subnetB := "192.168.7.0/24"

	netA, bcA := networkAndBroadcast(t, mustParseCIDR(t, subnetA))
	netB, bcB := networkAndBroadcast(t, mustParseCIDR(t, subnetB))

	allocA := mustNewAllocator(t, subnetA, pathA)
	allocB := mustNewAllocator(t, subnetB, pathB)

	var prevA, prevB uint32
	for i := 0; i < 6; i++ {
		aStr, err := allocA.Allocate()
		if err != nil {
			t.Fatalf("subnet A alloc %d failed: %v", i, err)
		}
		bStr, err := allocB.Allocate()
		if err != nil {
			t.Fatalf("subnet B alloc %d failed: %v", i, err)
		}

		a := mustUint32(t, aStr)
		b := mustUint32(t, bStr)

		if !isUsable(a, netA, bcA) {
			t.Fatalf("A allocation %s is not usable in %s", aStr, subnetA)
		}
		if !isUsable(b, netB, bcB) {
			t.Fatalf("B allocation %s is not usable in %s", bStr, subnetB)
		}
		if i > 0 {
			if a != prevA+1 {
				t.Fatalf("subnet A counter disturbed: %s after %s", aStr, uint32ToIP(prevA))
			}
			if b != prevB+1 {
				t.Fatalf("subnet B counter disturbed: %s after %s", bStr, uint32ToIP(prevB))
			}
		}
		prevA, prevB = a, b
	}
}

// ---------- exhaustion / boundary ----------

func TestExhaustionAndBoundary(t *testing.T) {
	path := freshStorage(t)
	subnet := "10.0.0.0/29"
	ipnet := mustParseCIDR(t, subnet)
	network, broadcast := networkAndBroadcast(t, ipnet)
	total := broadcast - network + 1
	alloc := mustNewAllocator(t, subnet, path)

	var got []uint32
	for {
		ipStr, err := alloc.Allocate()
		if err != nil {
			break // pool exhausted
		}
		got = append(got, mustUint32(t, ipStr))
		if uint32(len(got)) > total {
			t.Fatalf("allocated more IPs than exist in %s: %v", subnet, got)
		}
	}

	if len(got) == 0 {
		t.Fatalf("expected at least one allocatable IP in %s", subnet)
	}

	// first allocation from fresh storage must be network+2 (network and gateway reserved)
	if got[0] != firstUsable(network) {
		t.Fatalf("first allocation = %s, want %s", uint32ToIP(got[0]), uint32ToIP(firstUsable(network)))
	}

	seen := make(map[uint32]bool, len(got))
	for i, u := range got {
		if !isUsable(u, network, broadcast) {
			t.Fatalf("allocated %s is not a usable address in %s (reserved or out of range)",
				uint32ToIP(u), subnet)
		}
		if seen[u] {
			t.Fatalf("duplicate IP allocated: %s", uint32ToIP(u))
		}
		seen[u] = true
		if i > 0 && u != got[i-1]+1 {
			t.Fatalf("allocations not contiguous: %s then %s",
				uint32ToIP(got[i-1]), uint32ToIP(u))
		}
	}

	for i := 0; i < 3; i++ {
		if ip, err := alloc.Allocate(); err == nil {
			t.Fatalf("expected error after exhaustion, but got %s", ip)
		}
	}

	// usable count = total - network - gateway - broadcast = total - 3
	if uint32(len(got)) != total-3 {
		t.Fatalf("allocated %d IPs in %s; expected exactly %d (total %d minus network+gateway+broadcast)",
			len(got), subnet, total-3, total)
	}
}
