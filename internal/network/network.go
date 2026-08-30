package network

import (
	"crypto/rand"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"runtime"

	"github.com/corentin-dupaigne/tiny-cni/internal/ipam"
	"github.com/vishvananda/netlink"
	"golang.org/x/sys/unix"
)

type SetupParams struct {
	StoragePath string
	Subnet      string
	Prefix      string
	Bridge      string
	IfName      string
	Netns       string
}

func generateRandName(prefix string) (string, error) {
	b := make([]byte, 4)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generating random name: %w", err)
	}
	return fmt.Sprintf("%s-%x", prefix, b), nil
}

func setupBridge(bridgeName string) (netlink.Link, error) {
	link := netlink.NewLinkAttrs()
	link.Name = bridgeName

	bridge := &netlink.Bridge{LinkAttrs: link}

	err := netlink.LinkAdd(bridge)
	if err != nil {
		return nil, fmt.Errorf("deploying bridge: %w", err)
	}

	err = netlink.LinkSetUp(bridge)
	if err != nil {
		return nil, fmt.Errorf("set bridge up: %w", err)
	}

	return bridge, nil
}

func bridge(bridgeName string) (netlink.Link, error) {
	bridge, err := netlink.LinkByName(bridgeName)

	var notFound netlink.LinkNotFoundError
	if err != nil && errors.As(err, &notFound) {
		return setupBridge(bridgeName)
	} else if err != nil {
		return nil, fmt.Errorf("searching for bridge by name: %w", err)
	}

	return bridge, nil
}

func Setup(args SetupParams) error {
	success := false

	hostIFNAME := netlink.NewLinkAttrs()
	name, err := generateRandName(args.Prefix)
	if err != nil {
		return fmt.Errorf("generating random name: %w", err)
	}
	hostIFNAME.Name = name

	veth := netlink.NewVeth(hostIFNAME)
	veth.PeerName = args.IfName

	bridge, err := bridge(args.Bridge)
	if err != nil {
		return err
	}
	slog.Debug("Bridge found", "bridge name", bridge.Attrs().Name)
	veth.MasterIndex = bridge.Attrs().Index

	// open pod's namespace file to obtain its fd
	file, err := os.OpenFile(args.Netns, os.O_RDONLY, 0600)
	if err != nil {
		return fmt.Errorf("opening namespace file: %w", err)
	}
	slog.Debug("Openend given pod's namespace file", "ns", args.Netns)

	defer file.Close()

	veth.PeerNamespace = netlink.NsFd(file.Fd())

	err = netlink.LinkAdd(veth)
	if err != nil {
		return fmt.Errorf("deploying veth: %w", err)
	}
	slog.Debug("Deployed veth between host and pod's namespace")

	defer func() {
		if !success {
			err := netlink.LinkDel(veth)
			if err != nil {
				slog.Error("Tearing down created veth", "success", success, "err", err)
			}
		}
	}()

	err = netlink.LinkSetUp(veth)
	if err != nil {
		return fmt.Errorf("set up host interface %w:", err)
	}
	slog.Debug("Set host interface UP")

	alloc, err := ipam.NewAllocator(args.Subnet, args.StoragePath)
	if err != nil {
		return fmt.Errorf("Instantiating allocator: %w", err)
	}
	slog.Debug("Instantiated allocator")

	ip, err := alloc.Allocate()
	if err != nil {
		return err
	}
	slog.Debug("Available IP returned by IPAM", "IP", ip)

	// switch to pod's namespace, set pod's ip
	ch := make(chan error)
	go func(fd int, nstype int) {
		runtime.LockOSThread()

		err := unix.Setns(fd, nstype)
		if err != nil {
			ch <- fmt.Errorf("switching netns: %w", err)
			return
		}

		parsedIp, err := netlink.ParseAddr(ip.String())
		if err != nil {
			ch <- fmt.Errorf("parsing ip: %w", err)
			return
		}
		slog.Debug("Parsed IP", "IP", ip.String())

		podIf, err := netlink.LinkByName(args.IfName)
		if err != nil {
			ch <- fmt.Errorf("searching pod's interface: %w", err)
			return
		}
		slog.Debug("Searched for pod's interface")

		err = netlink.AddrAdd(podIf, parsedIp)
		if err != nil {
			ch <- fmt.Errorf("adding addr to pod's interface: %w", err)
			return
		}
		slog.Debug("Added addr to pod's interface", "addr", parsedIp.String())

		err = netlink.LinkSetUp(podIf)
		if err != nil {
			ch <- fmt.Errorf("set up pod's interface: %w", err)
			return
		}

		loIf, err := netlink.LinkByName("lo")
		if err != nil {
			ch <- fmt.Errorf("searching pod's lo interface: %w", err)
			return
		}
		slog.Debug("Searched for pod's lo interface")

		err = netlink.LinkSetUp(loIf)
		if err != nil {
			ch <- fmt.Errorf("set up pod's lo interface: %w", err)
			return
		}
		slog.Debug("Set up pod's lo interface")

		ch <- nil
	}(int(file.Fd()), unix.CLONE_NEWNET)

	res := <-ch

	if res != nil {
		return res
	}

	success = true

	return nil
}
