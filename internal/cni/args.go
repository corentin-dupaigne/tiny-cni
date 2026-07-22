package cni

import (
	"os"
)

type Args struct {
	Command     string
	ContainerId string
	IfName      string
	Netns       string
	Args        string
	Path        string
}

func LoadArgs() *Args {
	return &Args{
		Command:     os.Getenv("CNI_COMMAND"),
		ContainerId: os.Getenv("CNI_CONTAINERID"),
		IfName:      os.Getenv("CNI_IFNAME"),
		Netns:       os.Getenv("CNI_NETNS"),
		Args:        os.Getenv("CNI_ARGS"),
		Path:        os.Getenv("CNI_PATH"),
	}
}
