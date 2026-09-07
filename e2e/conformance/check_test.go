package conformance

import (
	. "github.com/onsi/ginkgo/v2"

	"cni-conformance/internal/harness"
)

// Only 4.8 (required-env validation) is neutrally assertable; the rest of
// CHECK needs a prevResult or a plugin chain.
var _ = Describe("§4 CHECK operation", Label("netns", "optional"), func() {
	BeforeEach(func() {
		requireRoot()
		requireProtocol("CHECK", "0.4.0")
	})

	Describe("4.8 required environment variables", Label("MUST", "SHOULD"), func() {
		var a attachment

		BeforeEach(func() {
			// Give the plugin real state so a well-formed CHECK has something to check.
			a = newAttachment("eth0")
			add(a)
			probe := run(a.inv(harness.CmdCheck))
			if probe.ExitCode != 0 && looksUnsupported(probe) {
				Skip(Sprintf("plugin declines CHECK:\n%s", probe))
			}
			if probe.ExitCode != 0 {
				// Not a conformance failure by itself (CHECK semantics depend
				// on prevResult, out of scope), but worth surfacing.
				AddReportEntry("note: well-formed CHECK after ADD exited non-zero (not asserted, §4 scope note)", probe.String())
			}
		})

		omit := map[string]func(*harness.Invocation){
			harness.EnvContainerID: func(i *harness.Invocation) { i.ContainerID = "" },
			harness.EnvNetNS:       func(i *harness.Invocation) { i.NetNS = "" },
			harness.EnvIfname:      func(i *harness.Invocation) { i.Ifname = "" },
		}
		for _, name := range []string{harness.EnvContainerID, harness.EnvNetNS, harness.EnvIfname} {
			It("returns error code 4 naming "+name+" when it is missing", func() {
				inv := a.inv(harness.CmdCheck)
				omit[name](&inv)
				expectEnvError(run(inv), name)
			})
		}
	})
})
