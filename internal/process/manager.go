package process

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"
)

type ProcessManager struct {
	executablePath string
	cmd            *exec.Cmd
	mu             sync.RWMutex
	ctx            context.Context
	cancel         context.CancelFunc
	configPath     string
}

type InstanceStatus struct {
	Running     bool      `json:"running"`
	PID         int       `json:"pid,omitempty"`
	Executable  string    `json:"executable"`
	ConfigPath  string    `json:"config_path"`
	StartTime   time.Time `json:"start_time,omitempty"`
	Uptime      string    `json:"uptime,omitempty"`
	AutoStarted bool      `json:"auto_started"`
}

func NewProcessManager(configPath string) *ProcessManager {
	ctx, cancel := context.WithCancel(context.Background())

	absPath := configPath
	if configPath != "" {
		if filepath.IsAbs(configPath) {
			absPath = configPath
		} else {
			absPath, _ = filepath.Abs(configPath)
		}
	}

	return &ProcessManager{
		ctx:        ctx,
		cancel:     cancel,
		configPath: absPath,
	}
}

func (pm *ProcessManager) FindExecutable() (string, error) {
	pm.mu.RLock()
	if pm.executablePath != "" {
		pm.mu.RUnlock()
		return pm.executablePath, nil
	}
	pm.mu.RUnlock()

	execDir, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("failed to get executable directory: %w", err)
	}
	execDir = filepath.Dir(execDir)

	entries, err := os.ReadDir(execDir)
	if err != nil {
		return "", fmt.Errorf("failed to read directory %s: %w", execDir, err)
	}

	var candidates []string
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		name := entry.Name()
		nameLower := strings.ToLower(name)

		if pm.isWangshuProcessName(nameLower) && pm.isExecutable(name) {
			candidates = append(candidates, name)
		}
	}

	if len(candidates) == 0 {
		return "", fmt.Errorf("wangshu executable not found in %s", execDir)
	}

	if len(candidates) == 1 {
		path := filepath.Join(execDir, candidates[0])
		pm.mu.Lock()
		pm.executablePath = path
		pm.mu.Unlock()
		slog.Info("Found wangshu executable", "path", path)
		return path, nil
	}

	for _, name := range candidates {
		if strings.EqualFold(name, "wangshu") || strings.EqualFold(name, "wangshu.exe") ||
			strings.EqualFold(name, "wangshu") || strings.EqualFold(name, "wangshu.exe") {
			path := filepath.Join(execDir, name)
			pm.mu.Lock()
			pm.executablePath = path
			pm.mu.Unlock()
			slog.Info("Found wangshu executable", "path", path)
			return path, nil
		}
	}

	path := filepath.Join(execDir, candidates[0])
	pm.mu.Lock()
	pm.executablePath = path
	pm.mu.Unlock()
	slog.Info("Found wangshu executable", "path", path, "note", "multiple candidates found, using first one")
	return path, nil
}

func (pm *ProcessManager) isExecutable(filename string) bool {
	if runtime.GOOS == "windows" {
		ext := strings.ToLower(filepath.Ext(filename))
		return ext == ".exe"
	}

	ext := filepath.Ext(filename)
	if ext != "" {
		return false
	}

	return true
}

func (pm *ProcessManager) FindRunningProcess() (int, error) {
	if runtime.GOOS == "windows" {
		return pm.findProcessWindows()
	}
	return pm.findProcessUnix()
}

func (pm *ProcessManager) findProcessWindows() (int, error) {
	cmd := exec.Command("tasklist", "/FO", "CSV", "/NH")
	output, err := cmd.Output()
	if err != nil {
		return 0, err
	}

	lines := strings.Split(string(output), "\n")
	for _, line := range lines {
		parts := strings.Split(line, ",")
		if len(parts) < 2 {
			continue
		}

		imageName := strings.Trim(parts[0], "\"")
		imageNameLower := strings.ToLower(imageName)

		if pm.isWangshuProcessName(imageNameLower) {
			pidStr := strings.Trim(parts[1], "\"")
			var pid int
			if _, err := fmt.Sscanf(pidStr, "%d", &pid); err == nil {
				return pid, nil
			}
		}
	}

	return 0, fmt.Errorf("no running wangshu process found")
}

func (pm *ProcessManager) findProcessUnix() (int, error) {
	cmd := exec.Command("ps", "aux")
	output, err := cmd.Output()
	if err != nil {
		return 0, err
	}

	lines := strings.Split(string(output), "\n")
	for _, line := range lines {
		fields := strings.Fields(line)
		if len(fields) < 11 {
			continue
		}

		command := strings.Join(fields[10:], " ")
		commandLower := strings.ToLower(command)

		if pm.isWangshuProcessName(commandLower) && !strings.Contains(commandLower, "wangshu-manager") {
			var pid int
			if _, err := fmt.Sscanf(fields[1], "%d", &pid); err == nil {
				return pid, nil
			}
		}
	}

	return 0, fmt.Errorf("no running wangshu process found")
}

func (pm *ProcessManager) isWangshuProcessName(name string) bool {
	name = strings.ToLower(name)

	basePatterns := []string{
		"wangshu",
		"wangshu.exe",
	}

	for _, pattern := range basePatterns {
		if name == pattern {
			return true
		}
	}

	if strings.HasPrefix(name, "./wangshu") || strings.HasPrefix(name, ".\\wangshu") {
		return true
	}

	if strings.HasPrefix(name, "wangshu-") {
		suffix := strings.TrimPrefix(name, "wangshu-")
		validSuffixes := []string{
			"windows", "linux", "darwin", "freebsd", "openbsd", "netbsd",
			"amd64", "386", "arm", "arm64", "mips", "mips64", "ppc64", "ppc64le", "riscv64", "s390x",
		}
		for _, validSuffix := range validSuffixes {
			if strings.HasPrefix(suffix, validSuffix) {
				return true
			}
		}
	}

	if strings.HasPrefix(name, "wangshu.") {
		suffix := strings.TrimPrefix(name, "wangshu.")
		if strings.HasPrefix(suffix, "v") || strings.HasPrefix(suffix, "1.") || strings.HasPrefix(suffix, "2.") || strings.HasPrefix(suffix, "3.") {
			return true
		}
	}

	return false
}

func (pm *ProcessManager) GetStatus() (*InstanceStatus, error) {
	execPath, err := pm.FindExecutable()
	if err != nil {
		return nil, err
	}

	pm.mu.RLock()
	defer pm.mu.RUnlock()

	status := &InstanceStatus{
		Executable: execPath,
		ConfigPath: pm.configPath,
	}

	pid, err := pm.FindRunningProcess()
	if err == nil {
		status.Running = true
		status.PID = pid

		if pm.cmd != nil && pm.cmd.Process != nil {
			status.StartTime = time.Now()
			status.Uptime = time.Since(status.StartTime).Round(time.Second).String()
		}
	}

	return status, nil
}

func (pm *ProcessManager) Start(autoStarted bool) error {
	execPath, err := pm.FindExecutable()
	if err != nil {
		return err
	}

	pm.mu.Lock()
	defer pm.mu.Unlock()

	if pm.cmd != nil && pm.cmd.Process != nil {
		if pm.isProcessRunning(pm.cmd.Process) {
			return fmt.Errorf("wangshu is already running with PID %d", pm.cmd.Process.Pid)
		}
	}

	args := []string{}
	if pm.configPath != "" {
		args = append(args, pm.configPath)
	}

	pm.cmd = exec.CommandContext(pm.ctx, execPath, args...)
	pm.cmd.Stdout = os.Stdout
	pm.cmd.Stderr = os.Stderr

	if err := pm.cmd.Start(); err != nil {
		return fmt.Errorf("failed to start wangshu: %w", err)
	}

	slog.Info("wangshu process started", "pid", pm.cmd.Process.Pid, "auto_started", autoStarted)

	go func() {
		err := pm.cmd.Wait()
		pm.mu.Lock()
		pm.cmd = nil
		pm.mu.Unlock()
		if err != nil {
			slog.Error("wangshu process exited", "error", err)
		} else {
			slog.Info("wangshu process exited normally")
		}
	}()

	return nil
}

func (pm *ProcessManager) Stop() error {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	if pm.cmd != nil && pm.cmd.Process != nil {
		if pm.isProcessRunning(pm.cmd.Process) {
			pid := pm.cmd.Process.Pid
			if err := pm.terminateProcess(pid); err != nil {
				return fmt.Errorf("failed to stop wangshu process: %w", err)
			}
			slog.Info("wangshu process stopped", "pid", pid)
		}
		pm.cmd = nil
		return nil
	}

	pid, err := pm.FindRunningProcess()
	if err != nil {
		return fmt.Errorf("no running wangshu process found: %w", err)
	}

	if err := pm.terminateProcess(pid); err != nil {
		return fmt.Errorf("failed to stop wangshu process: %w", err)
	}

	slog.Info("wangshu process stopped", "pid", pid)
	return nil
}

func (pm *ProcessManager) Restart() error {
	if err := pm.Stop(); err != nil {
		slog.Warn("Failed to stop wangshu during restart", "error", err)
	}

	time.Sleep(1 * time.Second)

	if err := pm.Start(false); err != nil {
		return fmt.Errorf("failed to start wangshu after restart: %w", err)
	}

	return nil
}

func (pm *ProcessManager) terminateProcess(pid int) error {
	if runtime.GOOS == "windows" {
		cmd := exec.Command("taskkill", "/F", "/PID", fmt.Sprintf("%d", pid))
		return cmd.Run()
	}

	process, err := os.FindProcess(pid)
	if err != nil {
		return err
	}

	return process.Signal(syscall.SIGTERM)
}

func (pm *ProcessManager) isProcessRunning(process *os.Process) bool {
	if process == nil {
		return false
	}

	if runtime.GOOS == "windows" {
		cmd := exec.Command("tasklist", "/FI", fmt.Sprintf("PID eq %d", process.Pid), "/FO", "CSV", "/NH")
		output, err := cmd.Output()
		if err != nil {
			return false
		}

		lines := strings.Split(string(output), "\n")
		for _, line := range lines {
			parts := strings.Split(line, ",")
			if len(parts) >= 2 {
				imageName := strings.Trim(parts[0], "\"")
				pidStr := strings.Trim(parts[1], "\"")
				if strings.Contains(pidStr, fmt.Sprintf("%d", process.Pid)) {
					return pm.isWangshuProcessName(strings.ToLower(imageName))
				}
			}
		}
		return false
	}

	err := process.Signal(syscall.Signal(0))
	return err == nil
}

func (pm *ProcessManager) AutoStartIfNotRunning() error {
	status, err := pm.GetStatus()
	if err != nil {
		return err
	}

	if !status.Running {
		slog.Info("wangshu not running, attempting to auto-start")
		if err := pm.Start(true); err != nil {
			return fmt.Errorf("failed to auto-start wangshu: %w", err)
		}
		slog.Info("wangshu auto-started successfully")
	}

	return nil
}

func (pm *ProcessManager) Shutdown() {
	pm.cancel()
	pm.Stop()
}
