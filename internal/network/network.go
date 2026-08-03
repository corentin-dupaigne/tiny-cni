package network

import (
	"fmt"
	"log/slog"
	"os"
	"runtime"

	"github.com/corentin-dupaigne/tiny-cni/internal/cni"
	"github.com/corentin-dupaigne/tiny-cni/internal/ipam"
	"github.com/vishvananda/netlink"
	"golang.org/x/sys/unix"
)

func SetupVeth(args *cni.Args) error {
	hostIFNAME := netlink.NewLinkAttrs()
	hostIFNAME.Name = "veth0" // should be randomly generated later to avoid conflicts

	veth := netlink.NewVeth(hostIFNAME)
	veth.PeerName = args.IfName

	// open pod's namespace file to obtain its fd
	file, err := os.OpenFile(args.Netns, os.O_RDONLY, 0600)
	slog.Debug("Opening given pod's namespace file", "ns", args.Netns)
	if err != nil {
		return fmt.Errorf("opening namespace file: %w", err)
	}

	defer file.Close()

	veth.PeerNamespace = netlink.NsFd(file.Fd())

	slog.Debug("Deploying veth between host and pod's namespace")
	err = netlink.LinkAdd(veth)
	if err != nil {
		return fmt.Errorf("deploying veth: %w", err)
	}

	slog.Debug("Set host interface UP")
	err = netlink.LinkSetUp(veth)
	if err != nil {
		return fmt.Errorf("set up host interface %w:", err)
	}

	ip, err := ipam.GetNextIp(ipam.Subnet, ipam.StoragePath)
	slog.Debug("Available IP returned by IPAM", "IP", ip)
	if err != nil {
		return err
	}

	// switch to pod's namespace, set pod's ip
	ch := make(chan error)
	go func(fd int, nstype int) {
		runtime.LockOSThread()

		err := unix.Setns(fd, nstype)
		if err != nil {
			ch <- fmt.Errorf("switching netns: %w", err)
			return
		}

		parsedIp, err := netlink.ParseAddr(ip)
		slog.Debug("Parsed IP", "IP", parsedIp)
		if err != nil {
			ch <- fmt.Errorf("parsing ip: %w", err)
			return
		}

		podIf, err := netlink.LinkByName(args.IfName)
		slog.Debug("Searched for pod's interface", "if", podIf)
		if err != nil {
			ch <- fmt.Errorf("searching pod's interface: %w", err)
			return
		}

		err = netlink.AddrAdd(podIf, parsedIp)
		slog.Debug("Added addr to pod's interface", "if", podIf, "addr", parsedIp)
		if err != nil {
			ch <- fmt.Errorf("adding addr to pod's interface %w:", err)
			return
		}

		err = netlink.LinkSetUp(podIf)
		if err != nil {
			ch <- fmt.Errorf("set up pod's interface %w:", err)
			return
		}

		loIf, err := netlink.LinkByName("lo")
		slog.Debug("Searched for pod's lo interface", "if", loIf)
		if err != nil {
			ch <- fmt.Errorf("searching pod's lo interface %w:", err)
			return
		}

		err = netlink.LinkSetUp(loIf)
		if err != nil {
			ch <- fmt.Errorf("set up pod's lo interface %w:", err)
			return
		}

		ch <- nil
	}(int(file.Fd()), unix.CLONE_NEWNET)

	res := <-ch

	if res != nil {
		return res
	}

	return nil
}
