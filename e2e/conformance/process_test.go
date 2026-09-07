package conformance

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"cni-conformance/internal/harness"
)

var _ = Describe("§1 Process contract", func() {
	// VERSION is the cheapest success path and needs no namespace; ADD/DEL
	// success is asserted by every §2/§3 spec.
	It("1.1 exits 0 on success", Label("MUST"), func() {
		out := run(harness.Invocation{Command: harness.CmdVersion, Stdin: versionRequest(), OmitPath: true})
		Expect(out.ExitCode).To(Equal(0), "VERSION must exit 0 on success:\n%s", out)
	})

	// An ADD with no parameters at all is a failure for every plugin.
	It("1.2 exits non-zero on failure and 1.3 prints an error structure", Label("MUST", "SHOULD"), func() {
		out := run(harness.Invocation{Command: harness.CmdAdd})
		Expect(out.ExitCode).NotTo(Equal(0), "ADD without CNI_CONTAINERID/CNI_NETNS/CNI_IFNAME must fail (req 1.2):\n%s", out)

		e, err := harness.ParseError(out.Stdout)
		should(err == nil, "failure should print an error structure on stdout (req 1.3): %v\n%s", err, out)
		// cniVersion is deliberately not asserted: env validation happens
		// before the config (and thus the version) is read, so even libcni's
		// skel prints it empty here.
		if e != nil {
			should(e.Msg != "", "error structure should carry a non-empty msg (§6): %s", e)
		}
	})
})
