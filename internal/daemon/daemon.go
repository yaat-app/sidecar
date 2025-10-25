package daemon

import (
	"errors"
	"fmt"
	"io/ioutil"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"

	"github.com/yaat/sidecar/internal/config"
)

const (
	pidFile = "/var/run/yaat-sidecar.pid"
	logDir  = "/var/log/yaat"
	logFile = "/var/log/yaat/sidecar.log"
)

// Start starts the sidecar as a daemon process
func Start(configPath, logFilePath string, verbose bool) error {
	// Check if already running
	if IsRunning() {
		return fmt.Errorf("sidecar is already running (PID file exists: %s)", pidFile)
	}

	// Get current executable path
	executable, err := os.Executable()
	if err != nil {
		return fmt.Errorf("failed to get executable path: %w", err)
	}

	// Build command args
	args := []string{"--config", configPath}
	if verbose {
		args = append(args, "--verbose")
	}

	// Determine log file path
	logPath := logFilePath
	if logPath == "" {
		logPath = logFile
		// Create log directory if it doesn't exist
		if err := os.MkdirAll(logDir, 0755); err != nil && !os.IsPermission(err) {
			// If we can't create /var/log/yaat, use home directory
			home, _ := os.UserHomeDir()
			logPath = filepath.Join(home, ".yaat", "sidecar.log")
			os.MkdirAll(filepath.Dir(logPath), 0755)
		}
	}
	args = append(args, "--log-file", logPath)

	// Create the command
	cmd := exec.Command(executable, args...)

	// Detach from parent process
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setsid: true, // Create new session
	}

	// Start the process
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start daemon: %w", err)
	}

	// Write PID file
	pidPath := pidFile
	if err := writePidFile(pidPath, cmd.Process.Pid); err != nil {
		// Try user home directory if /var/run is not writable
		home, _ := os.UserHomeDir()
		pidPath = filepath.Join(home, ".yaat", "sidecar.pid")
		os.MkdirAll(filepath.Dir(pidPath), 0755)
		if err := writePidFile(pidPath, cmd.Process.Pid); err != nil {
			return fmt.Errorf("failed to write PID file: %w", err)
		}
	}

	// Release the process so it runs independently
	cmd.Process.Release()

	return nil
}

// Stop stops the daemon process
func Stop() error {
	pid, pidPath, err := readPID()
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("sidecar is not running")
		}
		return err
	}

	// Find the process
	process, err := os.FindProcess(pid)
	if err != nil {
		return fmt.Errorf("failed to find process: %w", err)
	}

	// Send SIGTERM
	if err := process.Signal(syscall.SIGTERM); err != nil {
		return fmt.Errorf("failed to stop process: %w", err)
	}

	// Remove PID file
	os.Remove(pidPath)

	return nil
}

// IsRunning checks if the daemon is currently running
func IsRunning() bool {
	pid, _, err := readPID()
	if err != nil {
		return false
	}

	// Check if process is actually running
	process, err := os.FindProcess(pid)
	if err != nil {
		return false
	}

	// Send signal 0 to check if process exists
	err = process.Signal(syscall.Signal(0))
	return err == nil
}

// Uninstall removes the sidecar from the system
func Uninstall() error {
	// Stop daemon if running
	if IsRunning() {
		if err := Stop(); err != nil {
			fmt.Printf("Warning: failed to stop daemon: %v\n", err)
		}
	}

	// Remove binary
	executable, err := os.Executable()
	if err == nil {
		os.Remove(executable)
	}

	// Remove PID file
	pidPath := GetPidPath()
	os.Remove(pidPath)
	if dir := filepath.Dir(pidPath); dir != "." && dir != "/" {
		os.Remove(dir)
	}

	// Remove log file
	logPath := GetLogPath()
	os.Remove(logPath)
	os.RemoveAll(filepath.Dir(logPath))

	// Remove config file if in default location
	defaultConfig := config.DefaultConfigPath()
	os.Remove(defaultConfig)
	if dir := filepath.Dir(defaultConfig); dir != "." && dir != "/" {
		// Remove directory if empty
		os.Remove(dir)
	}
	os.Remove("yaat.yaml")

	// On macOS, remove launchd plist
	if runtime.GOOS == "darwin" {
		home, _ := os.UserHomeDir()
		plistPath := filepath.Join(home, "Library/LaunchAgents/io.yaat.sidecar.plist")
		os.Remove(plistPath)
		exec.Command("launchctl", "unload", plistPath).Run()
	}

	// On Linux, remove systemd service
	if runtime.GOOS == "linux" {
		servicePath := "/etc/systemd/system/yaat-sidecar.service"
		exec.Command("systemctl", "stop", "yaat-sidecar").Run()
		exec.Command("systemctl", "disable", "yaat-sidecar").Run()
		os.Remove(servicePath)
		exec.Command("systemctl", "daemon-reload").Run()
	}

	return nil
}

// GetPidPath returns the path to the PID file
func GetPidPath() string {
	return getPidFilePath()
}

// GetLogPath returns the path to the log file
func GetLogPath() string {
	logPath := logFile
	if _, err := os.Stat(logDir); os.IsNotExist(err) {
		home, _ := os.UserHomeDir()
		logPath = filepath.Join(home, ".yaat", "sidecar.log")
	}
	return logPath
}

// Helper functions

func writePidFile(path string, pid int) error {
	return ioutil.WriteFile(path, []byte(strconv.Itoa(pid)), 0644)
}

func getPidFilePath() string {
	if _, err := os.Stat(pidFile); err == nil {
		return pidFile
	}
	home, _ := os.UserHomeDir()
	if home == "" {
		return pidFile
	}
	userPid := filepath.Join(home, ".yaat", "sidecar.pid")
	if _, err := os.Stat(userPid); err == nil {
		return userPid
	}
	return userPid
}

func readPID() (int, string, error) {
	pidPath := getPidFilePath()

	pidBytes, err := ioutil.ReadFile(pidPath)
	if err != nil {
		return 0, pidPath, fmt.Errorf("failed to read PID file: %w", err)
	}

	pid, err := strconv.Atoi(strings.TrimSpace(string(pidBytes)))
	if err != nil {
		return 0, pidPath, fmt.Errorf("invalid PID in file: %w", err)
	}
	return pid, pidPath, nil
}
