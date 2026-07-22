package config

import (
	"encoding/json"
	"fmt"
)

type ipamConfig struct {
	Type string `json:"type"`
}

type Config struct {
	CniVersion string     `json:"cniVersion"`
	Name       string     `json:"name"`
	Type       string     `json:"type"`
	Ipam       ipamConfig `json:"ipam"`
}

func Parse(data []byte) (*Config, error) {
	var parsedConfig Config

	err := json.Unmarshal(data, &parsedConfig)

	if err != nil {
		return nil, fmt.Errorf("parsing netconf: %w", err)
	}

	return &parsedConfig, nil
}
