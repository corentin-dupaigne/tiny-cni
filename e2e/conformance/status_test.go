package conformance

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"cni-conformance/internal/harness"
)

// STATUS is spec 1.1.0+ and optional to implement.
var _ = Describe("§8 STATUS operation", Label("optional"), func() {
	It("8.1 exits 0 on a healthy plugin", Label("MUST"), func() {
		requireProtocol("STATUS", "1.1.0")
		out := run(harness.Invocation{Command: harness.CmdStatus})
		if out.ExitCode != 0 && looksUnsupported(out) {
			Skip(Sprintf("plugin declines STATUS:\n%s", out))
		}
		Expect(out.ExitCode).To(Equal(0), "STATUS on a healthy plugin must exit 0 (req 8.1):\n%s", out)
	})
})
