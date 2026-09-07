package harness

import (
	"encoding/json"
	"fmt"
	"strings"

	current "github.com/containernetworking/cni/pkg/types/100"
	"github.com/containernetworking/cni/pkg/version"
)

// CNIError mirrors the spec's error result structure.
type CNIError struct {
	CNIVersion string `json:"cniVersion"`
	Code       uint   `json:"code"`
	Msg        string `json:"msg"`
	Details    string `json:"details"`
}

// Mentions reports whether the error text names s (case-insensitive) in either
// msg or details.
func (e *CNIError) Mentions(s string) bool {
	s = strings.ToLower(s)
	return strings.Contains(strings.ToLower(e.Msg), s) ||
		strings.Contains(strings.ToLower(e.Details), s)
}

func (e *CNIError) String() string {
	return fmt.Sprintf("code=%d msg=%q details=%q cniVersion=%q", e.Code, e.Msg, e.Details, e.CNIVersion)
}

// ParseError decodes a CNI error structure. It is strict about shape: the
// payload must be a JSON object with a numeric "code" and a string "msg".
func ParseError(stdout []byte) (*CNIError, error) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(stdout, &raw); err != nil {
		return nil, fmt.Errorf("stdout is not a JSON object: %w", err)
	}
	if _, ok := raw["code"]; !ok {
		return nil, fmt.Errorf("error structure has no \"code\" field")
	}
	if _, ok := raw["msg"]; !ok {
		return nil, fmt.Errorf("error structure has no \"msg\" field")
	}
	var e CNIError
	if err := json.Unmarshal(stdout, &e); err != nil {
		return nil, fmt.Errorf("error structure has wrong field types: %w", err)
	}
	return &e, nil
}

// VersionResult mirrors the output of the VERSION operation.
type VersionResult struct {
	CNIVersion        string   `json:"cniVersion"`
	SupportedVersions []string `json:"supportedVersions"`
}

// Supports reports whether v is in the supported list.
func (v *VersionResult) Supports(ver string) bool {
	for _, s := range v.SupportedVersions {
		if s == ver {
			return true
		}
	}
	return false
}

// SupportsAtLeast reports whether any supported version is >= min.
func (v *VersionResult) SupportsAtLeast(min string) bool {
	for _, s := range v.SupportedVersions {
		if ok, err := version.GreaterThanOrEqualTo(s, min); err == nil && ok {
			return true
		}
	}
	return false
}

// ParseVersionResult decodes the VERSION output and checks field presence.
func ParseVersionResult(stdout []byte) (*VersionResult, error) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(stdout, &raw); err != nil {
		return nil, fmt.Errorf("stdout is not a JSON object: %w", err)
	}
	if _, ok := raw["cniVersion"]; !ok {
		return nil, fmt.Errorf("version result has no \"cniVersion\" field")
	}
	if _, ok := raw["supportedVersions"]; !ok {
		return nil, fmt.Errorf("version result has no \"supportedVersions\" field")
	}
	var v VersionResult
	if err := json.Unmarshal(stdout, &v); err != nil {
		return nil, fmt.Errorf("version result has wrong field types: %w", err)
	}
	return &v, nil
}

// ParseResult decodes an ADD result for the given cniVersion and returns it
// normalised to the current (1.x) result shape. It also performs the
// spec-level schema checks that are independent of the plugin.
func ParseResult(cniVersion string, stdout []byte) (*current.Result, []string, error) {
	if !json.Valid(stdout) {
		return nil, nil, fmt.Errorf("stdout is not valid JSON")
	}
	// Reject error structures masquerading as results.
	if e, err := ParseError(stdout); err == nil {
		return nil, nil, fmt.Errorf("stdout is an error structure, not a result: %s", e)
	}
	if cniVersion == "" {
		cniVersion = version.Current()
	}
	r, err := version.NewResult(cniVersion, stdout)
	if err != nil {
		return nil, nil, fmt.Errorf("result does not decode as a cniVersion %s result: %w", cniVersion, err)
	}
	res, err := current.GetResult(r)
	if err != nil {
		return nil, nil, fmt.Errorf("result cannot be converted to the current schema: %w", err)
	}
	return res, validateResult(res, cniVersion, readStringField(stdout, "cniVersion")), nil
}

// validateResult returns a list of schema violations (empty when clean).
// rawVersion is the cniVersion as printed by the plugin (GetResult rewrites
// r.CNIVersion to the library's current version, so it cannot be used).
func validateResult(r *current.Result, cniVersion, rawVersion string) []string {
	var problems []string
	// 0.1.0/0.2.0 results do not carry cniVersion, so only assert echo for 0.3.0+.
	if ok, _ := version.GreaterThanOrEqualTo(cniVersion, "0.3.0"); ok && rawVersion != cniVersion {
		problems = append(problems, fmt.Sprintf("result cniVersion %q != config cniVersion %q", rawVersion, cniVersion))
	}
	for i, iface := range r.Interfaces {
		if iface == nil {
			problems = append(problems, fmt.Sprintf("interfaces[%d] is null", i))
			continue
		}
		if iface.Name == "" {
			problems = append(problems, fmt.Sprintf("interfaces[%d] has an empty name", i))
		}
	}
	for i, ip := range r.IPs {
		if ip == nil {
			problems = append(problems, fmt.Sprintf("ips[%d] is null", i))
			continue
		}
		if ip.Address.IP == nil || ip.Address.Mask == nil {
			problems = append(problems, fmt.Sprintf("ips[%d] has no valid CIDR address", i))
		}
		if ip.Interface != nil && (*ip.Interface < 0 || *ip.Interface >= len(r.Interfaces)) {
			problems = append(problems, fmt.Sprintf("ips[%d].interface=%d is out of range (have %d interfaces)", i, *ip.Interface, len(r.Interfaces)))
		}
	}
	for i, rt := range r.Routes {
		if rt == nil {
			problems = append(problems, fmt.Sprintf("routes[%d] is null", i))
			continue
		}
		if rt.Dst.IP == nil || rt.Dst.Mask == nil {
			problems = append(problems, fmt.Sprintf("routes[%d] has no valid dst CIDR", i))
		}
	}
	return problems
}

// ResultIPs returns the IP addresses (without prefix) in the result.
func ResultIPs(r *current.Result) []string {
	var ips []string
	for _, ip := range r.IPs {
		if ip != nil && ip.Address.IP != nil {
			ips = append(ips, ip.Address.IP.String())
		}
	}
	return ips
}
