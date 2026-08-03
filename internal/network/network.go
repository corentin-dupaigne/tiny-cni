package network

import (
	"fmt"
	"log/slog"
	"os"

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

	// switch to pod's namespace, set pod's ip
	ch := make(chan error)
	go func(fd int, nstype int) {
		err := unix.Setns(fd, nstype)
		if err != nil {
			ch <- fmt.Errorf("switching netns: %w", err)
			return
		}

		ip, err := ipam.GetNextIp(ipam.Subnet, ipam.StoragePath)
		if err != nil {
			ch <- err
			return
		}

		parsedIp, err := netlink.ParseAddr(ip)
		if err != nil {
			ch <- fmt.Errorf("parsing ip: %w", err)
			return
		}

		podIf, err := netlink.LinkByName(args.IfName)
		if err != nil {
			ch <- fmt.Errorf("searching pod's interface: %w", err)
			return
		}

		err = netlink.AddrAdd(podIf, parsedIp)
		if err != nil {
			ch <- fmt.Errorf("adding addr to pod's interface: %w", err)
			return
		}

		ch <- nil
	}(int(file.Fd()), unix.CLONE_NEWNET)

	res := <-ch

	if res != nil {
		return res
	}

	file.Close()

	return nil
}
