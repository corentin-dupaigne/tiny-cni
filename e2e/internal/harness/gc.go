package harness

import "encoding/json"

// Attachment identifies one (containerID, ifname) pair for GC requests.
type Attachment struct {
	ContainerID string `json:"containerID"`
	Ifname      string `json:"ifname"`
}

// ConfigWithValidAttachments returns a copy of the config with the
// spec-defined `cni.dev/valid-attachments` key set, as a runtime would inject
// it for a GC request. No other field is touched.
func (h *Harness) ConfigWithValidAttachments(valid []Attachment) []byte {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(h.Config, &m); err != nil {
		return h.Config
	}
	if valid == nil {
		valid = []Attachment{}
	}
	m["cni.dev/valid-attachments"], _ = json.Marshal(valid)
	out, _ := json.Marshal(m)
	return out
}
