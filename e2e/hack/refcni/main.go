// refcni is a deliberately minimal CNI plugin used to exercise the conformance
// suite itself (the "tiny plugin" of the suite's self-check rule). It creates a
// dummy interface in the container netns and hands out addresses from a
// subnet using a flock-protected JSON file. It is not meant for real use.
//
// Config:
//
//	{"cniVersion":"1.1.0","name":"ref","type":"refcni","subnet":"10.250.0.0/24","dataDir":"/run/refcni"}
package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"syscall"

	"github.com/containernetworking/cni/pkg/skel"
	"github.com/containernetworking/cni/pkg/types"
	current "github.com/containernetworking/cni/pkg/types/100"
	"github.com/containernetworking/cni/pkg/version"
	"github.com/containernetworking/plugins/pkg/ns"
	"github.com/vishvananda/netlink"
)

type netConf struct {
	types.NetConf
	Subnet  string `json:"subnet"`
	DataDir string `json:"dataDir"`
}

func loadConf(data []byte) (*netConf, error) {
	c := &netConf{}
	if err := json.Unmarshal(data, c); err != nil {
		return nil, types.NewError(types.ErrDecodingFailure, "failed to parse config", err.Error())
	}
	if c.Subnet == "" {
		c.Subnet = "10.250.0.0/24"
	}
	if c.DataDir == "" {
		c.DataDir = "/run/refcni"
	}
	if _, _, err := net.ParseCIDR(c.Subnet); err != nil {
		return nil, types.NewError(types.ErrInvalidNetworkConfig, "bad subnet", err.Error())
	}
	return c, nil
}

// --- state store -----------------------------------------------------------

type entry struct {
	IP    string `json:"ip"`
	NetNS string `json:"netns"`
}

type state struct {
	Entries map[string]entry `json:"entries"` // key: containerID/ifname
}

type store struct {
	dir  string
	lock *os.File
	st   state
}

func openStore(dir string) (*store, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	lf, err := os.OpenFile(filepath.Join(dir, "lock"), os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(lf.Fd()), syscall.LOCK_EX); err != nil {
		lf.Close()
		return nil, err
	}
	s := &store{dir: dir, lock: lf, st: state{Entries: map[string]entry{}}}
	data, err := os.ReadFile(filepath.Join(dir, "state.json"))
	if err == nil && len(data) > 0 {
		if err := json.Unmarshal(data, &s.st); err != nil {
			s.close()
			return nil, err
		}
	}
	return s, nil
}

func (s *store) save() error {
	data, err := json.MarshalIndent(s.st, "", "  ")
	if err != nil {
		return err
	}
	tmp := filepath.Join(s.dir, "state.json.tmp")
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Join(s.dir, "state.json"))
}

func (s *store) close() {
	_ = syscall.Flock(int(s.lock.Fd()), syscall.LOCK_UN)
	_ = s.lock.Close()
}

func key(id, ifname string) string { return id + "/" + ifname }

// allocate returns the first free host address in subnet, skipping .0 and .1.
func (s *store) allocate(subnet string) (*net.IPNet, error) {
	_, ipnet, _ := net.ParseCIDR(subnet)
	used := map[string]bool{}
	for _, e := range s.st.Entries {
		used[e.IP] = true
	}
	ip := make(net.IP, len(ipnet.IP))
	copy(ip, ipnet.IP.To4())
	for i := 0; i < 2; i++ { // skip network and gateway
		inc(ip)
	}
	for ipnet.Contains(ip) {
		if !used[ip.String()] {
			return &net.IPNet{IP: append(net.IP(nil), ip...), Mask: ipnet.Mask}, nil
		}
		inc(ip)
	}
	return nil, errors.New("subnet exhausted")
}

func inc(ip net.IP) {
	for j := len(ip) - 1; j >= 0; j-- {
		ip[j]++
		if ip[j] != 0 {
			break
		}
	}
}

// --- interface helpers -----------------------------------------------------

func deleteLinkIn(netnsPath, ifname string) error {
	if netnsPath == "" {
		return nil
	}
	netns, err := ns.GetNS(netnsPath)
	if err != nil {
		return nil // netns is gone: nothing to delete
	}
	defer netns.Close()
	return netns.Do(func(ns.NetNS) error {
		link, err := netlink.LinkByName(ifname)
		if err != nil {
			if _, ok := err.(netlink.LinkNotFoundError); ok {
				return nil
			}
			return err
		}
		return netlink.LinkDel(link)
	})
}

// --- commands --------------------------------------------------------------

func cmdAdd(args *skel.CmdArgs) error {
	conf, err := loadConf(args.StdinData)
	if err != nil {
		return err
	}
	netns, err := ns.GetNS(args.Netns)
	if err != nil {
		return types.NewError(types.ErrInvalidNetNS, "failed to open netns", err.Error())
	}
	defer netns.Close()

	s, err := openStore(conf.DataDir)
	if err != nil {
		return err
	}
	defer s.close()

	if _, dup := s.st.Entries[key(args.ContainerID, args.IfName)]; dup {
		return fmt.Errorf("container %s already has interface %s", args.ContainerID, args.IfName)
	}
	addr, err := s.allocate(conf.Subnet)
	if err != nil {
		return err
	}

	var mac string
	err = netns.Do(func(ns.NetNS) error {
		if _, err := netlink.LinkByName(args.IfName); err == nil {
			return fmt.Errorf("interface %s already exists in %s", args.IfName, args.Netns)
		}
		link := &netlink.Dummy{LinkAttrs: netlink.LinkAttrs{Name: args.IfName}}
		if err := netlink.LinkAdd(link); err != nil {
			return fmt.Errorf("create %s: %w", args.IfName, err)
		}
		l, err := netlink.LinkByName(args.IfName)
		if err != nil {
			return err
		}
		if err := netlink.AddrAdd(l, &netlink.Addr{IPNet: addr}); err != nil {
			_ = netlink.LinkDel(l)
			return err
		}
		if err := netlink.LinkSetUp(l); err != nil {
			_ = netlink.LinkDel(l)
			return err
		}
		mac = l.Attrs().HardwareAddr.String()
		return nil
	})
	if err != nil {
		return err
	}

	s.st.Entries[key(args.ContainerID, args.IfName)] = entry{IP: addr.IP.String(), NetNS: args.Netns}
	if err := s.save(); err != nil {
		_ = deleteLinkIn(args.Netns, args.IfName)
		return err
	}

	result := &current.Result{
		CNIVersion: conf.CNIVersion,
		Interfaces: []*current.Interface{{Name: args.IfName, Mac: mac, Sandbox: args.Netns}},
		IPs:        []*current.IPConfig{{Interface: current.Int(0), Address: *addr}},
	}
	return types.PrintResult(result, conf.CNIVersion)
}

func cmdDel(args *skel.CmdArgs) error {
	conf, err := loadConf(args.StdinData)
	if err != nil {
		return err
	}
	s, err := openStore(conf.DataDir)
	if err != nil {
		return err
	}
	defer s.close()

	k := key(args.ContainerID, args.IfName)
	e, ok := s.st.Entries[k]
	netnsPath := args.Netns
	if netnsPath == "" && ok {
		netnsPath = e.NetNS
	}
	if err := deleteLinkIn(netnsPath, args.IfName); err != nil {
		return err
	}
	if ok {
		delete(s.st.Entries, k)
		return s.save()
	}
	return nil
}

func cmdCheck(args *skel.CmdArgs) error {
	if _, err := loadConf(args.StdinData); err != nil {
		return err
	}
	netns, err := ns.GetNS(args.Netns)
	if err != nil {
		return types.NewError(types.ErrInvalidNetNS, "failed to open netns", err.Error())
	}
	defer netns.Close()
	return netns.Do(func(ns.NetNS) error {
		_, err := netlink.LinkByName(args.IfName)
		return err
	})
}

func cmdStatus(*skel.CmdArgs) error { return nil }

func cmdGC(args *skel.CmdArgs) error {
	conf, err := loadConf(args.StdinData)
	if err != nil {
		return err
	}
	valid := map[string]bool{}
	for _, a := range conf.ValidAttachments {
		valid[key(a.ContainerID, a.IfName)] = true
	}
	s, err := openStore(conf.DataDir)
	if err != nil {
		return err
	}
	defer s.close()

	var errs []error
	for k, e := range s.st.Entries {
		if valid[k] {
			continue
		}
		ifname := filepath.Base(k)
		if err := deleteLinkIn(e.NetNS, ifname); err != nil {
			errs = append(errs, err)
			continue
		}
		delete(s.st.Entries, k)
	}
	if err := s.save(); err != nil {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

func main() {
	skel.PluginMainFuncs(skel.CNIFuncs{
		Add:    cmdAdd,
		Del:    cmdDel,
		Check:  cmdCheck,
		GC:     cmdGC,
		Status: cmdStatus,
	}, version.All, "refcni: minimal reference plugin for the conformance suite")
}
