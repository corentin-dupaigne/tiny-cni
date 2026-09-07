// Package harness drives a CNI plugin binary exactly the way a container runtime
// would: config on stdin, parameters via CNI_* environment variables, result on
// stdout. It knows nothing about the plugin under test beyond what the CNI spec
// defines, and it never inspects plugin-specific configuration fields.
package harness

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Well-known CNI operations.
const (
	CmdAdd     = "ADD"
	CmdDel     = "DEL"
	CmdCheck   = "CHECK"
	CmdVersion = "VERSION"
	CmdStatus  = "STATUS"
	CmdGC      = "GC"
)

// Well-known error codes (CNI spec §Error section).
const (
	ErrIncompatibleVersion = 1
	ErrUnsupportedField    = 2
	ErrUnknownContainer    = 3
	ErrInvalidEnv          = 4
	ErrIOFailure           = 5
	ErrDecodingFailure     = 6
	ErrInvalidNetConfig    = 7
)

// Environment variable names used by the CNI protocol.
const (
	EnvCommand     = "CNI_COMMAND"
	EnvContainerID = "CNI_CONTAINERID"
	EnvNetNS       = "CNI_NETNS"
	EnvIfname      = "CNI_IFNAME"
	EnvArgs        = "CNI_ARGS"
	EnvPath        = "CNI_PATH"
)

// Harness holds everything needed to invoke the plugin under test.
type Harness struct {
	// PluginPath is the absolute path to the plugin binary.
	PluginPath string
	// Config is the user-provided network configuration, forwarded verbatim.
	Config []byte
	// CNIVersion is the cniVersion string read from Config (spec-mandated field).
	CNIVersion string
	// CNIPath is the value of CNI_PATH passed to the plugin (for delegation).
	CNIPath string
	// Timeout bounds a single plugin invocation.
	Timeout time.Duration
}

// New builds a Harness from a plugin path and a config file path.
func New(pluginPath, configPath, cniPath string, timeout time.Duration) (*Harness, error) {
	abs, err := filepath.Abs(pluginPath)
	if err != nil {
		return nil, err
	}
	st, err := os.Stat(abs)
	if err != nil {
		return nil, fmt.Errorf("plugin binary: %w", err)
	}
	if st.IsDir() || st.Mode()&0o111 == 0 {
		return nil, fmt.Errorf("plugin binary %s is not an executable file", abs)
	}
	conf, err := os.ReadFile(configPath)
	if err != nil {
		return nil, fmt.Errorf("config: %w", err)
	}
	if !json.Valid(conf) {
		return nil, fmt.Errorf("config %s is not valid JSON", configPath)
	}
	if cniPath == "" {
		cniPath = filepath.Dir(abs)
	}
	h := &Harness{
		PluginPath: abs,
		Config:     conf,
		CNIPath:    cniPath,
		Timeout:    timeout,
	}
	h.CNIVersion = readStringField(conf, "cniVersion")
	return h, nil
}

// readStringField extracts a top-level string field from a JSON object without
// touching anything else. Returns "" if absent or not a string.
func readStringField(conf []byte, key string) string {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(conf, &m); err != nil {
		return ""
	}
	var s string
	if err := json.Unmarshal(m[key], &s); err != nil {
		return ""
	}
	return s
}

// ConfigWithout returns a copy of the config with the given top-level key
// removed. Only spec-mandated keys (type, cniVersion) may be passed here; the
// harness must never remove plugin-specific keys.
func (h *Harness) ConfigWithout(key string) []byte {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(h.Config, &m); err != nil {
		return h.Config
	}
	delete(m, key)
	out, _ := json.Marshal(m)
	return out
}

// ConfigWithCNIVersion returns a copy of the config with cniVersion replaced.
func (h *Harness) ConfigWithCNIVersion(v string) []byte {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(h.Config, &m); err != nil {
		return h.Config
	}
	m["cniVersion"], _ = json.Marshal(v)
	out, _ := json.Marshal(m)
	return out
}

// Invocation describes one plugin call. Env values that are empty are not set,
// so tests can omit required variables deliberately.
type Invocation struct {
	Command     string
	ContainerID string
	NetNS       string
	Ifname      string
	Args        string
	// Stdin overrides the harness config when non-nil.
	Stdin []byte
	// OmitPath suppresses CNI_PATH (it is optional for most commands).
	OmitPath bool
}

// Output is what the plugin produced.
type Output struct {
	Stdout   []byte
	Stderr   []byte
	ExitCode int
	Duration time.Duration
	// Env is the environment that was passed, for diagnostics.
	Env []string
}

// Error decodes stdout as a CNI error structure. It returns nil if stdout does
// not contain a parseable error object.
func (o *Output) Error() *CNIError {
	e, err := ParseError(o.Stdout)
	if err != nil {
		return nil
	}
	return e
}

// String renders the output for failure messages.
func (o *Output) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "exit=%d duration=%s\n", o.ExitCode, o.Duration.Round(time.Millisecond))
	fmt.Fprintf(&b, "env: %s\n", strings.Join(o.Env, " "))
	fmt.Fprintf(&b, "stdout: %s\n", trimForLog(o.Stdout))
	fmt.Fprintf(&b, "stderr: %s", trimForLog(o.Stderr))
	return b.String()
}

func trimForLog(b []byte) string {
	const max = 2000
	s := strings.TrimSpace(string(b))
	if len(s) > max {
		return s[:max] + "...(truncated)"
	}
	if s == "" {
		return "<empty>"
	}
	return s
}

// Run executes the plugin once. A non-zero exit is not a Go error; only
// failures to start the process, or a timeout, are.
func (h *Harness) Run(inv Invocation) (*Output, error) {
	ctx, cancel := context.WithTimeout(context.Background(), h.Timeout)
	defer cancel()

	env := []string{"PATH=" + os.Getenv("PATH")}
	set := func(k, v string) {
		if v != "" {
			env = append(env, k+"="+v)
		}
	}
	set(EnvCommand, inv.Command)
	set(EnvContainerID, inv.ContainerID)
	set(EnvNetNS, inv.NetNS)
	set(EnvIfname, inv.Ifname)
	set(EnvArgs, inv.Args)
	if !inv.OmitPath {
		set(EnvPath, h.CNIPath)
	}

	stdin := inv.Stdin
	if stdin == nil {
		stdin = h.Config
	}

	cmd := exec.CommandContext(ctx, h.PluginPath)
	cmd.Env = env
	cmd.Stdin = bytes.NewReader(stdin)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	start := time.Now()
	err := cmd.Run()
	out := &Output{
		Stdout:   stdout.Bytes(),
		Stderr:   stderr.Bytes(),
		Duration: time.Since(start),
		Env:      env[1:], // drop PATH from diagnostics
	}
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return out, fmt.Errorf("plugin timed out after %s (%s)", h.Timeout, inv.Command)
		}
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			out.ExitCode = ee.ExitCode()
			return out, nil
		}
		return out, fmt.Errorf("failed to run plugin: %w", err)
	}
	return out, nil
}

// NewContainerID returns a random container ID, unique per call.
func NewContainerID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "cni-conf-" + strconv.FormatInt(time.Now().UnixNano(), 16)
	}
	return "cni-conf-" + hex.EncodeToString(b)
}
