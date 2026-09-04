package ipam

import (
	"bufio"
	"fmt"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// result is one goroutine's or one child process's allocation outcome.
type result struct {
	id   string
	addr netip.Addr
	err  error
}

// runConcurrent fires n goroutines that each allocate once through allocFor(i),
// releasing them all at the same instant to maximise contention.
func runConcurrent(t *testing.T, n int, allocFor func(i int) *Allocator) []result {
	t.Helper()

	var start sync.WaitGroup
	var done sync.WaitGroup
	start.Add(1)
	results := make([]result, n)

	for i := 0; i < n; i++ {
		done.Add(1)
		go func(i int) {
			defer done.Done()
			id := fmt.Sprintf("container-%03d", i)
			start.Wait()
			p, err := safeAllocate(allocFor(i), id)
			results[i] = result{id: id, addr: p.Addr(), err: err}
		}(i)
	}

	start.Done()
	done.Wait()
	return results
}

// assertNoDuplicates checks the property that matters most under concurrency:
// two containers must never end up with the same IP.
func assertNoDuplicates(t *testing.T, results []result, subnet netip.Prefix) int {
	t.Helper()
	owner := make(map[netip.Addr]string, len(results))
	ok := 0
	for _, r := range results {
		if r.err != nil {
			continue
		}
		ok++
		assertUsable(t, netip.PrefixFrom(r.addr, subnet.Bits()), subnet, r.id)
		if prev, dup := owner[r.addr]; dup {
			t.Errorf("race: %s was handed to both %s and %s", r.addr, prev, r.id)
			continue
		}
		owner[r.addr] = r.id
	}
	return ok
}

// TestConcurrentAllocateSharedAllocator hammers a single Allocator from many
// goroutines. The flock in withLockedState must serialise them.
func TestConcurrentAllocateSharedAllocator(t *testing.T) {
	subnet := mustParsePrefix(t, "10.0.0.0/24")
	path := freshStorage(t)
	alloc := mustNewAllocator(t, subnet.String(), path)

	const n = 64
	results := runConcurrent(t, n, func(int) *Allocator { return alloc })

	for _, r := range results {
		if r.err != nil {
			t.Errorf("%s failed although the /24 has room for %d addresses: %v",
				r.id, usableCount(t, subnet), r.err)
		}
	}
	if got := assertNoDuplicates(t, results, subnet); got != n {
		t.Errorf("%d/%d allocations succeeded", got, n)
	}

	state := readState(t, path)
	if len(state.ContainerToIp) != n {
		t.Errorf("state file records %d containers, want %d — writes were lost",
			len(state.ContainerToIp), n)
	}
	for _, r := range results {
		if r.err != nil {
			continue
		}
		if got, ok := state.ContainerToIp[r.id]; !ok {
			t.Errorf("state file lost the mapping for %s (%s)", r.id, r.addr)
		} else if got != r.addr {
			t.Errorf("state file maps %s to %s, but Allocate returned %s", r.id, got, r.addr)
		}
		if !state.AllocatedSet[r.addr] {
			t.Errorf("state file does not mark %s (%s) as allocated", r.addr, r.id)
		}
	}
}

// TestConcurrentAllocateDistinctAllocators uses one Allocator per goroutine
// over one storage file: nothing is shared in memory, so only the file lock
// can keep them consistent.
func TestConcurrentAllocateDistinctAllocators(t *testing.T) {
	subnet := mustParsePrefix(t, "10.20.0.0/24")
	path := freshStorage(t)

	const n = 32
	allocators := make([]*Allocator, n)
	for i := range allocators {
		allocators[i] = mustNewAllocator(t, subnet.String(), path)
	}

	results := runConcurrent(t, n, func(i int) *Allocator { return allocators[i] })

	for _, r := range results {
		if r.err != nil {
			t.Errorf("%s failed: %v", r.id, r.err)
		}
	}
	assertNoDuplicates(t, results, subnet)

	if state := readState(t, path); len(state.ContainerToIp) != n {
		t.Errorf("state file records %d containers, want %d", len(state.ContainerToIp), n)
	}
}

// TestConcurrentAllocateUnderExhaustion oversubscribes a /28: exactly the
// usable addresses must be handed out, the rest must get clean errors, and no
// address may be handed out twice.
func TestConcurrentAllocateUnderExhaustion(t *testing.T) {
	subnet := mustParsePrefix(t, "10.30.0.0/28")
	alloc := mustNewAllocator(t, subnet.String(), freshStorage(t))
	capacity := usableCount(t, subnet)

	n := capacity * 3
	results := runConcurrent(t, n, func(int) *Allocator { return alloc })

	ok := assertNoDuplicates(t, results, subnet)
	if ok != capacity {
		t.Errorf("%d concurrent allocations succeeded in %s, want exactly %d",
			ok, subnet, capacity)
	}
	for _, r := range results {
		if r.err != nil && strings.Contains(r.err.Error(), "panicked") {
			t.Errorf("%s: %v", r.id, r.err)
		}
	}
}

// TestConcurrentAllocateRepeatedContainerID has every goroutine allocate for
// the same container: a repeated CNI ADD must converge on a single IP.
func TestConcurrentAllocateRepeatedContainerID(t *testing.T) {
	subnet := mustParsePrefix(t, "10.40.0.0/24")
	path := freshStorage(t)
	alloc := mustNewAllocator(t, subnet.String(), path)

	const n = 16
	var start sync.WaitGroup
	var done sync.WaitGroup
	start.Add(1)
	addrs := make([]netip.Addr, n)

	for i := 0; i < n; i++ {
		done.Add(1)
		go func(i int) {
			defer done.Done()
			start.Wait()
			p, err := safeAllocate(alloc, "same-container")
			if err != nil {
				t.Errorf("goroutine %d: %v", i, err)
				return
			}
			addrs[i] = p.Addr()
		}(i)
	}
	start.Done()
	done.Wait()

	distinct := make(map[netip.Addr]bool)
	for _, a := range addrs {
		if a.IsValid() {
			distinct[a] = true
		}
	}
	if len(distinct) > 1 {
		t.Errorf("concurrent ADDs for one container returned %d different IPs (%v); "+
			"the extra addresses are leaked", len(distinct), distinct)
	}

	if state := readState(t, path); len(state.ContainerToIp) != 1 {
		t.Errorf("state file records %d containers, want 1: %+v",
			len(state.ContainerToIp), state.ContainerToIp)
	}
}

// TestConcurrentAllocateAndDeallocate mixes both operations to make sure the
// lock covers the read-modify-write of either path.
func TestConcurrentAllocateAndDeallocate(t *testing.T) {
	subnet := mustParsePrefix(t, "10.50.0.0/24")
	path := freshStorage(t)
	alloc := mustNewAllocator(t, subnet.String(), path)

	const n = 24
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			id := fmt.Sprintf("container-%03d", i)
			if _, err := safeAllocate(alloc, id); err != nil {
				t.Errorf("%s: %v", id, err)
				return
			}
			if i%2 == 0 {
				alloc.Deallocate(id)
			}
		}(i)
	}
	wg.Wait()

	// Whatever the interleaving, the file must still be parseable and every
	// surviving mapping must agree with the allocated set.
	state := readState(t, path)
	for id, ip := range state.ContainerToIp {
		if !state.AllocatedSet[ip] {
			t.Errorf("state is inconsistent: %s owns %s but it is not in AllocatedSet", id, ip)
		}
	}
}

// ---------- cross-process ----------

const childEnv = "TINY_CNI_IPAM_CHILD"

// TestCrossProcessConcurrentAllocate re-executes the test binary several times
// so that real processes contend for the flock — the case the in-process tests
// cannot cover, since flock is what makes concurrent CNI invocations safe.
func TestCrossProcessConcurrentAllocate(t *testing.T) {
	const (
		subnetStr = "10.60.0.0/24"
		children  = 4
		perChild  = 8
		wantTotal = children * perChild
	)

	if spec := os.Getenv(childEnv); spec != "" {
		runChildAllocations(t, spec, subnetStr, perChild)
		return
	}

	exe, err := os.Executable()
	if err != nil {
		t.Skipf("cannot locate test binary: %v", err)
	}
	dir := t.TempDir()
	statePath := filepath.Join(dir, "ipam.state")

	var wg sync.WaitGroup
	outputs := make([]string, children)
	errs := make([]error, children)
	for i := 0; i < children; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			outPath := filepath.Join(dir, fmt.Sprintf("child-%d.out", i))
			outputs[i] = outPath
			cmd := exec.Command(exe, "-test.run", "^TestCrossProcessConcurrentAllocate$")
			cmd.Env = append(os.Environ(),
				fmt.Sprintf("%s=%d|%s|%s", childEnv, i, statePath, outPath))
			if out, err := cmd.CombinedOutput(); err != nil {
				errs[i] = fmt.Errorf("child %d failed: %v\n%s", i, err, out)
			}
		}(i)
	}
	wg.Wait()

	for _, err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}

	subnet := mustParsePrefix(t, subnetStr)
	owner := make(map[netip.Addr]string, wantTotal)
	total := 0
	for _, out := range outputs {
		f, err := os.Open(out)
		if err != nil {
			t.Fatalf("reading child output: %v", err)
		}
		scanner := bufio.NewScanner(f)
		for scanner.Scan() {
			id, addrStr, ok := strings.Cut(scanner.Text(), " ")
			if !ok {
				t.Fatalf("malformed child output line %q", scanner.Text())
			}
			addr, err := netip.ParseAddr(addrStr)
			if err != nil {
				t.Fatalf("child reported unparseable address %q: %v", addrStr, err)
			}
			total++
			assertUsable(t, netip.PrefixFrom(addr, subnet.Bits()), subnet, id)
			if prev, dup := owner[addr]; dup {
				t.Errorf("cross-process race: %s handed to both %s and %s", addr, prev, id)
				continue
			}
			owner[addr] = id
		}
		f.Close()
		if err := scanner.Err(); err != nil {
			t.Fatalf("scanning child output: %v", err)
		}
	}

	if total != wantTotal {
		t.Fatalf("children reported %d allocations, want %d", total, wantTotal)
	}

	state := readState(t, statePath)
	if len(state.ContainerToIp) != wantTotal {
		t.Errorf("state file records %d containers, want %d — a process overwrote another's write",
			len(state.ContainerToIp), wantTotal)
	}
}

// runChildAllocations is the child half of TestCrossProcessConcurrentAllocate:
// it allocates perChild addresses and writes "id addr" lines to its output file.
func runChildAllocations(t *testing.T, spec, subnetStr string, perChild int) {
	t.Helper()
	parts := strings.SplitN(spec, "|", 3)
	if len(parts) != 3 {
		t.Fatalf("malformed child spec %q", spec)
	}
	idx, statePath, outPath := parts[0], parts[1], parts[2]

	alloc := mustNewAllocator(t, subnetStr, statePath)

	var b strings.Builder
	for i := 0; i < perChild; i++ {
		id := fmt.Sprintf("child%s-container%02d", idx, i)
		p, err := safeAllocate(alloc, id)
		if err != nil {
			t.Fatalf("child %s: allocation %d failed: %v", idx, i, err)
		}
		fmt.Fprintf(&b, "%s %s\n", id, p.Addr())
	}
	if err := os.WriteFile(outPath, []byte(b.String()), 0644); err != nil {
		t.Fatalf("child %s: writing results: %v", idx, err)
	}
}
