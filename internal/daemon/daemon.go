package daemon

import (
	"errors"
	"fmt"
	"io/ioutil"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"github.com/yaat-app/sidecar/internal/config"
)

// Start starts the sidecar as a daemon process
func Start(configPath, logFilePath, pidPath string, verbose bool) error {
	// Check if already running
	if IsRunning(pidPath) {
		return fmt.Errorf("sidecar is already running (PID file exists: %s)", pidPath)
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
		// Use default log path if not specified
		logPath = "/var/log/yaat-sidecar.log"
		// Create log directory if it doesn't exist
		logDir := filepath.Dir(logPath)
		if err := os.MkdirAll(logDir, 0755); err != nil && !os.IsPermission(err) {
			// If we can't create /var/log, use home directory
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
	// Create PID directory if needed
	os.MkdirAll(filepath.Dir(pidPath), 0755)
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
func Stop(pidPath string) error {
	pid, actualPidPath, err := readPID(pidPath)
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
	os.Remove(actualPidPath)

	return nil
}

// IsRunning checks if the daemon is currently running
func IsRunning(pidPath string) bool {
	pid, _, err := readPID(pidPath)
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

// Uninstall removes YAAT Sidecar from the system
// Returns: (warnings, error)
// warnings: list of non-fatal issues encountered
// error: fatal error that prevented uninstallation (nil if successful)
func Uninstall() ([]string, error) {
	fmt.Println("🧹  Uninstalling YAAT Sidecar...")
	fmt.Println()

	executable, _ := os.Executable()
	var warnings []string

	// Step 1: stop any running processes
	fmt.Print("→ Stopping running processes... ")
	stopped := false
	defaultPidPath := "/var/run/yaat-sidecar.pid"
	if IsRunning(defaultPidPath) {
		if err := Stop(defaultPidPath); err != nil {
			warnings = append(warnings, fmt.Sprintf("stop daemon: %v", err))
		} else {
			stopped = true
		}
	}

	forced, forceErr := stopResidualProcesses()
	if forceErr != nil {
		warnings = append(warnings, fmt.Sprintf("force stop: %v", forceErr))
	}

	switch {
	case stopped:
		fmt.Println("✓")
	case forced:
		fmt.Println("✓ (forced)")
	default:
		fmt.Println("(not running)")
	}

	// Step 2: remove systemd service (Linux-only)
	warnings = append(warnings, removeSystemdUnits()...)

	// Step 3: remove PID files
	warnings = append(warnings, removePathsGroup("PID files", possiblePidFiles(), true)...)

	// Step 4: remove log files
	warnings = append(warnings, removePathsGroup("log files", possibleLogFiles(), true)...)

	// Step 5: remove configuration files
	warnings = append(warnings, removePathsGroup("configuration files", possibleConfigFiles(), true)...)

	// Step 6: remove state and queue directories
	warnings = append(warnings, removePathsGroup("state files", possibleStateFiles(), false)...)
	warnings = append(warnings, removeDirectoriesGroup("queue directories", possibleQueueDirs())...)
	warnings = append(warnings, removeDirectoriesGroup("state directories", possibleStateDirs())...)

	// Step 7: remove binary and symlinks
	warnings = append(warnings, removeBinaryAndLinks(executable)...)

	fmt.Println()
	if len(warnings) > 0 {
		fmt.Println("⚠️  Uninstall completed with warnings:")
		for _, w := range warnings {
			fmt.Printf("   • %s\n", w)
		}
		fmt.Println()
		return warnings, nil
	}

	fmt.Println("✓ Uninstall completed cleanly")
	return warnings, nil
}

func stopResidualProcesses() (bool, error) {
	// Get current process PID to avoid killing ourselves during uninstall
	currentPID := os.Getpid()
	currentPIDStr := strconv.Itoa(currentPID)

	// Use pgrep to find all yaat-sidecar processes
	checkCmd := exec.Command("pgrep", "-f", "yaat-sidecar")
	output, err := checkCmd.Output()
	if err != nil {
		// Exit code 1 means no processes found, which is fine
		if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() == 1 {
			return false, nil
		}
		// pgrep not available or other error - skip force kill
		return false, nil
	}

	// Parse PIDs and kill each one except the current process
	pidsStr := strings.TrimSpace(string(output))
	if pidsStr == "" {
		return false, nil
	}

	pids := strings.Fields(pidsStr)
	killed := false

	for _, pidStr := range pids {
		pidStr = strings.TrimSpace(pidStr)
		if pidStr == "" || pidStr == currentPIDStr {
			continue // Skip empty or current process
		}

		// Verify it's a valid PID
		pid, err := strconv.Atoi(pidStr)
		if err != nil {
			continue
		}

		// Send SIGTERM to this process
		if process, err := os.FindProcess(pid); err == nil {
			if err := process.Signal(syscall.SIGTERM); err == nil {
				killed = true
			}
		}
	}

	return killed, nil
}

func removeSystemdUnits() []string {
	fmt.Print("→ Removing systemd unit... ")
	var warnings []string

	servicePaths := []struct {
		path string
		user bool
	}{
		{"/etc/systemd/system/yaat-sidecar.service", false},
		{"/lib/systemd/system/yaat-sidecar.service", false},
		{"/usr/lib/systemd/system/yaat-sidecar.service", false},
	}

	if home, err := os.UserHomeDir(); err == nil && home != "" {
		servicePaths = append(servicePaths,
			struct {
				path string
				user bool
			}{filepath.Join(home, ".config", "systemd", "user", "yaat-sidecar.service"), true},
			struct {
				path string
				user bool
			}{filepath.Join(home, ".local", "share", "systemd", "user", "yaat-sidecar.service"), true},
		)
	}

	found := false
	for _, candidate := range servicePaths {
		if _, err := os.Stat(candidate.path); err != nil {
			continue
		}
		found = true

		systemctlArgs := []string{"stop", "yaat-sidecar"}
		if candidate.user {
			systemctlArgs = append([]string{"--user"}, systemctlArgs...)
		}
		exec.Command("systemctl", systemctlArgs...).Run()

		systemctlArgs[1] = "disable"
		exec.Command("systemctl", systemctlArgs...).Run()

		if err := os.Remove(candidate.path); err != nil {
			if os.IsPermission(err) {
				warnings = append(warnings, fmt.Sprintf("remove systemd unit %s: permission denied", candidate.path))
			} else {
				warnings = append(warnings, fmt.Sprintf("remove systemd unit %s: %v", candidate.path, err))
			}
		}
	}

	if found {
		exec.Command("systemctl", "daemon-reload").Run()
		if err := exec.Command("systemctl", "--user", "daemon-reload").Run(); err != nil {
			// Ignore errors – user daemon may not be enabled.
		}
	}

	if !found {
		fmt.Println("(not installed)")
		return warnings
	}

	if len(warnings) > 0 {
		fmt.Println("⚠️  Warning")
	} else {
		fmt.Println("✓")
	}
	return warnings
}

func removePathsGroup(label string, paths []string, removeParents bool) []string {
	fmt.Printf("→ Removing %s... ", strings.ToLower(label))
	var warnings []string

	seen := map[string]struct{}{}
	removed := 0

	for _, p := range paths {
		if p == "" {
			continue
		}
		if _, ok := seen[p]; ok {
			continue
		}
		seen[p] = struct{}{}

		if err := os.Remove(p); err != nil {
			if os.IsNotExist(err) {
				continue
			}
			warnings = append(warnings, fmt.Sprintf("remove %s %s: %v", label, p, err))
			continue
		}

		removed++
		if removeParents {
			removeParentDirIfEmpty(filepath.Dir(p))
		}
	}

	if removed > 0 {
		fmt.Printf("✓ (removed %d)\n", removed)
	} else {
		fmt.Println("(none found)")
	}

	return warnings
}

func removeDirectoriesGroup(label string, dirs []string) []string {
	fmt.Printf("→ Removing %s... ", strings.ToLower(label))
	var warnings []string

	seen := map[string]struct{}{}
	removed := 0

	for _, dir := range dirs {
		if dir == "" {
			continue
		}
		if _, ok := seen[dir]; ok {
			continue
		}
		seen[dir] = struct{}{}

		// Check if directory exists
		info, err := os.Stat(dir)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("stat %s %s: %v", label, dir, err))
			continue
		}

		// Skip if not a directory
		if !info.IsDir() {
			continue
		}

		// Remove directory recursively
		if err := os.RemoveAll(dir); err != nil {
			if os.IsPermission(err) {
				warnings = append(warnings, fmt.Sprintf("remove %s %s: permission denied", label, dir))
			} else {
				warnings = append(warnings, fmt.Sprintf("remove %s %s: %v", label, dir, err))
			}
			continue
		}

		removed++
	}

	if removed > 0 {
		fmt.Printf("✓ (removed %d)\n", removed)
	} else {
		fmt.Println("(none found)")
	}

	return warnings
}

func removeBinaryAndLinks(executable string) []string {
	fmt.Print("→ Removing binary... ")
	var warnings []string

	if executable == "" {
		fmt.Println("(path unknown)")
		return []string{"binary path could not be determined"}
	}

	resolved := executable
	if eval, err := filepath.EvalSymlinks(executable); err == nil && eval != "" {
		resolved = eval
	}

	needsSudo, err := binaryNeedsSudo(resolved)
	if err != nil {
		fmt.Println("⚠️  Warning")
		return []string{fmt.Sprintf("inspect binary %s: %v", resolved, err)}
	}

	if needsSudo {
		fmt.Println("requires sudo")
		warnings = append(warnings, fmt.Sprintf("remove binary: sudo rm %s", resolved))
	} else {
		removeErr := os.Remove(resolved)
		switch {
		case removeErr == nil:
			fmt.Println("✓")
		case os.IsNotExist(removeErr):
			fmt.Println("(not found)")
		case isTextFileBusy(removeErr):
			if err := selfDestruct(resolved); err != nil {
				fmt.Println("⚠️  Warning")
				warnings = append(warnings, fmt.Sprintf("schedule binary removal %s: %v", resolved, err))
			} else {
				fmt.Println("✓ (scheduled)")
			}
		case os.IsPermission(removeErr):
			fmt.Println("requires sudo")
			warnings = append(warnings, fmt.Sprintf("remove binary: permission denied for %s", resolved))
		default:
			fmt.Println("⚠️  Warning")
			warnings = append(warnings, fmt.Sprintf("remove binary %s: %v", resolved, removeErr))
		}
	}

	removedLinks := 0
	for _, link := range possibleBinaryLinks() {
		if link == resolved {
			continue
		}

		removed, err := removeIfSymlinkTo(link, resolved)
		if err != nil {
			warnings = append(warnings, err.Error())
			continue
		}
		if removed {
			removedLinks++
		}
	}

	if removedLinks > 0 {
		fmt.Printf("   removed %d symlink(s)\n", removedLinks)
	}

	return warnings
}

func removeParentDirIfEmpty(dir string) {
	if dir == "" || dir == "/" {
		return
	}

	base := strings.ToLower(filepath.Base(dir))
	if base != "yaat" && base != ".yaat" {
		return
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	if len(entries) == 0 {
		os.Remove(dir)
	}
}

func removeIfSymlinkTo(path, target string) (bool, error) {
	info, err := os.Lstat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, fmt.Errorf("inspect symlink %s: %v", path, err)
	}

	if info.Mode()&os.ModeSymlink == 0 {
		return false, nil
	}

	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return false, fmt.Errorf("resolve symlink %s: %v", path, err)
	}

	if resolved != target {
		return false, nil
	}

	if err := os.Remove(path); err != nil {
		if os.IsPermission(err) {
			return false, fmt.Errorf("remove symlink %s: permission denied", path)
		}
		return false, fmt.Errorf("remove symlink %s: %v", path, err)
	}

	return true, nil
}

func possiblePidFiles() []string {
	paths := []string{"/var/run/yaat-sidecar.pid"}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		paths = append(paths, filepath.Join(home, ".yaat", "sidecar.pid"))
	}
	return paths
}

func possibleLogFiles() []string {
	paths := []string{"/var/log/yaat-sidecar.log", "/var/log/yaat/sidecar.log"}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		paths = append(paths, filepath.Join(home, ".yaat", "sidecar.log"))
		paths = append(paths, filepath.Join(home, "Library", "Logs", "yaat-sidecar.log"))
	}
	return paths
}

func possibleConfigFiles() []string {
	var paths []string

	if defaultPath := config.DefaultConfigPath(); defaultPath != "" {
		paths = append(paths, defaultPath)
	}

	paths = append(paths, "yaat.yaml")

	if home, err := os.UserHomeDir(); err == nil && home != "" {
		paths = append(paths,
			filepath.Join(home, ".config", "yaat", "yaat.yaml"),
			filepath.Join(home, "Library", "Application Support", "yaat", "yaat.yaml"),
			filepath.Join(home, ".yaat", "config.yaml"),
		)
	}

	paths = append(paths,
		"/etc/yaat/yaat.yaml",
	)

	return paths
}

func possibleBinaryLinks() []string {
	var paths []string
	paths = append(paths,
		"/usr/local/bin/yaat-sidecar",
		"/usr/bin/yaat-sidecar",
		"/bin/yaat-sidecar",
	)

	if home, err := os.UserHomeDir(); err == nil && home != "" {
		paths = append(paths,
			filepath.Join(home, "bin", "yaat-sidecar"),
			filepath.Join(home, ".local", "bin", "yaat-sidecar"),
		)
	}

	return paths
}

func possibleStateFiles() []string {
	var paths []string
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		paths = append(paths, filepath.Join(home, ".yaat", "state.json"))
	}
	return paths
}

func possibleQueueDirs() []string {
	var paths []string
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		paths = append(paths, filepath.Join(home, ".yaat", "queue"))
	}
	// Check for custom queue directory from environment
	if queueDir := os.Getenv("YAAT_QUEUE_DIR"); queueDir != "" {
		paths = append(paths, queueDir)
	}
	return paths
}

func possibleStateDirs() []string {
	var paths []string

	// Linux state directories
	paths = append(paths,
		"/var/lib/yaat",
		"/var/log/yaat",
	)

	if home, err := os.UserHomeDir(); err == nil && home != "" {
		// User-level state directory
		paths = append(paths, filepath.Join(home, ".yaat"))
	}

	return paths
}

func isTextFileBusy(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "text file busy") || strings.Contains(msg, "resource busy")
}

// binaryNeedsSudo checks if the binary requires sudo to delete
func binaryNeedsSudo(binaryPath string) (bool, error) {
	info, err := os.Stat(binaryPath)
	if err != nil {
		return false, fmt.Errorf("cannot stat binary: %w", err)
	}

	// Get file system info
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		// Can't determine ownership - assume it's safe to try
		return false, nil
	}

	// Check if binary is owned by root (uid 0)
	if stat.Uid == 0 {
		// Binary is owned by root - check if we're running as root
		return os.Geteuid() != 0, nil
	}

	// Check if binary is in a system directory
	systemDirs := []string{"/usr/local/bin", "/usr/bin", "/bin", "/usr/sbin", "/sbin"}
	for _, dir := range systemDirs {
		if filepath.Dir(binaryPath) == dir {
			// In system directory - check if we can write to parent dir
			parentDir := filepath.Dir(binaryPath)
			testFile := filepath.Join(parentDir, ".yaat-test-"+strconv.Itoa(os.Getpid()))
			if err := os.WriteFile(testFile, []byte("test"), 0644); err != nil {
				if os.IsPermission(err) {
					return true, nil
				}
			} else {
				os.Remove(testFile)
			}
		}
	}

	return false, nil
}

// selfDestruct creates a script that deletes the binary and itself
func selfDestruct(binaryPath string) error {
	// Create a temporary cleanup script
	tmpScript := filepath.Join(os.TempDir(), "yaat-cleanup-"+strconv.Itoa(os.Getpid())+".sh")

	// Script that waits for parent process to exit, then removes binary and itself
	// Remove -f flag to see actual errors
	script := fmt.Sprintf(`#!/bin/sh
# Wait for parent process to exit
sleep 0.5

# Remove the binary (without -f to see errors)
rm "%s" 2>/dev/null || {
    echo "Failed to remove binary: %s" >&2
    exit 1
}

# Remove this script
rm "%s" 2>/dev/null

exit 0
`, binaryPath, binaryPath, tmpScript)

	// Write the script
	if err := os.WriteFile(tmpScript, []byte(script), 0755); err != nil {
		return fmt.Errorf("failed to create cleanup script: %w", err)
	}

	// Execute the script in the background
	cmd := exec.Command(tmpScript)
	if err := cmd.Start(); err != nil {
		os.Remove(tmpScript)
		return fmt.Errorf("failed to start cleanup script: %w", err)
	}

	// Detach from the cleanup process
	cmd.Process.Release()

	return nil
}

// GetPidPath returns the path to the PID file
func GetPidPath(expectedPath string) string {
	return getPidFilePath(expectedPath)
}

// GetLogPath returns the actual log file path if it exists, empty string otherwise
func GetLogPath(expectedPath string) string {
	// Check expected path first
	if _, err := os.Stat(expectedPath); err == nil {
		return expectedPath
	}

	// Check user home fallback
	if home, err := os.UserHomeDir(); err == nil {
		homePath := filepath.Join(home, ".yaat", "sidecar.log")
		if _, err := os.Stat(homePath); err == nil {
			return homePath
		}
	}

	return "" // No log file exists
}

// GetExpectedLogPath returns where logs will be written (even if file doesn't exist yet)
func GetExpectedLogPath(expectedPath string) string {
	// Check if directory for expected path exists (writable)
	dir := filepath.Dir(expectedPath)
	if _, err := os.Stat(dir); err == nil {
		return expectedPath
	}

	// Fallback to user home
	if home, err := os.UserHomeDir(); err == nil {
		return filepath.Join(home, ".yaat", "sidecar.log")
	}

	return expectedPath // Last resort
}

// Helper functions

func writePidFile(path string, pid int) error {
	return ioutil.WriteFile(path, []byte(strconv.Itoa(pid)), 0644)
}

func getPidFilePath(expectedPath string) string {
	// Check expected path first
	if _, err := os.Stat(expectedPath); err == nil {
		return expectedPath
	}
	// Check home directory fallback
	home, _ := os.UserHomeDir()
	if home == "" {
		return expectedPath
	}
	userPid := filepath.Join(home, ".yaat", "sidecar.pid")
	if _, err := os.Stat(userPid); err == nil {
		return userPid
	}
	return expectedPath
}

func readPID(expectedPath string) (int, string, error) {
	pidPath := getPidFilePath(expectedPath)

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
