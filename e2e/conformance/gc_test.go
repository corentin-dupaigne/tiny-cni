package conformance

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"cni-conformance/internal/harness"
)

// GC is spec 1.1.0+ and optional to implement.
var _ = Describe("§9 GC operation", Label("netns", "optional"), func() {
	BeforeEach(func() {
		requireRoot()
		requireProtocol("GC", "1.1.0")
	})

	// With two attachments and a valid-attachments list naming only the
	// first: GC exits 0 (SHOULD, 9.2), the second's interface is gone
	// (SHOULD, best-effort 9.1), and the first survives (MUST).
	It("9.1/9.2 removes stale attachments and keeps valid ones", Label("MUST", "SHOULD"), func() {
		keep := newAttachment("eth0")
		stale := newAttachment("eth0")
		add(keep)
		add(stale)

		out := run(harness.Invocation{
			Command: harness.CmdGC,
			Stdin:   h.ConfigWithValidAttachments([]harness.Attachment{{ContainerID: keep.id, Ifname: keep.ifname}}),
		})
		if out.ExitCode != 0 && looksUnsupported(out) {
			Skip(Sprintf("plugin declines GC:\n%s", out))
		}
		should(out.ExitCode == 0, "GC should exit 0 on the happy path (req 9.2):\n%s", out)
		if out.ExitCode != 0 {
			return
		}

		Expect(keep.netns.HasInterface(keep.ifname)).To(BeTrue(), "GC removed interface %s of a *valid* attachment %s (req 9.1)", keep.ifname, keep.id)
		gone, err := stale.netns.HasInterface(stale.ifname)
		Expect(err).NotTo(HaveOccurred())
		should(!gone, "GC should remove resources of attachment %s not in cni.dev/valid-attachments (req 9.1); interface %s still present", stale.id, stale.ifname)

		// GC must not break a subsequent DEL of either attachment.
		for _, a := range []attachment{keep, stale} {
			o := run(a.inv(harness.CmdDel))
			Expect(o.ExitCode).To(Equal(0), "DEL after GC failed for %s:\n%s", a.id, o)
		}
	})
})
