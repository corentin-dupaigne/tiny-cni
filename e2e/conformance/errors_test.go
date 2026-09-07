package conformance

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"cni-conformance/internal/harness"
)

// Only inputs that are invalid for *every* plugin are used here (see
// "Config-error testing boundary" in the requirements document).
var _ = Describe("§6 Error result structure", Label("netns"), func() {
	BeforeEach(requireRoot)

	// Runs ADD with the given stdin and undoes any accidental success.
	addWithStdin := func(stdin []byte) *harness.Output {
		GinkgoHelper()
		a := newAttachment("eth0")
		inv := a.inv(harness.CmdAdd)
		inv.Stdin = stdin
		out := run(inv)
		if out.ExitCode == 0 {
			DeferCleanup(func() { _, _ = h.Run(a.inv(harness.CmdDel)) })
		}
		return out
	}

	It("code 6: rejects syntactically invalid JSON config", Label("MUST", "SHOULD"), func() {
		out := addWithStdin([]byte(`{"cniVersion": "1.0.0", "type": `)) // truncated object
		Expect(out.ExitCode).NotTo(Equal(0), "ADD with malformed JSON on stdin must fail (req 1.2 / code 6):\n%s", out)
		e := out.Error()
		should(e != nil, "malformed-JSON failure should print an error structure (req 1.3):\n%s", out)
		if e != nil {
			should(e.Code == harness.ErrDecodingFailure, "malformed JSON should yield code %d, got %s", harness.ErrDecodingFailure, e)
		}
	})

	It("code 6: rejects a config that is valid JSON but not an object", Label("MUST", "SHOULD"), func() {
		out := addWithStdin([]byte(`[1, 2, 3]`))
		Expect(out.ExitCode).NotTo(Equal(0), "ADD with a JSON array on stdin must fail (req 1.2 / code 6):\n%s", out)
		if e := out.Error(); e != nil {
			should(e.Code == harness.ErrDecodingFailure, "non-object config should yield code %d, got %s", harness.ErrDecodingFailure, e)
		}
	})

	// `type` and `cniVersion` are required of every config by the spec, but a
	// runtime normally guarantees them, and mature plugins built on libcni's
	// skel default a missing cniVersion to 0.1.0 and never read `type`. Per
	// the suite's own correctness rule this is advisory only.
	for _, key := range []string{"type", "cniVersion"} {
		It("rejects a config missing the spec-mandated field "+key, Label("SHOULD"), func() {
			out := addWithStdin(h.ConfigWithout(key))
			should(out.ExitCode != 0, "config without spec-mandated %q was accepted:\n%s", key, out)
		})
	}

	It("rejects an unknown CNI_COMMAND", Label("MUST", "SHOULD"), func() {
		a := newAttachment("eth0")
		inv := a.inv("FROBNICATE")
		out := run(inv)
		if out.ExitCode == 0 {
			DeferCleanup(func() { _, _ = h.Run(a.inv(harness.CmdDel)) })
		}
		Expect(out.ExitCode).NotTo(Equal(0), "CNI_COMMAND=FROBNICATE must fail (req 1.2):\n%s", out)
		should(out.Error() != nil, "unknown-command failure should print an error structure (req 1.3):\n%s", out)
	})

	It("rejects an ADD whose CNI_NETNS does not exist", Label("MUST", "SHOULD"), func() {
		id := harness.NewContainerID()
		bogus := "/var/run/netns/cni-conformance-does-not-exist-" + id
		out := run(harness.Invocation{Command: harness.CmdAdd, ContainerID: id, NetNS: bogus, Ifname: "eth0"})
		DeferCleanup(func() { _, _ = h.Run(harness.Invocation{Command: harness.CmdDel, ContainerID: id, Ifname: "eth0"}) })
		Expect(out.ExitCode).NotTo(Equal(0), "ADD into a nonexistent netns must fail (req 1.2):\n%s", out)
		should(out.Error() != nil, "nonexistent-netns failure should print an error structure (req 1.3):\n%s", out)
	})

	// CNI_ARGS is optional and IgnoreUnknown=1 is the spec's own escape hatch
	// for runtimes passing keys the plugin does not know.
	It("tolerates unknown CNI_ARGS keys when IgnoreUnknown=1", Label("SHOULD"), func() {
		a := newAttachment("eth0")
		inv := a.inv(harness.CmdAdd)
		inv.Args = "IgnoreUnknown=1;CNI_CONFORMANCE_FOO=bar"
		out := run(inv)
		DeferCleanup(func() { _, _ = h.Run(a.inv(harness.CmdDel)) })
		should(out.ExitCode == 0, "ADD with CNI_ARGS=%q should succeed:\n%s", inv.Args, out)
	})

	It("code 1: rejects a cniVersion the plugin does not support", Label("SHOULD"), func() {
		const bogus = "99.0.0"
		if pluginVersions().Supports(bogus) {
			Skip("plugin claims to support " + bogus)
		}
		out := addWithStdin(h.ConfigWithCNIVersion(bogus))
		should(out.ExitCode != 0, "ADD with unsupported cniVersion %s was accepted:\n%s", bogus, out)
		if e := out.Error(); e != nil {
			should(e.Code == harness.ErrIncompatibleVersion, "unsupported cniVersion should yield code %d, got %s", harness.ErrIncompatibleVersion, e)
		}
	})
})
