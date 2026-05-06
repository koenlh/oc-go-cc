package controlpanel

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"oc-go-cc/internal/daemon"
)

func TestReadLogTailReturnsLastLines(t *testing.T) {
	path := filepath.Join(t.TempDir(), "oc-go-cc.log")
	content := strings.Join([]string{"one", "two", "three", "four", "five"}, "\n")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	got, err := ReadLogTail(path, 1024, 2)
	if err != nil {
		t.Fatalf("ReadLogTail() error = %v", err)
	}
	if got != "four\nfive" {
		t.Fatalf("ReadLogTail() = %q, want %q", got, "four\nfive")
	}
}

func TestClearLogFileTruncatesExistingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "oc-go-cc.log")
	if err := os.WriteFile(path, []byte("warn line\nerror line\n"), 0644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	if err := ClearLogFile(path); err != nil {
		t.Fatalf("ClearLogFile() error = %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if len(data) != 0 {
		t.Fatalf("len(data) = %d, want 0", len(data))
	}
}

func TestStatusSnapshotReportsRunningProcess(t *testing.T) {
	paths := &daemon.Paths{
		PIDFile: filepath.Join(t.TempDir(), "oc-go-cc.pid"),
		LogFile: filepath.Join(t.TempDir(), "oc-go-cc.log"),
	}
	if err := os.WriteFile(paths.PIDFile, []byte("1234"), 0644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	app := &App{
		version:           "test",
		paths:             paths,
		configPath:        filepath.Join(t.TempDir(), "config.json"),
		serviceBinaryPath: `C:\bin\oc-go-cc.exe`,
		getPID:            func(string) (int, error) { return 1234, nil },
		isProcessRunning:  func(int) bool { return true },
	}

	status := app.statusSnapshot("")
	if !status.Running {
		t.Fatal("status.Running = false, want true")
	}
	if status.PID != 1234 {
		t.Fatalf("status.PID = %d, want %d", status.PID, 1234)
	}
}

func TestResolveServiceBinaryPathUsesSiblingBinary(t *testing.T) {
	if _, err := ResolveServiceBinaryPath(`C:\bin\oc-go-cc.exe`); err != nil {
		t.Fatalf("ResolveServiceBinaryPath() with override error = %v", err)
	}
}
