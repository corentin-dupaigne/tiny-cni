package ipam

// NOTE: set this to match the package getNextIp actually lives in.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGetNextIp(t *testing.T) {
	tests := []struct {
		name          string
		subnet        string
		seed          *string // nil => no file on disk (ErrNotExist branch); non-nil => pre-write this content
		want          string  // expected returned IP (what a correct allocator SHOULD return)
		wantErr       bool
		wantErrSubstr string // optional substring the error message should contain
	}{
		{
			// First call, no counter file yet. What this SHOULD return on a fresh
			// range is your design decision — set `want` to your intended first IP.
			name:    "file absent",
			subnet:  "10.244.1.0/24",
			seed:    nil,
			want:    "", // TODO: set to your intended first-call return value
			wantErr: false,
		},
		{
			name:    "normal allocation mid-range",
			subnet:  "10.244.1.0/24",
			seed:    strPtr("5/254"),
			want:    "10.244.1.5", // correct-allocator expectation; adjust to your numbering scheme
			wantErr: false,
		},
		{
			name:    "normal allocation low",
			subnet:  "10.244.1.0/24",
			seed:    strPtr("1/254"),
			want:    "10.244.1.1",
			wantErr: false,
		},
		{
			name:          "exhausted range",
			subnet:        "10.244.1.0/24",
			seed:          strPtr("254/254"),
			wantErr:       true,
			wantErrSubstr: "no IP addresses available",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			counterPath := filepath.Join(t.TempDir(), "counter")
			if tt.seed != nil {
				if err := os.WriteFile(counterPath, []byte(*tt.seed), 0644); err != nil {
					t.Fatalf("seeding counter file: %v", err)
				}
			}

			got, err := getNextIp(tt.subnet, counterPath)

			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected an error, got nil (returned %q)", got)
				}
				if tt.wantErrSubstr != "" && !strings.Contains(err.Error(), tt.wantErrSubstr) {
					t.Errorf("error = %q, want it to contain %q", err.Error(), tt.wantErrSubstr)
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Errorf("getNextIp(%q, ...) = %q, want %q", tt.subnet, got, tt.want)
			}
		})
	}
}

// Asserts the unambiguous part of the absent-file branch: the counter file
// must exist after the call, regardless of what value gets returned.
func TestGetNextIp_CreatesCounterWhenAbsent(t *testing.T) {
	t.Parallel()

	counterPath := filepath.Join(t.TempDir(), "counter")

	if _, err := getNextIp("10.244.1.0/24", counterPath); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, err := os.Stat(counterPath); err != nil {
		t.Errorf("expected counter file to be created at %s, but stat failed: %v", counterPath, err)
	}
}

func strPtr(s string) *string { return &s }
