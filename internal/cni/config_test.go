package cni

import (
	"reflect"
	"strings"
	"testing"

	"github.com/containernetworking/cni/pkg/types"
)

func TestParse(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    Config
		wantErr bool
	}{
		{
			name: "valid full config",
			input: `{"cniVersion":"1.0.0","name":"tiny","type":"tiny-cni","bridge":"tcni-bridge","prefix":"tcni",
				"ipam":{"type":"tiny-ipam","subnet":"10.244.0.0/24","storagePath":"/var/lib/cni/tiny/state.json"}}`,
			want: Config{
				PluginConf: types.PluginConf{
					CNIVersion: "1.0.0",
					Name:       "tiny",
					Type:       "tiny-cni",
				},
				IPAM: ipamConfig{
					IPAM:        types.IPAM{Type: "tiny-ipam"},
					Subnet:      "10.244.0.0/24",
					StoragePath: "/var/lib/cni/tiny/state.json",
				},
				Bridge: "tcni-bridge",
				Prefix: "tcni",
			},
		},
		{
			name:  "missing fields default to zero values",
			input: `{"cniVersion":"1.0.0"}`,
			want: Config{
				PluginConf: types.PluginConf{CNIVersion: "1.0.0"},
			},
		},
		{
			name:  "unknown fields are ignored",
			input: `{"cniVersion":"1.0.0","name":"tiny","extra":"ignored"}`,
			want: Config{
				PluginConf: types.PluginConf{
					CNIVersion: "1.0.0",
					Name:       "tiny",
				},
			},
		},
		{
			name:    "malformed json returns error",
			input:   `{"cniVersion":`,
			wantErr: true,
		},
		{
			name:    "empty input returns error",
			input:   ``,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Parse([]byte(tt.input))

			if tt.wantErr {
				if err == nil {
					t.Fatalf("Parse() expected an error, got nil")
				}
				if !strings.Contains(err.Error(), "parsing netconf") {
					t.Errorf("Parse() error = %q, want it to wrap %q", err, "parsing netconf")
				}
				return
			}

			if err != nil {
				t.Fatalf("Parse() unexpected error: %v", err)
			}
			if got == nil {
				t.Fatalf("Parse() returned nil config without error")
			}
			// Config embeds types.PluginConf, whose maps and slices rule out ==
			if !reflect.DeepEqual(*got, tt.want) {
				t.Errorf("Parse() = %+v, want %+v", *got, tt.want)
			}
		})
	}
}

// Config declares its own "ipam" field while the types.PluginConf it embeds
// declares one too. The shallower field wins, so the block has to land in the
// plugin's ipamConfig — the one carrying subnet and storagePath, which is what
// Add and Del read. Were the embedded field to win instead, both would silently
// hand the allocator empty strings.
func TestParseReadsIPAMIntoThePluginConfig(t *testing.T) {
	const input = `{"cniVersion":"1.0.0","ipam":{"type":"tiny-ipam","subnet":"10.244.0.0/24","storagePath":"/run/tiny.json"}}`

	got, err := Parse([]byte(input))
	if err != nil {
		t.Fatalf("Parse() unexpected error: %v", err)
	}

	if got.IPAM.Subnet != "10.244.0.0/24" {
		t.Errorf("IPAM.Subnet = %q, want %q", got.IPAM.Subnet, "10.244.0.0/24")
	}
	if got.IPAM.StoragePath != "/run/tiny.json" {
		t.Errorf("IPAM.StoragePath = %q, want %q", got.IPAM.StoragePath, "/run/tiny.json")
	}
	if got.IPAM.Type != "tiny-ipam" {
		t.Errorf("IPAM.Type = %q, want %q", got.IPAM.Type, "tiny-ipam")
	}

	if got.PluginConf.IPAM.Type != "" {
		t.Errorf("PluginConf.IPAM.Type = %q, want it left empty: the outer ipam field shadows it",
			got.PluginConf.IPAM.Type)
	}
}
