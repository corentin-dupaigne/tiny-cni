package conformance

import (
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"cni-conformance/internal/harness"
)

var _ = Describe("§3 DEL operation", Label("netns"), func() {
	BeforeEach(requireRoot)

	It("3.1 removes the interface ADD created", Label("MUST"), func() {
		a := newAttachment("eth0")
		add(a)
		del(a)
		Expect(a.netns.HasInterface(a.ifname)).To(BeFalse(), "interface %s still present in %s after DEL (req 3.1)", a.ifname, a.netns.Path())
	})

	It("3.2 succeeds when DEL is repeated for the same (containerID, ifname)", Label("MUST"), func() {
		a := newAttachment("eth0")
		add(a)
		del(a)
		out := run(a.inv(harness.CmdDel))
		Expect(out.ExitCode).To(Equal(0), "second DEL must exit 0 even though the interface is already gone (req 3.2):\n%s", out)
	})

	It("3.2 succeeds for an attachment that was never ADDed", Label("MUST"), func() {
		a := newAttachment("eth0")
		out := run(a.inv(harness.CmdDel))
		Expect(out.ExitCode).To(Equal(0), "DEL of an unknown attachment must exit 0 (req 3.2):\n%s", out)
	})

	It("3.3 should succeed when CNI_NETNS no longer exists", Label("SHOULD"), func() {
		a := newAttachment("eth0")
		add(a)
		path := a.netns.Path()
		Expect(a.netns.Close()).To(Succeed(), "remove netns")

		// Pass the now-dangling path exactly as a runtime would after the sandbox died.
		out := run(harness.Invocation{Command: harness.CmdDel, ContainerID: a.id, NetNS: path, Ifname: a.ifname})
		should(out.ExitCode == 0, "DEL should succeed when CNI_NETNS no longer exists (req 3.3):\n%s", out)
	})

	It("3.4 requires no result on stdout on success", Label("MUST"), func() {
		a := newAttachment("eth0")
		add(a)
		out := del(a)

		// Nothing is required; anything printed must not be an error structure.
		if s := strings.TrimSpace(string(out.Stdout)); s != "" {
			e, err := harness.ParseError(out.Stdout)
			Expect(err).To(HaveOccurred(), "DEL exited 0 but printed an error structure: %s", e)
			AddReportEntry("DEL stdout (allowed, informational)", s)
		}
	})

	Describe("3.5 required environment variables", Label("MUST", "SHOULD"), func() {
		It("returns error code 4 naming CNI_CONTAINERID when it is missing", func() {
			ns := newNetNS()
			out := run(harness.Invocation{Command: harness.CmdDel, NetNS: ns.Path(), Ifname: "eth0"})
			expectEnvError(out, harness.EnvContainerID)
		})

		It("returns error code 4 naming CNI_IFNAME when it is missing", func() {
			ns := newNetNS()
			out := run(harness.Invocation{Command: harness.CmdDel, ContainerID: harness.NewContainerID(), NetNS: ns.Path()})
			expectEnvError(out, harness.EnvIfname)
		})

		It("does not fail when CNI_NETNS is omitted (optional for DEL)", func() {
			a := newAttachment("eth0")
			add(a)
			out := run(harness.Invocation{Command: harness.CmdDel, ContainerID: a.id, Ifname: a.ifname})
			Expect(out.ExitCode).To(Equal(0), "DEL without CNI_NETNS must not fail (req 3.5):\n%s", out)
		})
	})
})
