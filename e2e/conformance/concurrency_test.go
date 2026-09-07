package conformance

import (
	"sync"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"cni-conformance/internal/harness"
)

var _ = Describe("§7 Concurrency", Label("netns"), func() {
	BeforeEach(requireRoot)

	It("7.1 handles N concurrent ADDs for distinct containers without duplicate IPs", Label("MUST"), func() {
		atts := make([]attachment, concurrency)
		for i := range atts {
			atts[i] = newAttachment("eth0")
			a := atts[i]
			DeferCleanup(func() { _, _ = h.Run(a.inv(harness.CmdDel)) })
		}

		type result struct {
			out *harness.Output
			err error
		}
		results := make([]result, len(atts))
		var wg sync.WaitGroup
		start := make(chan struct{})
		for i, a := range atts {
			wg.Add(1)
			go func(i int, a attachment) {
				defer wg.Done()
				defer GinkgoRecover()
				<-start // release all goroutines together
				out, err := h.Run(a.inv(harness.CmdAdd))
				results[i] = result{out, err}
			}(i, a)
		}
		close(start)
		wg.Wait()

		owners := map[string][]string{} // ip -> container IDs
		for i, r := range results {
			Expect(r.err).NotTo(HaveOccurred(), "container %d: %v\n%s", i, r.err, r.out)
			Expect(r.out.ExitCode).To(Equal(0), "container %d: concurrent ADD failed (req 7.1):\n%s", i, r.out)
			res, _, err := harness.ParseResult(h.CNIVersion, r.out.Stdout)
			Expect(err).NotTo(HaveOccurred(), "container %d: %v\n%s", i, err, r.out)
			for _, ip := range harness.ResultIPs(res) {
				owners[ip] = append(owners[ip], atts[i].id)
			}
			Expect(atts[i].netns.HasInterface(atts[i].ifname)).To(BeTrue(), "container %d: interface %s missing after concurrent ADD", i, atts[i].ifname)
		}
		for ip, ids := range owners {
			Expect(ids).To(HaveLen(1), "IP %s was allocated to %d containers concurrently (req 7.1): %v", ip, len(ids), ids)
		}
		if len(owners) == 0 {
			AddReportEntry("note", "plugin returned no IPs; only success and interface presence were asserted")
		} else {
			AddReportEntry("allocation", Sprintf("%d distinct IPs across %d containers", len(owners), len(atts)))
		}

		// Sequential DELs must all succeed too, and free the addresses.
		for i, a := range atts {
			out := run(a.inv(harness.CmdDel))
			Expect(out.ExitCode).To(Equal(0), "container %d: DEL failed:\n%s", i, out)
		}
	})

	It("7.1 handles N concurrent DELs for distinct containers", Label("MUST"), func() {
		atts := make([]attachment, concurrency)
		for i := range atts {
			atts[i] = newAttachment("eth0")
			add(atts[i])
		}
		outs := parallel(atts, func(a attachment) *harness.Output {
			out, err := h.Run(a.inv(harness.CmdDel))
			Expect(err).NotTo(HaveOccurred())
			return out
		})
		for i, out := range outs {
			Expect(out.ExitCode).To(Equal(0), "container %d: concurrent DEL failed (req 7.1):\n%s", i, out)
			Expect(atts[i].netns.HasInterface(atts[i].ifname)).To(BeFalse(), "container %d: interface still present after concurrent DEL", i)
		}
	})

	It("7.1 handles concurrent ADDs and DELs across distinct containers", Label("MUST"), func() {
		// Half the containers are being torn down while the other half come up.
		n := concurrency / 2
		leaving := make([]attachment, n)
		arriving := make([]attachment, n)
		for i := range n {
			leaving[i] = newAttachment("eth0")
			add(leaving[i])
			arriving[i] = newAttachment("eth0")
			a := arriving[i]
			DeferCleanup(func() { _, _ = h.Run(a.inv(harness.CmdDel)) })
		}
		all := append(append([]attachment{}, leaving...), arriving...)
		outs := parallel(all, func(a attachment) *harness.Output {
			cmd := harness.CmdAdd
			for _, l := range leaving {
				if l.id == a.id {
					cmd = harness.CmdDel
				}
			}
			out, err := h.Run(a.inv(cmd))
			Expect(err).NotTo(HaveOccurred())
			return out
		})
		owners := map[string][]string{}
		for i, out := range outs {
			Expect(out.ExitCode).To(Equal(0), "attachment %d: concurrent op failed (req 7.1):\n%s", i, out)
			if i >= n { // an ADD: collect its IPs
				res, _, err := harness.ParseResult(h.CNIVersion, out.Stdout)
				Expect(err).NotTo(HaveOccurred(), "attachment %d:\n%s", i, out)
				for _, ip := range harness.ResultIPs(res) {
					owners[ip] = append(owners[ip], all[i].id)
				}
			}
		}
		for ip, ids := range owners {
			Expect(ids).To(HaveLen(1), "IP %s allocated to %d containers concurrently: %v", ip, len(ids), ids)
		}
	})
})

// parallel runs fn for every attachment at once and returns the outputs in
// order. Assertions inside fn are safe: each goroutine recovers for Ginkgo.
func parallel(atts []attachment, fn func(attachment) *harness.Output) []*harness.Output {
	outs := make([]*harness.Output, len(atts))
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i, a := range atts {
		wg.Add(1)
		go func(i int, a attachment) {
			defer wg.Done()
			defer GinkgoRecover()
			<-start
			outs[i] = fn(a)
		}(i, a)
	}
	close(start)
	wg.Wait()
	return outs
}
