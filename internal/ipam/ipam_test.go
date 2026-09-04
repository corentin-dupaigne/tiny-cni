package ipam

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"testing"
)

// ---------- helpers ----------

func freshStorage(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "ipam.state")
}

func mustNewAllocator(t *testing.T, subnet, storagePath string) *Allocator {
	t.Helper()
	a, err := NewAllocator(subnet, storagePath)
	if err != nil {
		t.Fatalf("NewAllocator(%q, %q): %v", subnet, storagePath, err)
	}
	return a
}

func mustParsePrefix(t *testing.T, cidr string) netip.Prefix {
	t.Helper()
	p, err := netip.ParsePrefix(cidr)
	if err != nil {
		t.Fatalf("bad CIDR %q: %v", cidr, err)
	}
	return p.Masked()
}

// networkAddr / gatewayAddr / broadcastAddr are the three addresses an IPAM
// must never hand out for an IPv4 subnet.
func networkAddr(t *testing.T, p netip.Prefix) netip.Addr {
	t.Helper()
	if !p.Addr().Is4() {
		t.Fatalf("test helpers only support IPv4, got %s", p)
	}
	return p.Masked().Addr()
}

func gatewayAddr(t *testing.T, p netip.Prefix) netip.Addr {
	t.Helper()
	return networkAddr(t, p).Next()
}

func broadcastAddr(t *testing.T, p netip.Prefix) netip.Addr {
	t.Helper()
	b := networkAddr(t, p).As4()
	n := binary.BigEndian.Uint32(b[:])
	host := uint32(0xffffffff) >> p.Bits()
	binary.BigEndian.PutUint32(b[:], n|host)
	return netip.AddrFrom4(b)
}

// usableCount is the number of pod addresses a subnet can hold:
// every address minus network, gateway and broadcast.
func usableCount(t *testing.T, p netip.Prefix) int {
	t.Helper()
	total := 1 << (32 - p.Bits())
	return total - 3
}

// safeAllocate calls Allocate but converts a panic into an error so that one
// broken case cannot take down the whole test binary. It is also safe to call
// from a goroutine, where an unrecovered panic would be fatal.
func safeAllocate(a *Allocator, containerID string) (p netip.Prefix, err error) {
	defer func() {
		if r := recover(); r != nil {
			p, err = netip.Prefix{}, fmt.Errorf("Allocate(%q) panicked: %v", containerID, r)
		}
	}()
	return a.Allocate(containerID)
}

// assertUsable checks the core invariant of every allocation: the returned
// prefix carries the subnet mask and an address that is inside the subnet and
// is not one of the three reserved addresses.
func assertUsable(t *testing.T, got netip.Prefix, subnet netip.Prefix, ctx string) {
	t.Helper()
	if got.Bits() != subnet.Bits() {
		t.Errorf("%s: prefix length = /%d, want /%d (%s)", ctx, got.Bits(), subnet.Bits(), got)
	}
	addr := got.Addr()
	if !addr.IsValid() {
		t.Fatalf("%s: allocated invalid address", ctx)
	}
	if !subnet.Contains(addr) {
		t.Errorf("%s: %s is outside subnet %s", ctx, addr, subnet)
	}
	switch addr {
	case networkAddr(t, subnet):
		t.Errorf("%s: allocated the network address %s", ctx, addr)
	case gatewayAddr(t, subnet):
		t.Errorf("%s: allocated the gateway address %s", ctx, addr)
	case broadcastAddr(t, subnet):
		t.Errorf("%s: allocated the broadcast address %s", ctx, addr)
	}
}

func readState(t *testing.T, path string) IPAMState {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading state file %s: %v", path, err)
	}
	var state IPAMState
	if err := json.Unmarshal(data, &state); err != nil {
		t.Fatalf("unmarshalling state file %s: %v\n%s", path, err, data)
	}
	return state
}

// ---------- constructor / gateway ----------

func TestNewAllocatorRejectsInvalidSubnet(t *testing.T) {
	cases := []string{
		"not-a-subnet",
		"10.0.0.0",
		"10.0.0.0/33",
		"10.0.0.0/-1",
		"",
	}
	for _, subnet := range cases {
		t.Run(subnet, func(t *testing.T) {
			if _, err := NewAllocator(subnet, freshStorage(t)); err == nil {
				t.Fatalf("NewAllocator(%q) = nil error, want error", subnet)
			}
		})
	}
}

func TestNewAllocatorAcceptsValidSubnets(t *testing.T) {
	for _, subnet := range []string{"10.0.0.0/24", "172.16.0.0/20", "192.168.1.0/29", "10.0.0.0/8"} {
		if _, err := NewAllocator(subnet, freshStorage(t)); err != nil {
			t.Errorf("NewAllocator(%q): unexpected error %v", subnet, err)
		}
	}
}

func TestNewAllocatorDoesNotTouchStorage(t *testing.T) {
	path := freshStorage(t)
	mustNewAllocator(t, "10.0.0.0/24", path)
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("NewAllocator created %s before any allocation (stat err = %v)", path, err)
	}
}

func TestGatewayIP(t *testing.T) {
	cases := map[string]string{
		"10.0.0.0/24":    "10.0.0.1",
		"172.16.0.0/20":  "172.16.0.1",
		"192.168.1.0/29": "192.168.1.1",
		"10.1.2.64/26":   "10.1.2.65",
		"10.1.2.100/24":  "10.1.2.1", // unmasked input must be masked first
	}
	for subnet, want := range cases {
		t.Run(subnet, func(t *testing.T) {
			alloc := mustNewAllocator(t, subnet, freshStorage(t))
			if got := alloc.GatewayIP(); got != want {
				t.Fatalf("GatewayIP() = %s, want %s", got, want)
			}
		})
	}
}

// ---------- single allocation ----------

func TestAllocateFirstAddress(t *testing.T) {
	subnet := mustParsePrefix(t, "10.0.0.0/24")
	alloc := mustNewAllocator(t, subnet.String(), freshStorage(t))

	got, err := safeAllocate(alloc, "container-a")
	if err != nil {
		t.Fatalf("Allocate: %v", err)
	}
	assertUsable(t, got, subnet, "first allocation")

	// network and gateway are reserved, so the first pod IP is network+2.
	want := gatewayAddr(t, subnet).Next()
	if got.Addr() != want {
		t.Fatalf("first allocation = %s, want %s", got.Addr(), want)
	}
}

func TestAllocateReturnsSubnetMask(t *testing.T) {
	for _, subnet := range []string{"10.0.0.0/24", "172.16.0.0/20", "10.0.0.0/28"} {
		t.Run(subnet, func(t *testing.T) {
			p := mustParsePrefix(t, subnet)
			alloc := mustNewAllocator(t, subnet, freshStorage(t))
			got, err := safeAllocate(alloc, "c1")
			if err != nil {
				t.Fatalf("Allocate: %v", err)
			}
			if got.Bits() != p.Bits() {
				t.Fatalf("Allocate returned %s, want a /%d prefix", got, p.Bits())
			}
		})
	}
}

func TestAllocateCreatesStateFile(t *testing.T) {
	path := freshStorage(t)
	subnet := "10.0.0.0/24"
	alloc := mustNewAllocator(t, subnet, path)

	got, err := safeAllocate(alloc, "container-a")
	if err != nil {
		t.Fatalf("Allocate: %v", err)
	}

	state := readState(t, path)
	if ip, ok := state.ContainerToIp["container-a"]; !ok {
		t.Errorf("state file has no entry for container-a: %+v", state)
	} else if ip != got.Addr() {
		t.Errorf("state file maps container-a to %s, but Allocate returned %s", ip, got.Addr())
	}
	if !state.AllocatedSet[got.Addr()] {
		t.Errorf("state file does not mark %s as allocated: %+v", got.Addr(), state.AllocatedSet)
	}

	// The reserved addresses must be marked as taken from the very first write.
	p := mustParsePrefix(t, subnet)
	if !state.AllocatedSet[networkAddr(t, p)] {
		t.Errorf("network address %s is not reserved in the state file", networkAddr(t, p))
	}
	if !state.AllocatedSet[gatewayAddr(t, p)] {
		t.Errorf("gateway address %s is not reserved in the state file", gatewayAddr(t, p))
	}
}

// ---------- many allocations ----------

func TestAllocateManyAreUnique(t *testing.T) {
	subnet := mustParsePrefix(t, "10.0.0.0/24")
	alloc := mustNewAllocator(t, subnet.String(), freshStorage(t))

	const n = 50
	seen := make(map[netip.Addr]string, n)
	for i := 0; i < n; i++ {
		id := fmt.Sprintf("container-%02d", i)
		got, err := safeAllocate(alloc, id)
		if err != nil {
			t.Fatalf("allocation %d (%s) failed: %v", i, id, err)
		}
		assertUsable(t, got, subnet, fmt.Sprintf("allocation %d", i))
		if prev, dup := seen[got.Addr()]; dup {
			t.Fatalf("allocation %d: %s already handed to %s", i, got.Addr(), prev)
		}
		seen[got.Addr()] = id
	}
}

func TestAllocateSameContainerTwiceIsIdempotent(t *testing.T) {
	alloc := mustNewAllocator(t, "10.0.0.0/24", freshStorage(t))

	first, err := safeAllocate(alloc, "container-a")
	if err != nil {
		t.Fatalf("first Allocate: %v", err)
	}
	second, err := safeAllocate(alloc, "container-a")
	if err != nil {
		t.Fatalf("second Allocate: %v", err)
	}
	if first != second {
		t.Fatalf("re-allocating the same container returned %s then %s; "+
			"a repeated CNI ADD must be idempotent and the old IP must not leak", first, second)
	}
}

func TestAllocateReusesAddressesAfterDeallocate(t *testing.T) {
	path := freshStorage(t)
	alloc := mustNewAllocator(t, "10.0.0.0/24", path)

	first, err := safeAllocate(alloc, "container-a")
	if err != nil {
		t.Fatalf("Allocate: %v", err)
	}
	alloc.Deallocate("container-a")

	state := readState(t, path)
	if state.AllocatedSet[first.Addr()] {
		t.Errorf("after Deallocate, %s is still marked allocated in the state file", first.Addr())
	}
	if _, ok := state.ContainerToIp["container-a"]; ok {
		t.Errorf("after Deallocate, container-a still has a mapping in the state file")
	}
}

func TestAllocatePersistsAcrossAllocatorInstances(t *testing.T) {
	path := freshStorage(t)
	subnet := mustParsePrefix(t, "10.5.0.0/24")

	// Two Allocator instances sharing a storage path prove the state lives in
	// the file, not in the struct.
	first, err := safeAllocate(mustNewAllocator(t, subnet.String(), path), "container-a")
	if err != nil {
		t.Fatalf("first allocation: %v", err)
	}
	second, err := safeAllocate(mustNewAllocator(t, subnet.String(), path), "container-b")
	if err != nil {
		t.Fatalf("second allocation: %v", err)
	}
	if first.Addr() == second.Addr() {
		t.Fatalf("a fresh Allocator on the same storage handed out %s twice", first.Addr())
	}
	assertUsable(t, second, subnet, "second allocation")

	state := readState(t, path)
	if len(state.ContainerToIp) != 2 {
		t.Fatalf("state file holds %d containers, want 2: %+v", len(state.ContainerToIp), state.ContainerToIp)
	}
}

func TestAllocateAcrossCIDRSizes(t *testing.T) {
	cases := []struct {
		name   string
		subnet string
		allocs int
	}{
		{"slash24", "10.1.2.0/24", 20},
		{"slash20", "172.16.0.0/20", 12},
		{"slash26", "10.1.2.64/26", 10},
		{"slash28", "10.0.0.0/28", 5},
		{"slash29", "192.168.1.0/29", 2},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			subnet := mustParsePrefix(t, tc.subnet)
			alloc := mustNewAllocator(t, tc.subnet, freshStorage(t))

			seen := make(map[netip.Addr]bool, tc.allocs)
			for i := 0; i < tc.allocs; i++ {
				got, err := safeAllocate(alloc, fmt.Sprintf("c-%d", i))
				if err != nil {
					t.Fatalf("%s: allocation %d failed: %v", tc.subnet, i, err)
				}
				assertUsable(t, got, subnet, fmt.Sprintf("%s allocation %d", tc.subnet, i))
				if seen[got.Addr()] {
					t.Fatalf("%s: duplicate allocation %s", tc.subnet, got.Addr())
				}
				seen[got.Addr()] = true
			}
		})
	}
}

func TestAllocateSubnetsAreIndependent(t *testing.T) {
	subnetA := mustParsePrefix(t, "10.10.0.0/24")
	subnetB := mustParsePrefix(t, "192.168.7.0/24")
	allocA := mustNewAllocator(t, subnetA.String(), freshStorage(t))
	allocB := mustNewAllocator(t, subnetB.String(), freshStorage(t))

	for i := 0; i < 6; i++ {
		id := fmt.Sprintf("c-%d", i)
		a, err := safeAllocate(allocA, id)
		if err != nil {
			t.Fatalf("subnet A allocation %d: %v", i, err)
		}
		b, err := safeAllocate(allocB, id)
		if err != nil {
			t.Fatalf("subnet B allocation %d: %v", i, err)
		}
		assertUsable(t, a, subnetA, fmt.Sprintf("A allocation %d", i))
		assertUsable(t, b, subnetB, fmt.Sprintf("B allocation %d", i))
	}
}

// ---------- exhaustion / boundaries ----------

func TestAllocateExhaustsPoolWithoutOverrunning(t *testing.T) {
	for _, cidr := range []string{"10.0.0.0/29", "10.0.0.0/28", "192.168.1.0/30"} {
		t.Run(cidr, func(t *testing.T) {
			subnet := mustParsePrefix(t, cidr)
			alloc := mustNewAllocator(t, cidr, freshStorage(t))
			want := usableCount(t, subnet)

			seen := make(map[netip.Addr]bool)
			var lastErr error
			for i := 0; i <= want+3; i++ {
				got, err := safeAllocate(alloc, fmt.Sprintf("c-%d", i))
				if err != nil {
					lastErr = err
					break
				}
				assertUsable(t, got, subnet, fmt.Sprintf("%s allocation %d", cidr, i))
				if seen[got.Addr()] {
					t.Fatalf("%s: duplicate allocation %s", cidr, got.Addr())
				}
				seen[got.Addr()] = true
			}

			if lastErr == nil {
				t.Fatalf("%s: allocator never reported exhaustion after %d allocations (capacity %d)",
					cidr, len(seen), want)
			}
			if len(seen) != want {
				t.Fatalf("%s: allocated %d addresses, want exactly %d (total minus network, gateway, broadcast); last error: %v",
					cidr, len(seen), want, lastErr)
			}

			// Exhaustion must be stable, not a one-off.
			for i := 0; i < 3; i++ {
				if got, err := safeAllocate(alloc, fmt.Sprintf("late-%d", i)); err == nil {
					t.Fatalf("%s: expected exhaustion error, got %s", cidr, got)
				}
			}
		})
	}
}

func TestAllocateOnExhaustedPoolDoesNotPanic(t *testing.T) {
	// A /30 holds a single usable address; the second call must return an
	// error rather than crashing the plugin.
	alloc := mustNewAllocator(t, "10.0.0.0/30", freshStorage(t))
	if _, err := safeAllocate(alloc, "c-0"); err != nil {
		t.Fatalf("first allocation in /30 failed: %v", err)
	}
	got, err := safeAllocate(alloc, "c-1")
	if err == nil {
		t.Fatalf("second allocation in /30 returned %s, want an exhaustion error", got)
	}
}

func TestAllocateNeverReturnsReservedAddresses(t *testing.T) {
	subnet := mustParsePrefix(t, "10.0.0.0/28")
	alloc := mustNewAllocator(t, subnet.String(), freshStorage(t))

	for i := 0; i < usableCount(t, subnet)+2; i++ {
		got, err := safeAllocate(alloc, fmt.Sprintf("c-%d", i))
		if err != nil {
			break
		}
		assertUsable(t, got, subnet, fmt.Sprintf("allocation %d", i))
	}
}

func TestAllocateDoesNotEscapeSubnet(t *testing.T) {
	// A subnet that ends at a byte boundary catches off-by-one walks into the
	// neighbouring /24.
	subnet := mustParsePrefix(t, "10.0.0.240/28")
	alloc := mustNewAllocator(t, subnet.String(), freshStorage(t))

	for i := 0; i < usableCount(t, subnet)+2; i++ {
		got, err := safeAllocate(alloc, fmt.Sprintf("c-%d", i))
		if err != nil {
			break
		}
		if !subnet.Contains(got.Addr()) {
			t.Fatalf("allocation %d = %s escaped subnet %s", i, got.Addr(), subnet)
		}
		assertUsable(t, got, subnet, fmt.Sprintf("allocation %d", i))
	}
}

// ---------- storage failures ----------

func TestAllocateWithCorruptStateFile(t *testing.T) {
	path := freshStorage(t)
	if err := os.WriteFile(path, []byte("{not json"), 0644); err != nil {
		t.Fatalf("seeding corrupt state: %v", err)
	}
	alloc := mustNewAllocator(t, "10.0.0.0/24", path)

	if got, err := safeAllocate(alloc, "c-0"); err == nil {
		t.Fatalf("Allocate over a corrupt state file returned %s, want an error", got)
	}
}

func TestAllocateWithUnwritableStoragePath(t *testing.T) {
	// A path whose parent directory does not exist cannot be opened.
	path := filepath.Join(t.TempDir(), "missing-dir", "ipam.state")
	alloc := mustNewAllocator(t, "10.0.0.0/24", path)

	if got, err := safeAllocate(alloc, "c-0"); err == nil {
		t.Fatalf("Allocate on unwritable path returned %s, want an error", got)
	}
}

func TestAllocateResumesFromSeededState(t *testing.T) {
	path := freshStorage(t)
	subnet := mustParsePrefix(t, "10.0.0.0/24")
	taken := netip.MustParseAddr("10.0.0.2")

	state := IPAMState{
		ContainerToIp: map[string]netip.Addr{"pre-existing": taken},
		AllocatedSet: map[netip.Addr]bool{
			networkAddr(t, subnet): true,
			gatewayAddr(t, subnet): true,
			taken:                  true,
		},
	}
	data, err := json.Marshal(state)
	if err != nil {
		t.Fatalf("marshalling seed state: %v", err)
	}
	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatalf("seeding state: %v", err)
	}

	alloc := mustNewAllocator(t, subnet.String(), path)
	got, err := safeAllocate(alloc, "container-b")
	if err != nil {
		t.Fatalf("Allocate over seeded state: %v", err)
	}
	assertUsable(t, got, subnet, "allocation over seeded state")
	if got.Addr() == taken {
		t.Fatalf("Allocate handed out %s, which was already owned by pre-existing", taken)
	}

	after := readState(t, path)
	if after.ContainerToIp["pre-existing"] != taken {
		t.Errorf("pre-existing mapping was lost: %+v", after.ContainerToIp)
	}
}
