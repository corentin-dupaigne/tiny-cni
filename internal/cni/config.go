package cni

import (
	"encoding/json"
	"fmt"

	"github.com/containernetworking/cni/pkg/types"
)

type ipamConfig struct {
	types.IPAM
	Subnet      string `json:"subnet"`
	StoragePath string `json:"storagePath"`
}

type Config struct {
	types.PluginConf
	IPAM   ipamConfig `json:"ipam"`
	Bridge string     `json:"bridge"`
	Prefix string     `json:"prefix"`
}

func Parse(data []byte) (*Config, error) {
	var parsedConfig Config

	err := json.Unmarshal(data, &parsedConfig)

	if err != nil {
		return nil, fmt.Errorf("parsing netconf: %w", err)
	}

	return &parsedConfig, nil
}
