package cni

import (
	"github.com/containernetworking/cni/pkg/skel"
	"github.com/corentin-dupaigne/tiny-cni/internal/network"
)

func Del(args *skel.CmdArgs) error {
	conf, err := Parse(args.StdinData)
	if err != nil {
		return err
	}

	err = network.Teardown(network.TeardownParams{
		StoragePath: conf.IPAM.StoragePath,
		Subnet:      conf.IPAM.Subnet,
		ContainerID: args.ContainerID,
		Netns:       args.Netns,
		IfName:      args.IfName,
	})

	return err
}
