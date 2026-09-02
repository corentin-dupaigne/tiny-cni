package ipam

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/netip"
	"os"

	"golang.org/x/sys/unix"
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

func (a *Allocator) Deallocate(containerID string) bool {
	state := IPAMState{}

	data, err := os.ReadFile(a.storagePath)
	if err != nil {
		return false
	}

	err = json.Unmarshal(data, &state)
	if err != nil {
		return false
	}

	if state.AllocatedSet[state.ContainerToIp[containerID]] == false {
		return false
	}

	state.AllocatedSet[state.ContainerToIp[containerID]] = false

	delete(state.ContainerToIp, containerID)

	return false

}

func (a *Allocator) defaultState() *IPAMState {
	state := &IPAMState{}
	networkIP := a.subnet.Masked().Addr()
	// set reserved addresses (network addr & gateway address) as allocated
	state.AllocatedSet = make(map[netip.Addr]bool)
	state.AllocatedSet[networkIP] = true
	state.AllocatedSet[networkIP.Next()] = true

	return state
}

func (a *Allocator) withLockedState(fn func(*IPAMState) error) error {
	var state *IPAMState
	var file *os.File

	file, err := os.OpenFile(a.storagePath, os.O_CREATE|os.O_RDWR, 0644)
	if err != nil {
		return fmt.Errorf("opening/creating file: %w", err)
	}
	slog.Debug("Opened/created file", "file", a.storagePath)

	fd := int(file.Fd())

	err = unix.Flock(fd, unix.LOCK_EX)
	if err != nil {
		return err
	}
	slog.Debug("Locked file with Flock syscall", "file", a.storagePath)

	defer func() {
		unix.Flock(fd, unix.LOCK_UN)
		file.Close()
	}()

	fileStat, err := file.Stat()
	if err != nil {
		return fmt.Errorf("Error reading file stats")
	}
	slog.Debug("Read file stat", "file", a.storagePath)

	data := make([]byte, fileStat.Size())

	size, err := file.Read(data)
	if err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	slog.Debug("Read file content", "file", a.storagePath)

	if size != 0 {
		err := json.Unmarshal(data, &state)
		if err != nil {
			return err
		}
		slog.Debug("State file exists -- unmarshal content", "file", a.storagePath)
	} else {
		state = a.defaultState()
		slog.Debug("State file doesn't exist -- build default state", "file", a.storagePath)
	}

	err = fn(state)
	if err != nil {
		return err
	}
	slog.Debug("Called wrapped func")

	newStateBytes, err := json.MarshalIndent(state, "", "	")

	err = os.WriteFile(a.storagePath, newStateBytes, 0644)
	if err != nil {
		return fmt.Errorf("writing in file %s: %w", a.storagePath, err)
	}
	slog.Debug("Wrote new IPAM state", "file", a.storagePath)

	return nil
}

func (a *Allocator) Allocate(containerID string) (netip.Prefix, error) {
	var candidate *netip.Addr

	slog.Debug("Start allocator")

	err := a.withLockedState(func(i *IPAMState) error {
		if i.ContainerToIp == nil {
			networkIP := a.subnet.Masked().Addr()
			addr := networkIP.Next().Next()
			candidate = &addr
		} else {
			for _, ip := range i.ContainerToIp {
				leftCandidate := ip.Prev()
				leftCheck := !i.AllocatedSet[leftCandidate] && leftCandidate.IsValid() && a.subnet.Contains(leftCandidate)
				if leftCheck {
					candidate = &leftCandidate
					break
				}

				rightCandidate := ip.Next()
				rightCheck := !i.AllocatedSet[rightCandidate] && rightCandidate.IsValid() && a.subnet.Contains(rightCandidate)
				if rightCheck {
					candidate = &rightCandidate
					break
				}
			}
		}

		if candidate == nil {
			return fmt.Errorf("no IP addresses available in range %s", a.subnet)
		}

		slog.Debug("Available IP found", "IP", candidate)

		if i.ContainerToIp == nil {
			i.ContainerToIp = make(map[string]netip.Addr)
		}

		if i.AllocatedSet == nil {
			i.AllocatedSet = make(map[netip.Addr]bool)
		}

		i.ContainerToIp[containerID] = *candidate
		i.AllocatedSet[*candidate] = true

		return nil

	})

	return netip.PrefixFrom(*candidate, a.subnet.Bits()), err
}
