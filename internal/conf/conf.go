package conf

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// GetConfigFile searches for the configuration file in order of priority:
// 1. Command line argument (if provided)
// 2. ./EXEC.yaml (current directory, where EXEC is the binary name)
// 3. <binary_dir>/EXEC.yaml (directory where the executable is located)
// Returns the path to the first existing config file, or an error if none found
func GetConfigFile() (string, error) {
	// Get the executable name (without extension)
	execName := filepath.Base(os.Args[0])
	// Remove .exe extension on Windows
	execName = strings.TrimSuffix(execName, ".exe")
	configFileName := execName + ".yaml"

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
	return "", fmt.Errorf("no config file found. Searched: [command line arg], ./%s, <exec_dir>/%s", configFileName, configFileName)
}
