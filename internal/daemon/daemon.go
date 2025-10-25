package daemon

import (
	"fmt"
	"io/ioutil"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"syscall"
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
	pidPath := getPidFilePath()

	// Read PID from file
	pidBytes, err := ioutil.ReadFile(pidPath)
	if err != nil {
		return fmt.Errorf("failed to read PID file: %w", err)
	}

	pid, err := strconv.Atoi(string(pidBytes))
	if err != nil {
		return fmt.Errorf("invalid PID in file: %w", err)
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
	pidPath := getPidFilePath()

	// Check if PID file exists
	if _, err := os.Stat(pidPath); os.IsNotExist(err) {
		return false
	}

	// Read PID
	pidBytes, err := ioutil.ReadFile(pidPath)
	if err != nil {
		return false
	}

	pid, err := strconv.Atoi(string(pidBytes))
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
	os.Remove(getPidFilePath())

	// Remove log file
	os.Remove(GetLogPath())

	// Remove config file if in default location
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
	pidPath := pidFile
	if _, err := os.Stat("/var/run"); os.IsNotExist(err) || os.IsPermission(err) {
		home, _ := os.UserHomeDir()
		pidPath = filepath.Join(home, ".yaat", "sidecar.pid")
	}
	return pidPath
}
