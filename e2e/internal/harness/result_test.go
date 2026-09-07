package harness

import (
	"testing"
)

func TestParseError(t *testing.T) {
	e, err := ParseError([]byte(`{"cniVersion":"1.0.0","code":4,"msg":"required env variables [CNI_NETNS] missing","details":""}`))
	if err != nil {
		t.Fatal(err)
	}
	if e.Code != ErrInvalidEnv || !e.Mentions("cni_netns") {
		t.Errorf("unexpected error parse: %s", e)
	}
	for _, bad := range []string{``, `not json`, `[]`, `{"msg":"x"}`, `{"code":"4","msg":"x"}`} {
		if _, err := ParseError([]byte(bad)); err == nil {
			t.Errorf("ParseError(%q) should fail", bad)
		}
	}
}

func TestParseVersionResult(t *testing.T) {
	v, err := ParseVersionResult([]byte(`{"cniVersion":"1.0.0","supportedVersions":["0.4.0","1.0.0","1.1.0"]}`))
	if err != nil {
		t.Fatal(err)
	}
	if !v.Supports("1.0.0") || v.Supports("2.0.0") {
		t.Error("Supports is wrong")
	}
	if !v.SupportsAtLeast("1.1.0") || v.SupportsAtLeast("1.2.0") {
		t.Error("SupportsAtLeast is wrong")
	}
	if _, err := ParseVersionResult([]byte(`{"cniVersion":"1.0.0"}`)); err == nil {
		t.Error("missing supportedVersions should fail")
	}
}

func TestParseResult(t *testing.T) {
	good := `{"cniVersion":"1.0.0","interfaces":[{"name":"eth0","sandbox":"/run/netns/x"}],
	  "ips":[{"address":"10.0.0.2/24","interface":0}],"routes":[{"dst":"0.0.0.0/0"}]}`
	r, problems, err := ParseResult("1.0.0", []byte(good))
	if err != nil {
		t.Fatal(err)
	}
	if len(problems) != 0 {
		t.Errorf("unexpected problems: %v", problems)
	}
	if ips := ResultIPs(r); len(ips) != 1 || ips[0] != "10.0.0.2" {
		t.Errorf("ResultIPs = %v", ips)
	}

	// Out-of-range interface index is flagged.
	badIdx := `{"cniVersion":"1.0.0","interfaces":[{"name":"eth0"}],"ips":[{"address":"10.0.0.2/24","interface":3}]}`
	_, problems, err = ParseResult("1.0.0", []byte(badIdx))
	if err != nil {
		t.Fatal(err)
	}
	if len(problems) != 1 {
		t.Errorf("expected 1 problem, got %v", problems)
	}
	// A result printed with a different cniVersion than the config is rejected.
	if _, _, err := ParseResult("1.0.0", []byte(`{"cniVersion":"0.4.0","interfaces":[]}`)); err == nil {
		t.Error("cniVersion mismatch should fail")
	}

	// An error structure is not a result.
	if _, _, err := ParseResult("1.0.0", []byte(`{"code":4,"msg":"x"}`)); err == nil {
		t.Error("error structure accepted as result")
	}
	// Legacy 0.2.0 results decode too.
	if _, _, err := ParseResult("0.2.0", []byte(`{"cniVersion":"0.2.0","ip4":{"ip":"10.0.0.2/24"}}`)); err != nil {
		t.Errorf("0.2.0 result: %v", err)
	}
}

func TestConfigEditing(t *testing.T) {
	h := &Harness{Config: []byte(`{"cniVersion":"1.0.0","type":"x","opaque":{"a":1}}`)}
	if got := string(h.ConfigWithout("type")); got != `{"cniVersion":"1.0.0","opaque":{"a":1}}` {
		t.Errorf("ConfigWithout = %s", got)
	}
	if got := string(h.ConfigWithCNIVersion("99.0.0")); got != `{"cniVersion":"99.0.0","opaque":{"a":1},"type":"x"}` {
		t.Errorf("ConfigWithCNIVersion = %s", got)
	}
	got := string(h.ConfigWithValidAttachments([]Attachment{{ContainerID: "c", Ifname: "eth0"}}))
	want := `{"cni.dev/valid-attachments":[{"containerID":"c","ifname":"eth0"}],"cniVersion":"1.0.0","opaque":{"a":1},"type":"x"}`
	if got != want {
		t.Errorf("ConfigWithValidAttachments = %s", got)
	}
}
