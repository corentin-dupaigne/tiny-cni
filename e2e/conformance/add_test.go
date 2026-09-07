package conformance

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"cni-conformance/internal/harness"
)

var _ = Describe("§2 ADD operation", Label("netns"), func() {
	BeforeEach(requireRoot)

	It("2.1 creates the CNI_IFNAME interface inside CNI_NETNS", Label("MUST"), func() {
		a := newAttachment("eth0")
		Expect(a.netns.HasInterface(a.ifname)).To(BeFalse(), "fresh netns unexpectedly already has %s", a.ifname)

		add(a)

		found, err := a.netns.HasInterface(a.ifname)
		Expect(err).NotTo(HaveOccurred())
		if !found {
			names, _ := a.netns.Interfaces()
			Fail(Sprintf("ADD must create %s inside %s (req 2.1); interfaces present: %v", a.ifname, a.netns.Path(), names))
		}
	})

	It("2.2 prints a valid result structure on stdout", Label("MUST", "SHOULD"), func() {
		a := newAttachment("eth0")
		out := add(a)

		res, problems, err := harness.ParseResult(h.CNIVersion, out.Stdout)
		Expect(err).NotTo(HaveOccurred(), "ADD stdout must be a result structure (req 2.2):\n%s", out)
		Expect(problems).To(BeEmpty(), "result schema violations")

		// The requested interface is expected in `interfaces`; plugins that
		// only adjust an existing interface may legitimately omit it → SHOULD.
		found := false
		for _, iface := range res.Interfaces {
			if iface != nil && iface.Name == a.ifname {
				found = true
				if iface.Sandbox != "" {
					Expect(iface.Sandbox).To(Equal(a.netns.Path()), "interface %s reports a sandbox other than CNI_NETNS", a.ifname)
				}
			}
		}
		should(found, "result should list the container interface %q in interfaces; got %v", a.ifname, res.Interfaces)
		AddReportEntry("result", Sprintf("%d interfaces, ips=%v, %d routes", len(res.Interfaces), harness.ResultIPs(res), len(res.Routes)))
	})

	It("2.3 errors when CNI_IFNAME already exists in the container", Label("MUST", "SHOULD"), func() {
		a := newAttachment("eth0")
		add(a)

		out := run(a.inv(harness.CmdAdd))
		Expect(out.ExitCode).NotTo(Equal(0), "second ADD with the same CNI_IFNAME into the same netns must fail (req 2.3):\n%s", out)
		should(out.Error() != nil, "duplicate-ifname failure should print an error structure (req 1.3):\n%s", out)
	})

	// The spec phrases this as what a runtime *may* do; whether a plugin's
	// network can host two attachments in one netns is plugin-dependent
	// (e.g. ptp's per-interface /32 gateway route collides), so SHOULD.
	It("2.4 allows the same container to be added again with a different CNI_IFNAME", Label("SHOULD"), func() {
		a := newAttachment("eth0")
		b := attachment{id: a.id, ifname: "eth1", netns: a.netns}

		add(a)
		out := run(b.inv(harness.CmdAdd))
		DeferCleanup(func() { _, _ = h.Run(b.inv(harness.CmdDel)) })
		should(out.ExitCode == 0, "ADD of the same container with a different CNI_IFNAME should succeed (req 2.4):\n%s", out)
		if out.ExitCode != 0 {
			should(out.Error() != nil, "failed second ADD should print an error structure (req 1.3):\n%s", out)
			return
		}

		for _, x := range []attachment{a, b} {
			Expect(x.netns.HasInterface(x.ifname)).To(BeTrue(), "interface %s missing after both ADDs", x.ifname)
		}
	})

	// CNI_COMMAND itself is not covered: by libcni convention a plugin run
	// with no command prints its "about" line to stderr and exits 0 (every
	// skel-based plugin, bridge included, does this), so there is no
	// operation to return code 4 for.
	Describe("2.5 required environment variables", Label("MUST", "SHOULD"), func() {
		omit := map[string]func(*harness.Invocation){
			harness.EnvContainerID: func(i *harness.Invocation) { i.ContainerID = "" },
			harness.EnvNetNS:       func(i *harness.Invocation) { i.NetNS = "" },
			harness.EnvIfname:      func(i *harness.Invocation) { i.Ifname = "" },
		}
		for _, name := range []string{harness.EnvContainerID, harness.EnvNetNS, harness.EnvIfname} {
			It("returns error code 4 naming "+name+" when it is missing", func() {
				full := newAttachment("eth0")
				inv := full.inv(harness.CmdAdd)
				omit[name](&inv)
				out := run(inv)
				if out.ExitCode == 0 { // wrongly succeeded: undo whatever it did
					DeferCleanup(func() { _, _ = h.Run(full.inv(harness.CmdDel)) })
				}
				expectEnvError(out, name)
			})
		}
	})
})
