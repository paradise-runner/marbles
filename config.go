package main

import (
	"fmt"
	"os"

	"github.com/BurntSushi/toml"
)

// Config represents the ~/.marbles/config.toml settings.
type Config struct {
	DefaultAgent string `toml:"default_agent"`
	OutputFormat string `toml:"output_format"` // "human" or "json"
}

// LoadConfig reads the config file, returning defaults if file doesn't exist.
func LoadConfig() (*Config, error) {
	cfg := &Config{}
	data, err := os.ReadFile(DefaultConfigPath)
	if err != nil {
		if os.IsNotExist(err) {
			return cfg, nil
		}
		return nil, fmt.Errorf("read config: %w", err)
	}
	if err := toml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	return cfg, nil
}
