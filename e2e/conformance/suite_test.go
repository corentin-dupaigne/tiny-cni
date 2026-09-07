// Package conformance is the CNI plugin conformance suite, written with
// Ginkgo v2. Each spec file maps to a section of "CNI Conformance Test Suite —
// Requirements & Coverage.md"; spec text carries the requirement number.
//
// Configuration is taken from the environment so the suite runs with a plain
// `go test` or the `ginkgo` CLI:
//
//	CNI_PLUGIN           path to the plugin binary under test (required)
//	CNI_CONFIG           path to the network config JSON forwarded verbatim (required)
//	CNI_PATH             value of CNI_PATH handed to the plugin (default: dir of CNI_PLUGIN)
//	CNI_TEST_STRICT      "1" to turn SHOULD-level violations into failures
//	CNI_TEST_TIMEOUT     per-invocation timeout, Go duration (default 30s)
//	CNI_TEST_CONCURRENCY number of parallel ADDs for the concurrency test (default 8)
//
// Labels: MUST, SHOULD (advisory), optional (skips when the plugin does not
// implement the operation), netns (needs root). Filter with
// `--ginkgo.label-filter='MUST && !netns'`.
package conformance

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/containernetworking/cni/pkg/version"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"cni-conformance/internal/harness"
)

var (
	h           *harness.Harness
	strict      bool
	concurrency = 8

	shouldMu         sync.Mutex
	shouldViolations []string // suite-wide, for the final summary
	specShould       []string // current spec, for strict mode
)

func TestConformance(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "CNI Conformance Suite")
}

var _ = BeforeSuite(func() {
	plugin := os.Getenv("CNI_PLUGIN")
	config := os.Getenv("CNI_CONFIG")
	if plugin == "" || config == "" {
		Skip("CNI_PLUGIN and CNI_CONFIG are not set")
	}
	timeout := 30 * time.Second
	if v := os.Getenv("CNI_TEST_TIMEOUT"); v != "" {
		d, err := time.ParseDuration(v)
		Expect(err).NotTo(HaveOccurred(), "bad CNI_TEST_TIMEOUT %q", v)
		timeout = d
	}
	strict = os.Getenv("CNI_TEST_STRICT") == "1"
	if v := os.Getenv("CNI_TEST_CONCURRENCY"); v != "" {
		n, err := strconv.Atoi(v)
		Expect(err).NotTo(HaveOccurred(), "bad CNI_TEST_CONCURRENCY %q", v)
		Expect(n).To(BeNumerically(">=", 2), "CNI_TEST_CONCURRENCY must be >= 2")
		concurrency = n
	}

	var err error
	h, err = harness.New(plugin, config, os.Getenv("CNI_PATH"), timeout)
	Expect(err).NotTo(HaveOccurred())

	GinkgoWriter.Printf("plugin=%s config=%s cniVersion=%q CNI_PATH=%s root=%v strict=%v\n",
		h.PluginPath, config, h.CNIVersion, h.CNIPath, harness.IsRoot(), strict)
	if !harness.IsRoot() {
		GinkgoWriter.Println("not running as root: specs labelled 'netns' will be skipped")
	}
	preflight()
})

// preflight aborts the suite when the binary does not speak CNI at all (e.g.
// a management CLI passed by mistake), so the run shows one clear message
// instead of every spec failing the same way.
func preflight() {
	out, err := h.Run(harness.Invocation{Command: harness.CmdVersion, Stdin: versionRequest(), OmitPath: true})
	Expect(err).NotTo(HaveOccurred(), "preflight VERSION: %s", out)
	if out.ExitCode == 0 {
		if _, perr := harness.ParseVersionResult(out.Stdout); perr == nil {
			return
		}
	} else if _, perr := harness.ParseError(out.Stdout); perr == nil {
		return
	}
	Fail(fmt.Sprintf("preflight: %s does not answer CNI_COMMAND=VERSION with a version result or a CNI error "+
		"structure; it does not look like a CNI plugin binary (for Calico use the `calico` plugin, not calicoctl).\n%s",
		h.PluginPath, out))
}

// Per-spec SHOULD bookkeeping: reset before, enforce after in strict mode.
var _ = BeforeEach(func() {
	shouldMu.Lock()
	specShould = nil
	shouldMu.Unlock()
})

var _ = AfterEach(func() {
	shouldMu.Lock()
	v := specShould
	shouldMu.Unlock()
	if strict && len(v) > 0 {
		Fail("SHOULD violation(s) in strict mode:\n  - " + strings.Join(v, "\n  - "))
	}
})

var _ = ReportAfterSuite("SHOULD summary", func(Report) {
	shouldMu.Lock()
	defer shouldMu.Unlock()
	if len(shouldViolations) == 0 {
		return
	}
	mode := " (advisory; set CNI_TEST_STRICT=1 to fail on them)"
	if strict {
		mode = " (strict mode: counted as failures)"
	}
	fmt.Fprintf(GinkgoWriter, "\n%d SHOULD-level violation(s)%s:\n", len(shouldViolations), mode)
	fmt.Fprintf(os.Stderr, "\n%d SHOULD-level violation(s)%s:\n", len(shouldViolations), mode)
	for _, v := range shouldViolations {
		fmt.Fprintf(os.Stderr, "  - %s\n", firstLine(v))
	}
})

// Sprintf shortens Skip/Fail/report messages in the specs.
var Sprintf = fmt.Sprintf

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i] + " …"
	}
	return s
}

// requireProtocol skips the spec unless both the config's cniVersion (the
// protocol version in use — what gates an operation) and the plugin's
// supportedVersions reach min. A config without cniVersion is 0.1.0.
func requireProtocol(op, min string) {
	GinkgoHelper()
	cfg := h.CNIVersion
	if cfg == "" {
		cfg = "0.1.0"
	}
	if ok, err := version.GreaterThanOrEqualTo(cfg, min); err != nil || !ok {
		Skip(Sprintf("config cniVersion is %s; %s requires %s", cfg, op, min))
	}
	if v := pluginVersions(); !v.SupportsAtLeast(min) {
		Skip(Sprintf("plugin supports %v; %s requires %s", v.SupportedVersions, op, min))
	}
}

// requireRoot skips the current spec when namespaces cannot be created.
func requireRoot() {
	GinkgoHelper()
	if !harness.IsRoot() {
		Skip("requires root to create network namespaces")
	}
}

// should records a SHOULD-level violation. The spec keeps running; in strict
// mode AfterEach turns the violation into a failure.
func should(cond bool, format string, args ...any) {
	GinkgoHelper()
	if cond {
		return
	}
	msg := fmt.Sprintf(format, args...)
	shouldMu.Lock()
	specShould = append(specShould, msg)
	shouldViolations = append(shouldViolations, CurrentSpecReport().FullText()+": "+msg)
	shouldMu.Unlock()
	AddReportEntry("SHOULD violation (advisory)", msg)
	GinkgoWriter.Printf("SHOULD violation (advisory): %s\n", msg)
}

// run invokes the plugin; harness-level errors (timeout, cannot exec) fail
// the spec, a non-zero exit is returned to the caller for assertion.
func run(inv harness.Invocation) *harness.Output {
	GinkgoHelper()
	out, err := h.Run(inv)
	Expect(err).NotTo(HaveOccurred(), "%s: %v\n%s", inv.Command, err, out)
	return out
}

// newNetNS creates a namespace that is removed when the spec ends.
func newNetNS() *harness.NetNS {
	GinkgoHelper()
	n, err := harness.NewNetNS()
	Expect(err).NotTo(HaveOccurred(), "create netns")
	DeferCleanup(func() { _ = n.Close() })
	return n
}

// attachment is one (containerID, netns, ifname) triple the suite has ADDed.
type attachment struct {
	id     string
	ifname string
	netns  *harness.NetNS
}

func newAttachment(ifname string) attachment {
	GinkgoHelper()
	return attachment{id: harness.NewContainerID(), ifname: ifname, netns: newNetNS()}
}

func (a attachment) inv(cmd string) harness.Invocation {
	return harness.Invocation{Command: cmd, ContainerID: a.id, NetNS: a.netns.Path(), Ifname: a.ifname}
}

// add performs a successful ADD and registers a best-effort DEL for cleanup.
func add(a attachment) *harness.Output {
	GinkgoHelper()
	out := run(a.inv(harness.CmdAdd))
	DeferCleanup(func() { _, _ = h.Run(a.inv(harness.CmdDel)) })
	Expect(out.ExitCode).To(Equal(0), "ADD must succeed (req 1.1); it failed:\n%s", out)
	return out
}

// del performs a DEL and fails the spec if it does not exit 0.
func del(a attachment) *harness.Output {
	GinkgoHelper()
	out := run(a.inv(harness.CmdDel))
	Expect(out.ExitCode).To(Equal(0), "DEL must succeed (req 1.1); it failed:\n%s", out)
	return out
}

// pluginVersions runs VERSION and returns the parsed result.
func pluginVersions() *harness.VersionResult {
	GinkgoHelper()
	out := run(harness.Invocation{Command: harness.CmdVersion, Stdin: versionRequest(), OmitPath: true})
	Expect(out.ExitCode).To(Equal(0), "VERSION failed:\n%s", out)
	v, err := harness.ParseVersionResult(out.Stdout)
	Expect(err).NotTo(HaveOccurred(), "VERSION output:\n%s", out)
	return v
}

func versionRequest() []byte {
	v := h.CNIVersion
	if v == "" {
		v = "1.1.0"
	}
	return []byte(`{"cniVersion":"` + v + `"}`)
}

// expectEnvError asserts the code-4 contract: non-zero exit (MUST); error
// structure present (SHOULD); when present, code 4 naming the variable (MUST).
func expectEnvError(out *harness.Output, missing string) {
	GinkgoHelper()
	Expect(out.ExitCode).NotTo(Equal(0), "plugin must fail when %s is missing (req 1.2); it exited 0:\n%s", missing, out)
	e := out.Error()
	should(e != nil, "no error structure on stdout when %s is missing (req 1.3):\n%s", missing, out)
	if e == nil {
		return
	}
	Expect(e.Code).To(Equal(uint(harness.ErrInvalidEnv)), "missing %s: error code must be %d, got %s\n%s", missing, harness.ErrInvalidEnv, e, out)
	Expect(e.Mentions(missing)).To(BeTrue(), "missing %s: error msg/details must name the variable, got %s", missing, e)
}

// looksUnsupported guesses whether a failed invocation means "this plugin does
// not implement that command", so optional operations can be skipped.
func looksUnsupported(out *harness.Output) bool {
	text := strings.ToLower(string(out.Stdout) + " " + string(out.Stderr))
	for _, s := range []string{"unknown cni_command", "unknown command", "unsupported command",
		"not supported", "not implemented", "unrecognized command", "invalid cni_command"} {
		if strings.Contains(text, s) {
			return true
		}
	}
	return false
}
