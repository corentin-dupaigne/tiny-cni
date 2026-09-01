package ipam

import (
	"encoding/json"
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

type IPAMState struct {
	// containerID -> IP
	ContainerToIp map[string]netip.Addr `json:"containerToIp"`
	AllocatedSet  map[netip.Addr]bool   `json:"allocatedSet"`
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

func (a *Allocator) Deallocate() (bool, error) {}

func (a *Allocator) Allocate(containerID string) (netip.Prefix, error) {
	state := IPAMState{}
	var candidate *netip.Addr

	data, err := os.ReadFile(a.storagePath)
	if errors.Is(err, os.ErrNotExist) {
		networkIP := a.subnet.Masked().Addr()
		// skip reserved addresses (network addr & gateway address)
		*candidate = networkIP.Next().Next()
		// state.ContainerToIp[containerID] = candidate
	} else if err != nil {
		return netip.Prefix{}, fmt.Errorf("reading file %s: %w", a.storagePath, err)
	} else {
		err := json.Unmarshal(data, &state)
		if err != nil {
			return netip.Prefix{}, fmt.Errorf("parsing IP from file %s: %w", a.storagePath, err)
		}
	}

	if candidate == nil {
		for _, ip := range state.ContainerToIp {
			leftCandidate := ip.Prev()
			leftCheck := !state.AllocatedSet[leftCandidate] && leftCandidate.IsValid() && !a.subnet.Contains(leftCandidate)
			if leftCheck {
				candidate = &leftCandidate
				break
			}

			rightCandidate := ip.Next()
			rightCheck := !state.AllocatedSet[rightCandidate] && rightCandidate.IsValid() && !a.subnet.Contains(rightCandidate)
			if rightCheck {
				candidate = &rightCandidate
				break
			}
		}
	}

	if candidate == nil {
		return netip.Prefix{}, fmt.Errorf("no IP addresses available in range %s", a.subnet)
	}

	state.ContainerToIp[containerID] = *candidate
	state.AllocatedSet[*candidate] = false

	newState, err := json.MarshalIndent(state, "", "	")
	if err != nil {
		return netip.Prefix{}, fmt.Errorf("marshaling new ipam state: %w", err)
	}

	err = os.WriteFile(a.storagePath, newState, 0644)
	if err != nil {
		return netip.Prefix{}, fmt.Errorf("writing in file %s: %w", a.storagePath, err)
	}

	return netip.PrefixFrom(*candidate, a.subnet.Bits()), nil
}
