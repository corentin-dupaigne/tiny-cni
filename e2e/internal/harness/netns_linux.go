package harness

import (
	"fmt"
	"net"
	"os"

	"github.com/containernetworking/plugins/pkg/ns"
	"github.com/containernetworking/plugins/pkg/testutils"
	"github.com/vishvananda/netlink"
)

// NetNS is a container network namespace created for one test.
type NetNS struct {
	ns   ns.NetNS
	path string
	gone bool
}

// NewNetNS creates a fresh, mounted network namespace. Requires root.
func NewNetNS() (*NetNS, error) {
	n, err := testutils.NewNS()
	if err != nil {
		return nil, fmt.Errorf("create netns: %w", err)
	}
	return &NetNS{ns: n, path: n.Path()}, nil
}

// Path is the value to pass as CNI_NETNS.
func (n *NetNS) Path() string { return n.path }

// Close unmounts and removes the namespace. Safe to call twice.
func (n *NetNS) Close() error {
	if n.gone {
		return nil
	}
	n.gone = true
	// The fd NewNS keeps open pins the bind mount: close it before
	// unmounting, or unmount fails with EBUSY.
	if err := n.ns.Close(); err != nil {
		return fmt.Errorf("close netns fd: %w", err)
	}
	return testutils.UnmountNS(n.ns)
}

// Exists reports whether the namespace path is still a usable netns.
func (n *NetNS) Exists() bool {
	if n.gone {
		return false
	}
	return ns.IsNSorErr(n.path) == nil
}

// HasInterface reports whether an interface named ifname exists inside the
// namespace, using netlink after entering the namespace.
func (n *NetNS) HasInterface(ifname string) (bool, error) {
	if n.gone {
		return false, fmt.Errorf("netns %s no longer exists", n.path)
	}
	found := false
	err := ns.WithNetNSPath(n.path, func(ns.NetNS) error {
		_, err := netlink.LinkByName(ifname)
		if err == nil {
			found = true
			return nil
		}
		if _, ok := err.(netlink.LinkNotFoundError); ok {
			return nil
		}
		return err
	})
	return found, err
}

// Interfaces lists the interface names present in the namespace.
func (n *NetNS) Interfaces() ([]string, error) {
	var names []string
	err := ns.WithNetNSPath(n.path, func(ns.NetNS) error {
		links, err := netlink.LinkList()
		if err != nil {
			return err
		}
		for _, l := range links {
			names = append(names, l.Attrs().Name)
		}
		return nil
	})
	return names, err
}

// LinkState is what can be observed about one interface inside the netns.
type LinkState struct {
	Name  string
	Up    bool
	MAC   string
	MTU   int
	Addrs []string // CIDR strings, e.g. "10.0.0.2/24"
}

// HasAddr reports whether the interface carries the given CIDR.
func (l *LinkState) HasAddr(cidr string) bool {
	want, err := normalizeCIDR(cidr)
	if err != nil {
		return false
	}
	for _, a := range l.Addrs {
		if got, err := normalizeCIDR(a); err == nil && got == want {
			return true
		}
	}
	return false
}

// normalizeCIDR renders "ip/prefix" with the host bits kept (an address,
// not a network), so 10.0.0.2/24 stays 10.0.0.2/24.
func normalizeCIDR(s string) (string, error) {
	ip, ipnet, err := net.ParseCIDR(s)
	if err != nil {
		return "", err
	}
	ones, _ := ipnet.Mask.Size()
	return fmt.Sprintf("%s/%d", ip.String(), ones), nil
}

// Link returns the observable state of ifname inside the namespace, or nil
// (with no error) if the interface does not exist.
func (n *NetNS) Link(ifname string) (*LinkState, error) {
	if n.gone {
		return nil, fmt.Errorf("netns %s no longer exists", n.path)
	}
	var st *LinkState
	err := ns.WithNetNSPath(n.path, func(ns.NetNS) error {
		link, err := netlink.LinkByName(ifname)
		if err != nil {
			if _, ok := err.(netlink.LinkNotFoundError); ok {
				return nil
			}
			return err
		}
		attrs := link.Attrs()
		st = &LinkState{
			Name: attrs.Name,
			Up:   attrs.Flags&net.FlagUp != 0,
			MAC:  attrs.HardwareAddr.String(),
			MTU:  attrs.MTU,
		}
		addrs, err := netlink.AddrList(link, netlink.FAMILY_ALL)
		if err != nil {
			return err
		}
		for _, a := range addrs {
			if a.IPNet != nil {
				st.Addrs = append(st.Addrs, a.IPNet.String())
			}
		}
		return nil
	})
	return st, err
}

// RouteState is one routing-table entry inside the netns.
type RouteState struct {
	Dst   string // CIDR; "0.0.0.0/0" or "::/0" for a default route
	Gw    string // empty when on-link
	Iface string
}

// Routes lists the main-table routes inside the namespace.
func (n *NetNS) Routes() ([]RouteState, error) {
	if n.gone {
		return nil, fmt.Errorf("netns %s no longer exists", n.path)
	}
	var out []RouteState
	err := ns.WithNetNSPath(n.path, func(ns.NetNS) error {
		links, err := netlink.LinkList()
		if err != nil {
			return err
		}
		names := map[int]string{}
		for _, l := range links {
			names[l.Attrs().Index] = l.Attrs().Name
		}
		routes, err := netlink.RouteList(nil, netlink.FAMILY_ALL)
		if err != nil {
			return err
		}
		for _, r := range routes {
			rs := RouteState{Iface: names[r.LinkIndex]}
			switch {
			case r.Dst != nil:
				rs.Dst = r.Dst.String()
			case r.Family == netlink.FAMILY_V6:
				rs.Dst = "::/0"
			default:
				rs.Dst = "0.0.0.0/0"
			}
			if r.Gw != nil {
				rs.Gw = r.Gw.String()
			}
			out = append(out, rs)
		}
		return nil
	})
	return out, err
}

// IsRoot reports whether the process can create namespaces.
func IsRoot() bool { return os.Geteuid() == 0 }
