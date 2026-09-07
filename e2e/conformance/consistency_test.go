package conformance

import (
	"strings"

	current "github.com/containernetworking/cni/pkg/types/100"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"cni-conformance/internal/harness"
)

// The result a plugin prints is a set of claims about what it configured.
// These specs check the claims against the namespace, which is neutral: only
// the plugin's own statements are asserted.
var _ = Describe("§2 ADD result vs. namespace state", Label("netns"), func() {
	BeforeEach(requireRoot)

	// containerIfaceFor resolves which interface an IP entry belongs to: the
	// indexed interface when it is sandboxed in our netns, CNI_IFNAME when the
	// (pre-0.3.0) result carries no index, nil when it is a host interface.
	containerIfaceFor := func(res *current.Result, ip *current.IPConfig, a attachment) *current.Interface {
		if ip.Interface == nil {
			return &current.Interface{Name: a.ifname, Sandbox: a.netns.Path()}
		}
		if *ip.Interface < 0 || *ip.Interface >= len(res.Interfaces) || res.Interfaces[*ip.Interface] == nil {
			return nil // schema violation, reported by the 2.2 spec
		}
		iface := res.Interfaces[*ip.Interface]
		if iface.Sandbox != "" && iface.Sandbox != a.netns.Path() {
			return nil
		}
		if iface.Sandbox == "" {
			return nil // host-side interface
		}
		return iface
	}

	It("2.1/2.2 applies the addresses, MAC and routes it reports", Label("MUST", "SHOULD"), func() {
		a := newAttachment("eth0")
		out := add(a)
		res, _, err := harness.ParseResult(h.CNIVersion, out.Stdout)
		Expect(err).NotTo(HaveOccurred(), "ADD stdout:\n%s", out)

		// Every sandboxed interface in the result exists, with the MAC claimed.
		for _, iface := range res.Interfaces {
			if iface == nil || iface.Sandbox != a.netns.Path() {
				continue
			}
			st, err := a.netns.Link(iface.Name)
			Expect(err).NotTo(HaveOccurred())
			Expect(st).NotTo(BeNil(), "result lists sandbox interface %q but it is absent from %s", iface.Name, a.netns.Path())
			if iface.Mac != "" {
				Expect(strings.EqualFold(st.MAC, iface.Mac)).To(BeTrue(), "interface %s: result mac %q, actual %q", iface.Name, iface.Mac, st.MAC)
			}
			if iface.Mtu != 0 {
				should(st.MTU == iface.Mtu, "interface %s: result mtu %d, actual %d", iface.Name, iface.Mtu, st.MTU)
			}
			should(st.Up, "interface %s should be UP after ADD", iface.Name)
		}

		// Every IP attributed to a container interface is assigned there.
		checked := 0
		for i, ip := range res.IPs {
			iface := containerIfaceFor(res, ip, a)
			if iface == nil {
				continue
			}
			st, err := a.netns.Link(iface.Name)
			Expect(err).NotTo(HaveOccurred())
			Expect(st).NotTo(BeNil(), "ips[%d] points at %q which does not exist in the netns", i, iface.Name)
			Expect(st.HasAddr(ip.Address.String())).To(BeTrue(),
				"ips[%d]: result says %s is on %s, but that interface carries %v", i, ip.Address.String(), iface.Name, st.Addrs)
			checked++
		}

		// Every route the plugin reports exists in the netns.
		routes, err := a.netns.Routes()
		Expect(err).NotTo(HaveOccurred())
		for i, rt := range res.Routes {
			if rt == nil {
				continue
			}
			found := false
			for _, r := range routes {
				if r.Dst == rt.Dst.String() {
					found = true
					if rt.GW != nil {
						should(r.Gw == rt.GW.String(), "routes[%d] %s: result gw %s, actual %q", i, r.Dst, rt.GW, r.Gw)
					}
				}
			}
			Expect(found).To(BeTrue(), "routes[%d]: result reports %s but it is not in the netns routing table %v", i, rt.Dst.String(), routes)
		}
		AddReportEntry("verified", Sprintf("%d addresses, %d routes", checked, len(res.Routes)))
		if checked == 0 && len(res.IPs) > 0 {
			AddReportEntry("note", "result IPs are attributed to host interfaces only; nothing to verify in the netns")
		}
	})

	It("2.1 creates the interface under exactly the requested name", Label("MUST"), func() {
		for _, name := range []string{"net1", "cni-if_9", "abcdefghijklmno"} { // last one is IFNAMSIZ-1
			a := newAttachment(name)
			add(a)
			st, err := a.netns.Link(name)
			Expect(err).NotTo(HaveOccurred())
			Expect(st).NotTo(BeNil(), "ADD with CNI_IFNAME=%q did not create an interface of that exact name; present: %v",
				name, func() []string { n, _ := a.netns.Interfaces(); return n }())
		}
	})

	It("3.1 releases state so ADD after DEL on the same (containerID, ifname) succeeds", Label("MUST"), func() {
		a := newAttachment("eth0")
		add(a)
		del(a)
		out := run(a.inv(harness.CmdAdd))
		Expect(out.ExitCode).To(Equal(0), "ADD after a DEL of the same attachment must succeed (req 3.1):\n%s", out)
		Expect(a.netns.HasInterface(a.ifname)).To(BeTrue(), "interface %s missing after re-ADD", a.ifname)
	})
})

// A plugin's supportedVersions is a promise: each listed version must be
// usable end to end, with a result in that version's schema.
var _ = Describe("§5 supportedVersions are honoured", Label("netns"), func() {
	BeforeEach(requireRoot)

	It("5.2 serves ADD/DEL for every version it advertises", Label("MUST"), func() {
		v := pluginVersions()
		for _, ver := range v.SupportedVersions {
			By("cniVersion " + ver)
			a := newAttachment("eth0")
			inv := a.inv(harness.CmdAdd)
			inv.Stdin = h.ConfigWithCNIVersion(ver)
			out := run(inv)
			delInv := a.inv(harness.CmdDel)
			delInv.Stdin = inv.Stdin
			DeferCleanup(func() { _, _ = h.Run(delInv) })
			Expect(out.ExitCode).To(Equal(0), "plugin advertises %s but ADD with that cniVersion failed:\n%s", ver, out)

			_, problems, err := harness.ParseResult(ver, out.Stdout)
			Expect(err).NotTo(HaveOccurred(), "cniVersion %s: result does not match that version's schema:\n%s", ver, out)
			Expect(problems).To(BeEmpty(), "cniVersion %s: result schema violations", ver)
			Expect(a.netns.HasInterface(a.ifname)).To(BeTrue(), "cniVersion %s: interface %s missing after ADD", ver, a.ifname)

			d := run(delInv)
			Expect(d.ExitCode).To(Equal(0), "cniVersion %s: DEL failed:\n%s", ver, d)
		}
	})
})
