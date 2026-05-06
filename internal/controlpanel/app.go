package controlpanel

import (
	"bytes"
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"oc-go-cc/internal/config"
	"oc-go-cc/internal/daemon"
)

//go:embed assets/index.html
var assets embed.FS

const (
	defaultLogTailBytes = 256 * 1024
	defaultLogTailLines = 200
	maxLogTailLines     = 1000
)

type App struct {
	version           string
	paths             *daemon.Paths
	configPath        string
	serviceBinaryPath string
	servicePort       int
	startBackground   func(daemon.BackgroundOpts) error
	stopProcess       func(int) error
	getPID            func(string) (int, error)
	isProcessRunning  func(int) bool
	quit              func()
	mu                sync.Mutex
	lastAccess        time.Time
}

type ServiceStatus struct {
	Version       string `json:"version"`
	Running       bool   `json:"running"`
	PID           int    `json:"pid,omitempty"`
	ConfigPath    string `json:"configPath"`
	ConfigExists  bool   `json:"configExists"`
	LogFile       string `json:"logFile"`
	LogExists     bool   `json:"logExists"`
	PIDFile       string `json:"pidFile"`
	ServiceBinary string `json:"serviceBinary"`
	ServiceURL    string `json:"serviceUrl"`
	Message       string `json:"message,omitempty"`
}

type logsResponse struct {
	Path      string    `json:"path"`
	Content   string    `json:"content"`
	UpdatedAt time.Time `json:"updatedAt"`
}

type configSummary struct {
	Host string `json:"host"`
	Port int    `json:"port"`
}

func New(version, serviceBinaryPath, configPath string, servicePort int) (*App, error) {
	paths, err := daemon.DefaultPaths()
	if err != nil {
		return nil, err
	}

	resolvedBinary, err := ResolveServiceBinaryPath(serviceBinaryPath)
	if err != nil {
		return nil, err
	}

	if configPath == "" {
		configPath = config.DefaultConfigPath()
	} else {
		configPath, err = filepath.Abs(configPath)
		if err != nil {
			return nil, fmt.Errorf("cannot resolve config path: %w", err)
		}
	}

	return &App{
		version:           version,
		paths:             paths,
		configPath:        configPath,
		serviceBinaryPath: resolvedBinary,
		servicePort:       servicePort,
		startBackground:   daemon.ForkIntoBackground,
		stopProcess:       daemon.StopProcess,
		getPID:            daemon.GetPID,
		isProcessRunning:  daemon.IsProcessRunning,
		lastAccess:        time.Now(),
	}, nil
}

func ResolveServiceBinaryPath(override string) (string, error) {
	if override != "" {
		resolved, err := filepath.Abs(override)
		if err != nil {
			return "", fmt.Errorf("cannot resolve service binary path: %w", err)
		}
		return resolved, nil
	}

	execPath, err := os.Executable()
	if err == nil {
		dir := filepath.Dir(execPath)
		candidate := filepath.Join(dir, daemon.AppName+binaryExt())
		if _, statErr := os.Stat(candidate); statErr == nil {
			return candidate, nil
		}
	}

	lookedUp, err := exec.LookPath(daemon.AppName + binaryExt())
	if err == nil {
		return lookedUp, nil
	}

	lookedUp, err = exec.LookPath(daemon.AppName)
	if err == nil {
		return lookedUp, nil
	}

	return "", fmt.Errorf("cannot find %s binary; place it next to the UI app or pass --service-binary", daemon.AppName)
}

func (a *App) SetQuitFunc(quit func()) {
	a.quit = quit
}

func (a *App) LastAccess() time.Time {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.lastAccess
}

func (a *App) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/", a.handleIndex)
	mux.HandleFunc("/api/status", a.handleStatus)
	mux.HandleFunc("/api/start", a.handleStart)
	mux.HandleFunc("/api/stop", a.handleStop)
	mux.HandleFunc("/api/logs", a.handleLogs)
	mux.HandleFunc("/api/logs/clear", a.handleClearLogs)
	mux.HandleFunc("/api/quit", a.handleQuit)
	return a.trackAccess(mux)
}

func (a *App) trackAccess(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		a.mu.Lock()
		a.lastAccess = time.Now()
		a.mu.Unlock()
		next.ServeHTTP(w, r)
	})
}

func (a *App) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}

	body, err := assets.ReadFile("assets/index.html")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(body)
}

func (a *App) handleStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	a.writeJSON(w, http.StatusOK, a.statusSnapshot(""))
}

func (a *App) handleStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	status := a.statusSnapshot("")
	if status.Running {
		a.writeJSON(w, http.StatusOK, status)
		return
	}

	err := a.startBackground(daemon.BackgroundOpts{
		ConfigPath: a.configPath,
		Port:       a.servicePort,
		BinaryPath: a.serviceBinaryPath,
	})
	if err != nil {
		a.writeError(w, http.StatusBadGateway, err)
		return
	}

	status = a.waitForStatus(3*time.Second, true)
	if !status.Running {
		status.Message = "launch requested; if the service does not stay up, check the log panel"
	}
	a.writeJSON(w, http.StatusOK, status)
}

func (a *App) handleStop(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	pid, err := a.getPID(a.paths.PIDFile)
	if err != nil {
		a.writeJSON(w, http.StatusOK, a.statusSnapshot("service is not running"))
		return
	}

	if !a.isProcessRunning(pid) {
		_ = os.Remove(a.paths.PIDFile)
		a.writeJSON(w, http.StatusOK, a.statusSnapshot("service was not running; removed stale PID file"))
		return
	}

	if err := a.stopProcess(pid); err != nil {
		a.writeError(w, http.StatusBadGateway, err)
		return
	}
	_ = os.Remove(a.paths.PIDFile)

	status := a.waitForStatus(2*time.Second, false)
	status.Message = "service stopped"
	a.writeJSON(w, http.StatusOK, status)
}

func (a *App) handleLogs(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	lines := defaultLogTailLines
	if raw := r.URL.Query().Get("lines"); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 {
			if parsed > maxLogTailLines {
				parsed = maxLogTailLines
			}
			lines = parsed
		}
	}

	content, err := ReadLogTail(a.paths.LogFile, defaultLogTailBytes, lines)
	if err != nil {
		if os.IsNotExist(err) {
			content = ""
		} else {
			a.writeError(w, http.StatusBadGateway, err)
			return
		}
	}

	a.writeJSON(w, http.StatusOK, logsResponse{
		Path:      a.paths.LogFile,
		Content:   content,
		UpdatedAt: time.Now(),
	})
}

func (a *App) handleClearLogs(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if err := ClearLogFile(a.paths.LogFile); err != nil {
		a.writeError(w, http.StatusBadGateway, err)
		return
	}

	a.writeJSON(w, http.StatusOK, logsResponse{
		Path:      a.paths.LogFile,
		Content:   "",
		UpdatedAt: time.Now(),
	})
}

func (a *App) handleQuit(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	a.writeJSON(w, http.StatusOK, map[string]string{"status": "shutting down"})
	if a.quit != nil {
		go a.quit()
	}
}

func (a *App) waitForStatus(timeout time.Duration, running bool) ServiceStatus {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		status := a.statusSnapshot("")
		if status.Running == running {
			return status
		}
		time.Sleep(150 * time.Millisecond)
	}
	return a.statusSnapshot("")
}

func (a *App) statusSnapshot(message string) ServiceStatus {
	host, port := readConfiguredAddress(a.configPath)
	status := ServiceStatus{
		Version:       a.version,
		ConfigPath:    a.configPath,
		ConfigExists:  fileExists(a.configPath),
		LogFile:       a.paths.LogFile,
		LogExists:     fileExists(a.paths.LogFile),
		PIDFile:       a.paths.PIDFile,
		ServiceBinary: a.serviceBinaryPath,
		ServiceURL:    fmt.Sprintf("http://%s:%d", host, port),
		Message:       message,
	}

	pid, err := a.getPID(a.paths.PIDFile)
	if err != nil {
		return status
	}
	if !a.isProcessRunning(pid) {
		_ = os.Remove(a.paths.PIDFile)
		status.Message = "removed stale PID file"
		return status
	}

	status.Running = true
	status.PID = pid
	return status
}

func (a *App) writeJSON(w http.ResponseWriter, statusCode int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	_ = json.NewEncoder(w).Encode(payload)
}

func (a *App) writeError(w http.ResponseWriter, statusCode int, err error) {
	a.writeJSON(w, statusCode, map[string]string{"error": err.Error()})
}

func ReadLogTail(path string, maxBytes, maxLines int) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer func() { _ = file.Close() }()

	info, err := file.Stat()
	if err != nil {
		return "", err
	}

	start := int64(0)
	if info.Size() > int64(maxBytes) {
		start = info.Size() - int64(maxBytes)
	}
	if _, err := file.Seek(start, io.SeekStart); err != nil {
		return "", err
	}

	data, err := io.ReadAll(file)
	if err != nil {
		return "", err
	}
	if start > 0 {
		if idx := bytes.IndexByte(data, '\n'); idx >= 0 && idx+1 < len(data) {
			data = data[idx+1:]
		}
	}

	lines := strings.Split(string(data), "\n")
	if len(lines) > maxLines {
		lines = lines[len(lines)-maxLines:]
	}
	return strings.TrimLeft(strings.Join(lines, "\n"), "\n"), nil
}

func ClearLogFile(path string) error {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	return file.Close()
}

func readConfiguredAddress(path string) (string, int) {
	host := "127.0.0.1"
	port := 3456

	data, err := os.ReadFile(path)
	if err != nil {
		return host, port
	}

	var summary configSummary
	if err := json.Unmarshal(data, &summary); err != nil {
		return host, port
	}
	if strings.TrimSpace(summary.Host) != "" {
		host = summary.Host
	}
	if summary.Port != 0 {
		port = summary.Port
	}
	return host, port
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func binaryExt() string {
	if runtime.GOOS == "windows" {
		return ".exe"
	}
	return ""
}

func OpenBrowser(url string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	case "darwin":
		cmd = exec.Command("open", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	return cmd.Start()
}

func WithShutdownTimeout(parent context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(parent, timeout)
}
