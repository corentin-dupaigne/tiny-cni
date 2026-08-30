package cni

import (
	"strings"
	"testing"
)

func TestParse(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    Config
		wantErr bool
	}{
		{
			name:  "valid full config",
			input: `{"cniVersion":"1.0.0","name":"tiny","type":"tiny-cni","ipam":{"type":"host-local"}}`,
			want: Config{
				CniVersion: "1.0.0",
				Name:       "tiny",
				Type:       "tiny-cni",
				Ipam:       ipamConfig{Type: "host-local"},
			},
		},
		{
			name:  "missing fields default to zero values",
			input: `{"cniVersion":"1.0.0"}`,
			want: Config{
				CniVersion: "1.0.0",
			},
		},
		{
			name:  "unknown fields are ignored",
			input: `{"cniVersion":"1.0.0","name":"tiny","extra":"ignored"}`,
			want: Config{
				CniVersion: "1.0.0",
				Name:       "tiny",
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
			if *got != tt.want {
				t.Errorf("Parse() = %+v, want %+v", *got, tt.want)
			}
		})
	}
}
