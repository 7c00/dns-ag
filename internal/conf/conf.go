package conf

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// GetConfigFile locates the configuration file using the following rules:
// 1. If a command-line argument is provided, it is treated as an explicit config
//    path that must exist. If that file does not exist, an error is returned
//    immediately and no other locations are searched.
// 2. If no command-line argument is provided, ./EXEC.yaml is checked (current
//    directory, where EXEC is the binary name), then <binary_dir>/EXEC.yaml
//    (directory where the executable is located). If none of these files exist,
//    an error is returned.
func GetConfigFile() (string, error) {
	// Get the executable name (without extension)
	execName := filepath.Base(os.Args[0])
	// Remove .exe extension on Windows
	execName = strings.TrimSuffix(execName, ".exe")
	configFileName := execName + ".yaml"

	// Priority 1: Command line argument (first non-flag argument)
	if len(os.Args) > 1 {
		for _, arg := range os.Args[1:] {
			// Skip flag-like arguments (e.g., -h, --help)
			if strings.HasPrefix(arg, "-") {
				continue
			}
			configPath := arg
			if _, err := os.Stat(configPath); err == nil {
				return configPath, nil
			}
			// If a config path is specified but doesn't exist, return error
			return "", fmt.Errorf("specified config file not found: %s", configPath)
		}
	}

	// Priority 2: Current directory
	currentDirConfig := configFileName
	if _, err := os.Stat(currentDirConfig); err == nil {
		return currentDirConfig, nil
	}

	// Priority 3: Directory where the executable is located
	execPath, err := os.Executable()
	if err == nil {
		execDir := filepath.Dir(execPath)
		execDirConfig := filepath.Join(execDir, configFileName)
		if _, err := os.Stat(execDirConfig); err == nil {
			return execDirConfig, nil
		}
	}

	// No config file found
	return "", fmt.Errorf("no config file found. Searched: ./%s, <exec_dir>/%s", configFileName, configFileName)
}
