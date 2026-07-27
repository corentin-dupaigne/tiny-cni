package cni

import (
	"testing"
)

func TestLoadArgs(t *testing.T) {
	t.Setenv("CNI_COMMAND", "ADD")
	t.Setenv("CNI_CONTAINERID", "container-123")
	t.Setenv("CNI_IFNAME", "eth0")
	t.Setenv("CNI_NETNS", "/var/run/netns/ns1")
	t.Setenv("CNI_ARGS", "K8S_POD_NAME=pod1")
	t.Setenv("CNI_PATH", "/opt/cni/bin")

	got := LoadArgs()

	want := Args{
		Command:     "ADD",
		ContainerId: "container-123",
		IfName:      "eth0",
		Netns:       "/var/run/netns/ns1",
		Args:        "K8S_POD_NAME=pod1",
		Path:        "/opt/cni/bin",
	}

	if *got != want {
		t.Errorf("LoadArgs() = %+v, want %+v", *got, want)
	}
}

func TestLoadArgsUnsetEnv(t *testing.T) {
	// t.Setenv guarantees these are restored after the test, and setting them
	// empty gives a deterministic starting point regardless of the outer env.
	t.Setenv("CNI_COMMAND", "")
	t.Setenv("CNI_CONTAINERID", "")
	t.Setenv("CNI_IFNAME", "")
	t.Setenv("CNI_NETNS", "")
	t.Setenv("CNI_ARGS", "")
	t.Setenv("CNI_PATH", "")

	got := LoadArgs()

	if *got != (Args{}) {
		t.Errorf("LoadArgs() with unset env = %+v, want zero-value Args", *got)
	}
}
