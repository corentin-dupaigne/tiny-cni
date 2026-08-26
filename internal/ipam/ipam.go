package ipam

import (
	"errors"
	"fmt"
	"net/netip"
	"os"
)

type Allocator struct {
	subnet      netip.Prefix
	storagePath string
	gatewayIP   netip.Addr
}

func NewAllocator(subnet string, storagePath string) (*Allocator, error) {

	prefix, err := netip.ParsePrefix(subnet)
	if err != nil {
		return nil, fmt.Errorf("parsing prefix from subnet %s: %w", subnet, err)
	}

	return &Allocator{
		prefix,
		storagePath,
		prefix.Masked().Addr().Next(),
	}, nil
}

func (a *Allocator) GatewayIP() string {
	return a.subnet.Masked().Addr().Next().String()
}

func (a *Allocator) Allocate() (netip.Prefix, error) {
	var availableIp netip.Addr

	data, err := os.ReadFile(a.storagePath)
	if errors.Is(err, os.ErrNotExist) {

		networkIp := a.subnet.Masked().Addr()

		availableIp = networkIp.Next().Next()

	} else if err != nil {
		return netip.Prefix{}, fmt.Errorf("reading file %s: %w", a.storagePath, err)
	} else {
		availableIp, err = netip.ParseAddr(string(data))
		if err != nil {
			return netip.Prefix{}, fmt.Errorf("parsing IP from file %s: %w", a.storagePath, err)
		}
	}

	if !availableIp.IsValid() {
		return netip.Prefix{}, fmt.Errorf("parsed IP is invalid: %s", availableIp)
	}

	if !a.subnet.Contains(availableIp.Next()) {
		return netip.Prefix{}, fmt.Errorf("no IP addresses available in range %s", a.subnet)
	}

	err = os.WriteFile(a.storagePath, []byte(availableIp.Next().String()), 0644)
	if err != nil {
		return netip.Prefix{}, fmt.Errorf("writing in file %s: %w", a.storagePath, err)
	}

	return netip.PrefixFrom(availableIp, a.subnet.Bits()), nil
}
