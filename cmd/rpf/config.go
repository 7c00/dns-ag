package main

import (
	"fmt"
	"os"
	"path/filepath"

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

// findConfigFile searches for the configuration file in order of priority:
// 1. Command line argument (if provided)
// 2. ./rpf.yaml (current directory)
// 3. ~/.config/rpf/rpf.yaml (user config directory)
// Returns the path to the first existing config file, or an error if none found
func findConfigFile() (string, error) {
	// Priority 1: Command line argument
	if len(os.Args) > 1 {
		configPath := os.Args[1]
		if _, err := os.Stat(configPath); err == nil {
			return configPath, nil
		}
		// If specified but doesn't exist, return error
		return "", fmt.Errorf("specified config file not found: %s", configPath)
	}

	// Priority 2: Current directory
	currentDirConfig := "rpf.yaml"
	if _, err := os.Stat(currentDirConfig); err == nil {
		return currentDirConfig, nil
	}

	// Priority 3: ~/.config/rpf/rpf.yaml
	userConfigPath := expandHomeDir("~/.config/rpf/rpf.yaml")
	if _, err := os.Stat(userConfigPath); err == nil {
		return userConfigPath, nil
	}

	// No config file found
	return "", fmt.Errorf("no config file found. Searched: [command line arg], ./rpf.yaml, ~/.config/rpf/rpf.yaml")
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

	return &config, nil
}

// expandHomeDir expands the tilde (~) in a path to the user's home directory
func expandHomeDir(path string) string {
	if filepath.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err == nil {
			return filepath.Join(home, path[2:])
		}
	}
	return path
}
