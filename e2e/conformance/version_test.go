package conformance

import (
	"github.com/containernetworking/cni/pkg/version"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("§5 VERSION operation", func() {
	// The cniVersion echo is SHOULD: libcni's skel writes the library's own
	// current version instead of the request's, so every skel-based plugin
	// (the reference plugins included) would fail a MUST here.
	It("5.1/5.2 reports cniVersion (echoed) and a non-empty supportedVersions list", Label("MUST", "SHOULD"), func() {
		v := pluginVersions() // asserts exit 0, JSON object, both keys present

		Expect(v.SupportedVersions).NotTo(BeEmpty(), "supportedVersions must be a non-empty list")
		for _, s := range v.SupportedVersions {
			_, _, _, err := version.ParseVersion(s)
			Expect(err).NotTo(HaveOccurred(), "supportedVersions entry %q is not a semantic version", s)
		}
		if h.CNIVersion != "" {
			Expect(v.CNIVersion).NotTo(BeEmpty(), "version result must carry cniVersion")
			should(v.CNIVersion == h.CNIVersion, "cniVersion should echo the request: sent %q, got %q", h.CNIVersion, v.CNIVersion)
			if !v.Supports(h.CNIVersion) {
				GinkgoWriter.Printf("WARNING: config cniVersion %q is not in supportedVersions %v; most other specs are likely to fail\n",
					h.CNIVersion, v.SupportedVersions)
			}
		}
		AddReportEntry("supportedVersions", v.SupportedVersions)
	})
})
