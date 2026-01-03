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
	// Get the executable name from the invocation path (without extension)
	execName := filepath.Base(os.Args[0])
	// Remove .exe extension on Windows
	execName = strings.TrimSuffix(execName, ".exe")
	configFileName := execName + ".yaml"

	// Priority 1: Command line argument (first non-flag argument)
	// If provided, it is treated as an explicit config path that must exist
	if len(os.Args) > 1 {
		// Find the first non-flag argument
		var firstArg string
		for _, arg := range os.Args[1:] {
			if !strings.HasPrefix(arg, "-") {
				firstArg = arg
				break
			}
		}
		
		if firstArg != "" {
			if _, err := os.Stat(firstArg); err == nil {
				return firstArg, nil
			}
			// If a config path is specified but doesn't exist, return error
			return "", fmt.Errorf("specified config file not found: %s", firstArg)
		}
	}

	// Priority 2: Current directory
	currentDirConfig := configFileName
	if _, err := os.Stat(currentDirConfig); err == nil {
		return currentDirConfig, nil
	}

	// Priority 3: Directory where the executable is located
	execPath, err := os.Executable()
	if err != nil {
		// If we can't determine the executable path, return error with locations searched so far
		return "", fmt.Errorf("no config file found. Searched: ./%s (could not check executable directory: %v)", configFileName, err)
	}
	execDir := filepath.Dir(execPath)
	execDirConfig := filepath.Join(execDir, configFileName)
	if _, err := os.Stat(execDirConfig); err == nil {
		return execDirConfig, nil
	}

	// No config file found
	return "", fmt.Errorf("no config file found. Searched: ./%s, %s", configFileName, execDirConfig)
}
