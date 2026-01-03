package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// Rule represents a single port forwarding configuration
type Rule struct {
	Server       string `yaml:"server"`
	LocalPort    int    `yaml:"local_port"`
	RemotePort   int    `yaml:"remote_port"`
	IdentityFile string `yaml:"identity_file"`
}

// Config represents the configuration file structure
type Config struct {
	Rules []Rule `yaml:"rules"`
}

// loadConfig reads and parses the YAML configuration file
func loadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}

	var config Config
	if err := yaml.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("failed to parse config: %w", err)
	}

	// Validate port ranges for all rules (valid ports are 1-65535)
	for i, rule := range config.Rules {
		// Validate required fields
		if rule.Server == "" {
			return nil, fmt.Errorf("server is required in rule %d", i+1)
		}
		if rule.IdentityFile == "" {
			return nil, fmt.Errorf("identity_file is required in rule %d", i+1)
		}

		// Validate that the identity file exists and is accessible
		identityPath := expandHomeDir(rule.IdentityFile)
		if _, err := os.Stat(identityPath); err != nil {
			return nil, fmt.Errorf("identity_file %q in rule %d is not accessible: %v", rule.IdentityFile, i+1, err)
		}

		// Validate port ranges
		if rule.LocalPort < 1 || rule.LocalPort > 65535 {
			return nil, fmt.Errorf("invalid local_port %d in rule %d: must be between 1 and 65535", rule.LocalPort, i+1)
		}
		if rule.RemotePort < 1 || rule.RemotePort > 65535 {
			return nil, fmt.Errorf("invalid remote_port %d in rule %d: must be between 1 and 65535", rule.RemotePort, i+1)
		}
	}

	return &config, nil
}

// expandHomeDir expands the tilde (~) in a path to the user's home directory
func expandHomeDir(path string) string {
	if path == "~" || strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			// If we can't get the home directory, return the path unchanged
			// This will likely fail later validation, which is appropriate
			return path
		}
		if path == "~" {
			return home
		}
		return filepath.Join(home, path[2:])
	}
	return path
}
