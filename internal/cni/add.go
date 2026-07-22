package cni

import (
	"encoding/json"
	"github.com/corentin-dupaigne/tiny-cni/internal/config"
	"os"
)

type interfaces struct {
	Name       string `json:"name"`
	Mac        string `json:"mac"`
	Mtu        uint   `json:"mtu"`
	Sandbox    string `json:"sandbox"`
	SocketPath string `json:"socketPath"`
	PciId      string `json:"pciID"`
}

type ips struct {
	Address  string `json:"address"`
	Gateway  string `json:"gateway"`
	Interfac uint   `json:"interface"`
}

type routes struct {
	Dst      string `json:"dst"`
	Gw       string `json:"gw"`
	Mtu      uint   `json:"mtu"`
	Advmss   uint   `json:"advmss"`
	Priority uint   `json:"priority"`
	Table    uint   `json:"table"`
	Scope    uint   `json:"scope"`
}

type dns struct {
	Nameservers []string `json:"nameservers"`
	Domain      string   `json:"domain"`
	Search      []string `json:"search"`
	Options     []string `json:"options"`
}

type result struct {
	CniVersion string       `json:"cniVersion"`
	Interfaces []interfaces `json:"interfaces"`
	Ips        []ips        `json:"ips"`
	Routes     []routes     `json:"routes"`
	Dns        []dns        `json:"dns"`
}

func Add(args *Args, cfg *config.Config) error {

	// mock res, to be implemented
	res := result{
		CniVersion: cfg.CniVersion,
		Interfaces: []interfaces{{Name: args.IfName, Sandbox: args.Netns}},
		Ips:        []ips{{Address: "10.10.1.20/16", Gateway: "10.10.0.1"}},
	}

	json, err := json.Marshal(res)

	if err != nil {
		return err
	}

	os.Stdout.Write(json)

	return nil
}
