package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDefaultConfigDirForWindows(t *testing.T) {
	got := defaultConfigDirFor("windows", `C:\Users\tick1\AppData\Roaming`, `C:\Users\tick1`, func(string) bool { return false })
	want := filepath.Join(`C:\Users\tick1\AppData\Roaming`, "oc-go-cc")
	if got != want {
		t.Fatalf("defaultConfigDirFor(windows) = %q, want %q", got, want)
	}
}

func TestDefaultConfigDirForWindowsPrefersLegacyDotConfigIfPresent(t *testing.T) {
	homeDir := t.TempDir()
	legacyDir := filepath.Join(homeDir, ".config", "oc-go-cc")
	if err := os.MkdirAll(legacyDir, 0755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}

	got := defaultConfigDirFor("windows", filepath.Join(homeDir, "AppData", "Roaming"), homeDir, dirExists)
	if got != legacyDir {
		t.Fatalf("defaultConfigDirFor(windows, legacy exists) = %q, want %q", got, legacyDir)
	}
}

func TestDefaultConfigDirForUnixPreservesDotConfig(t *testing.T) {
	got := defaultConfigDirFor("linux", `/home/tick1/.config`, `/home/tick1`, func(string) bool { return false })
	want := filepath.Join(`/home/tick1`, ".config", "oc-go-cc")
	if got != want {
		t.Fatalf("defaultConfigDirFor(linux) = %q, want %q", got, want)
	}
}