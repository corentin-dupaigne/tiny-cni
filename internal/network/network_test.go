package network

import (
	"encoding/json"
	"net/netip"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/corentin-dupaigne/tiny-cni/internal/ipam"
	"github.com/vishvananda/netlink"
)

const (
	// the loopback is the one link guaranteed to exist in any namespace, so it
	// stands in for a bridge that is already there
	existingLink = "lo"

	testSubnet  = "10.244.0.0/24"
	testGateway = "10.244.0.1/24"
)

// IFNAMSIZ - 1: the kernel refuses to create an interface with a longer name
const maxIfNameLen = 15

func storagePath(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "ipam.state")
}

// missingPath points somewhere inside a fresh temp dir, so opening it fails
// with ErrNotExist rather than anything the caller could have caused.
func missingPath(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "no-such-netns")
}

// regularFile is a plain file: it opens, so the entry points get past their
// first step, but setns rejects it because it is not a namespace.
func regularFile(t *testing.T) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "not-a-netns")
	if err := os.WriteFile(path, nil, 0644); err != nil {
		t.Fatalf("creating %s: %v", path, err)
	}
	return path
}

// readState decodes the allocator's state file the way a second process would.
func readState(t *testing.T, path string) ipam.IPAMState {
	t.Helper()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading IPAM state %s: %v", path, err)
	}

	var state ipam.IPAMState
	if err := json.Unmarshal(data, &state); err != nil {
		t.Fatalf("decoding IPAM state %s: %v", path, err)
	}
	return state
}

// allocate hands containerID an address and returns it.
func allocate(t *testing.T, storage, containerID string) netip.Prefix {
	t.Helper()

	alloc, err := ipam.NewAllocator(testSubnet, storage)
	if err != nil {
		t.Fatalf("NewAllocator(%q, %q): %v", testSubnet, storage, err)
	}

	ip, err := alloc.Allocate(containerID)
	if err != nil {
		t.Fatalf("allocating for %s: %v", containerID, err)
	}
	return ip
}

// ---------- generateRandName ----------

func TestGenerateRandNameKeepsPrefix(t *testing.T) {
	shape := regexp.MustCompile(`^tcni-[0-9a-f]{8}$`)

	name, err := generateRandName("tcni")
	if err != nil {
		t.Fatalf("generateRandName: %v", err)
	}

	if !shape.MatchString(name) {
		t.Errorf("generateRandName(%q) = %q, want it to match %s", "tcni", name, shape)
	}
}

// every pod on a node gets its own host-side veth, so two calls colliding would
// make the second ADD fail
func TestGenerateRandNameDoesNotRepeat(t *testing.T) {
	const runs = 1000

	seen := make(map[string]bool, runs)
	for i := range runs {
		name, err := generateRandName("tcni")
		if err != nil {
			t.Fatalf("generateRandName (call %d): %v", i, err)
		}

		if seen[name] {
			t.Fatalf("generateRandName returned %q twice in %d calls", name, i+1)
		}
		seen[name] = true
	}
}

// the name goes straight to the kernel, which caps interface names at IFNAMSIZ
func TestGenerateRandNameFitsAnInterfaceName(t *testing.T) {
	// 9 characters of prefix is all the 9 characters of suffix leave room for
	prefix := strings.Repeat("p", maxIfNameLen-9)

	name, err := generateRandName(prefix)
	if err != nil {
		t.Fatalf("generateRandName: %v", err)
	}

	if len(name) > maxIfNameLen {
		t.Errorf("generateRandName(%q) = %q, %d characters long, want at most %d",
			prefix, name, len(name), maxIfNameLen)
	}
}

// ---------- bridge ----------

// an existing bridge is adopted, not recreated: only the first pod on a node
// creates one
func TestBridgeReturnsTheExistingLink(t *testing.T) {
	gateway, err := netlink.ParseAddr(testGateway)
	if err != nil {
		t.Fatalf("parsing %s: %v", testGateway, err)
	}

	link, err := bridge(existingLink, *gateway)
	if err != nil {
		t.Fatalf("bridge(%q): %v", existingLink, err)
	}

	if link.Attrs().Name != existingLink {
		t.Errorf("bridge(%q) returned link %q, want %q",
			existingLink, link.Attrs().Name, existingLink)
	}
}

// ---------- Teardown ----------

// the runtime is entitled to call DEL once the sandbox is already gone, so a
// namespace that cannot be opened is not an error, but the address still needs
// to be released
func TestTeardownReleasesTheAddressWhenTheNamespaceIsMissing(t *testing.T) {
	storage := storagePath(t)
	const containerID = "pod-a"

	ip := allocate(t, storage, containerID)

	err := Teardown(TeardownParams{
		StoragePath: storage,
		Subnet:      testSubnet,
		ContainerID: containerID,
		Netns:       missingPath(t),
		IfName:      "eth0",
	})
	if err != nil {
		t.Fatalf("Teardown with a missing namespace: %v", err)
	}

	state := readState(t, storage)

	if _, ok := state.ContainerToIp[containerID]; ok {
		t.Errorf("Teardown left %s in the IPAM state", containerID)
	}
	if state.AllocatedSet[ip.Addr()] {
		t.Errorf("Teardown left %s allocated", ip.Addr())
	}
}

func TestTeardownFailsWhenTheNamespaceIsNotOne(t *testing.T) {
	err := Teardown(TeardownParams{
		StoragePath: storagePath(t),
		Subnet:      testSubnet,
		ContainerID: "pod-a",
		Netns:       regularFile(t),
		IfName:      "eth0",
	})
	if err == nil {
		t.Fatal("Teardown against a regular file returned no error")
	}

	if !strings.Contains(err.Error(), "switching netns") {
		t.Errorf("Teardown error = %v, want it to mention switching namespace", err)
	}
}

// the address is released even when the namespace work failed: the runtime
// retries DEL, and a leaked address would never come back on its own
func TestTeardownReleasesTheAddressWhenItCannotEnterTheNamespace(t *testing.T) {
	storage := storagePath(t)
	const containerID = "pod-a"

	ip := allocate(t, storage, containerID)

	err := Teardown(TeardownParams{
		StoragePath: storage,
		Subnet:      testSubnet,
		ContainerID: containerID,
		Netns:       regularFile(t),
		IfName:      "eth0",
	})
	if err == nil {
		t.Fatal("Teardown against a regular file returned no error")
	}

	state := readState(t, storage)

	if _, ok := state.ContainerToIp[containerID]; ok {
		t.Errorf("Teardown left %s in the IPAM state", containerID)
	}
	if state.AllocatedSet[ip.Addr()] {
		t.Errorf("Teardown left %s allocated", ip.Addr())
	}
}
