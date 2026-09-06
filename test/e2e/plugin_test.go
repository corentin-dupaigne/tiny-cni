//go:build linux && e2e

// This file is the part of the harness that knows about tiny-cni: how a node is
// prepared, how the plugin is invoked, and what a pod looks like afterwards.
// The namespace machinery it builds on lives in netns_test.go.
//
// The plugin is driven the way a container runtime drives it: the built binary
// is executed with the CNI_* variables in its environment and the network
// config on stdin, and it answers with a result on stdout. Nothing here reaches
// into internal/ -- what is under test is the plugin as a runtime sees it,
// including its argument handling, its exit codes and the shape of its result.
package e2e

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	cniv1 "github.com/containernetworking/cni/pkg/types/100"
	"github.com/vishvananda/netlink"
)

const (
	// the version the suite speaks; it selects the result type below
	cniVersion = "1.0.0"

	// the bridge the plugin is asked to build the node around. Every test runs
	// on its own network namespace, so a fixed name cannot collide.
	bridgeName = "tcni-br0"

	// the prefix host-side veth names are generated from
	vethPrefix = "tcni"

	subnet = "10.244.0.0/24"

	// the interface name runtimes conventionally ask for
	defaultIfName = "eth0"
)

// the binary under test, resolved once in TestMain. A failure to find it is
// reported by the first test that needs it rather than at startup, so that
// listing the tests works without a build.
var (
	pluginBin    string
	pluginBinErr error
)

func TestMain(m *testing.M) {
	pluginBin, pluginBinErr = resolvePluginBin()

	os.Exit(m.Run())
}

// resolvePluginBin returns the path of the plugin binary to exercise. The
// Makefile builds it and points TINY_CNI_BIN at it, because the suite itself
// runs under sudo and building as root would write to root's build cache.
func resolvePluginBin() (string, error) {
	path := os.Getenv("TINY_CNI_BIN")
	if path == "" {
		return "", fmt.Errorf("TINY_CNI_BIN is not set, run the suite with: make test-e2e")
	}

	path, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolving TINY_CNI_BIN: %w", err)
	}

	if _, err := os.Stat(path); err != nil {
		return "", fmt.Errorf("plugin binary %s: %w", path, err)
	}

	return path, nil
}

// node is a throwaway network namespace standing in for a node, together with
// the network config the plugin is invoked with on it.
type node struct {
	config []byte
}

// newNode gives the calling test a clean node to work with: a private network
// namespace it is pinned to, and an IPAM state file of its own.
func newNode(t *testing.T) *node {
	t.Helper()

	if pluginBinErr != nil {
		t.Fatalf("%v", pluginBinErr)
	}

	requireRoot(t)
	enterNodeNetns(t)

	// a state file per test, so allocations start from a known point and tests
	// cannot deallocate each other's addresses
	storagePath := filepath.Join(t.TempDir(), "ipam-state.json")

	config := map[string]any{
		"cniVersion": cniVersion,
		"name":       "tinynet",
		"type":       "tiny-cni",
		"bridge":     bridgeName,
		"prefix":     vethPrefix,
		"ipam": map[string]any{
			"type":        "tiny-cni",
			"subnet":      subnet,
			"storagePath": storagePath,
		},
	}

	raw, err := json.Marshal(config)
	if err != nil {
		t.Fatalf("building network config: %v", err)
	}

	return &node{config: raw}
}

// invoke runs one CNI command against the plugin and returns what it printed on
// stdout. It does not fail the test: tests covering error paths read the error,
// the happy path goes through add and del.
//
// The child inherits the namespace of the thread that forks it, and the test
// goroutine is pinned to the node namespace for its whole run, so the plugin
// runs on the node the same way it would under a runtime.
func (n *node) invoke(command string, containerID string, netns string, ifName string) ([]byte, error) {
	cmd := exec.Command(pluginBin)
	cmd.Stdin = bytes.NewReader(n.config)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	cmd.Env = append(os.Environ(),
		"CNI_COMMAND="+command,
		"CNI_CONTAINERID="+containerID,
		"CNI_NETNS="+netns,
		"CNI_IFNAME="+ifName,
		// no chained plugins to look up, but the spec makes it mandatory
		"CNI_PATH="+filepath.Dir(pluginBin),
	)

	err := cmd.Run()
	if err != nil {
		// on failure the plugin is expected to describe itself on stdout, as
		// an error result; its logs go to stderr
		return stdout.Bytes(), fmt.Errorf("%s failed: %w\nstdout: %s\nstderr: %s",
			command, err, stdout.String(), stderr.String())
	}

	return stdout.Bytes(), nil
}

// add runs ADD and fails the test if the plugin does not accept it or answers
// with something that is not a result.
func (n *node) add(t *testing.T, containerID string, netns string, ifName string) *cniv1.Result {
	t.Helper()

	out, err := n.invoke("ADD", containerID, netns, ifName)
	if err != nil {
		t.Fatalf("%v", err)
	}

	var res cniv1.Result
	if err := json.Unmarshal(out, &res); err != nil {
		t.Fatalf("parsing ADD result %q: %v", out, err)
	}

	return &res
}

// del runs DEL and fails the test if the plugin does not accept it.
func (n *node) del(t *testing.T, containerID string, netns string, ifName string) {
	t.Helper()

	if _, err := n.invoke("DEL", containerID, netns, ifName); err != nil {
		t.Fatalf("%v", err)
	}
}

// pod is a namespace that has been through a successful ADD.
type pod struct {
	name        string
	containerID string
	netns       string
	ifName      string
	addr        netlink.Addr
	result      *cniv1.Result
}

// newPod creates a pod namespace, runs ADD against it, and reads back the
// address the IPAM allocated. It is the happy path: any failure along the way
// fails the test.
func newPod(t *testing.T, n *node, name string) pod {
	t.Helper()

	p := pod{
		name:        name,
		containerID: name,
		netns:       newPodNetns(t, name),
		ifName:      defaultIfName,
	}

	p.result = n.add(t, p.containerID, p.netns, p.ifName)

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

// hostVeths returns the host side of the veth pairs on the node, which is what
// the plugin leaves behind for every pod it wires up.
func hostVeths(t *testing.T) []netlink.Link {
	t.Helper()

	links, err := netlink.LinkList()
	if err != nil {
		t.Fatalf("listing links on the node: %v", err)
	}

	var veths []netlink.Link
	for _, l := range links {
		if _, ok := l.(*netlink.Veth); ok {
			veths = append(veths, l)
		}
	}

	return veths
}
