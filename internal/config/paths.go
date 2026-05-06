package config

import (
	"os"
	"path/filepath"
	"runtime"
)

const appConfigDirName = "oc-go-cc"

// DefaultConfigDir returns the default per-user config directory.
// Windows uses %APPDATA%\oc-go-cc, while other platforms preserve ~/.config/oc-go-cc.
func DefaultConfigDir() string {
	userConfigDir, _ := os.UserConfigDir()
	homeDir, _ := os.UserHomeDir()
	return defaultConfigDirFor(runtime.GOOS, userConfigDir, homeDir, dirExists)
}

// DefaultConfigPath returns the default config.json path.
func DefaultConfigPath() string {
	return filepath.Join(DefaultConfigDir(), "config.json")
}

func defaultConfigDirFor(goos, userConfigDir, homeDir string, exists func(string) bool) string {
	legacyDir := filepath.Join(homeDir, ".config", appConfigDirName)
	if goos == "windows" && homeDir != "" && exists != nil && exists(legacyDir) {
		return legacyDir
	}
	if goos == "windows" && userConfigDir != "" {
		return filepath.Join(userConfigDir, appConfigDirName)
	}
	if homeDir != "" {
		return legacyDir
	}
	if userConfigDir != "" {
		return filepath.Join(userConfigDir, appConfigDirName)
	}
	return filepath.Join(".", appConfigDirName)
}

func dirExists(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return info.IsDir()
}