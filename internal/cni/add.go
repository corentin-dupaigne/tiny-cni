package cni

import (
	"github.com/containernetworking/cni/pkg/skel"
	cnitypes "github.com/containernetworking/cni/pkg/types"
	cniv1 "github.com/containernetworking/cni/pkg/types/100"
	"github.com/corentin-dupaigne/tiny-cni/internal/network"
)

func Add(args *skel.CmdArgs) error {
	conf, err := Parse(args.StdinData)
	if err != nil {
		return err
	}

	res := &cniv1.Result{}
	res.CNIVersion = conf.CNIVersion

	success, err := network.Setup(network.SetupParams{
		StoragePath: conf.IPAM.StoragePath,
		Subnet:      conf.IPAM.Subnet,
		Prefix:      conf.Prefix,
		Bridge:      conf.Bridge,
		IfName:      args.IfName,
		Netns:       args.Netns,
	})

	if err != nil {
		return err
	}

	res.Interfaces = []*cniv1.Interface{}
	for _, inf := range success.Interfaces {
		res.Interfaces = append(res.Interfaces, &cniv1.Interface{
			Name:    inf.Name,
			Mac:     inf.Mac,
			Mtu:     inf.Mtu,
			Sandbox: inf.Sandbox,
		})
	}

	res.IPs = []*cniv1.IPConfig{}
	for _, ip := range success.Ips {
		res.IPs = append(res.IPs, &cniv1.IPConfig{
			Interface: &ip.IpInterface,
			Address:   ip.Address,
		})
	}

	return cnitypes.PrintResult(res, conf.CNIVersion)
}
