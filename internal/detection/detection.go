package detection

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

// DetectedService represents a detected service
type DetectedService struct {
	Name       string
	Type       string // "web_server", "framework", "database", etc.
	ConfigPath string
	LogPaths   []string
	Running    bool
}

// DetectedEnvironment contains all detected services and logs
type DetectedEnvironment struct {
	Services    []DetectedService
	LogFiles    []LogFile
	IsContainer bool
	IsK8s       bool
	Journald    bool
}

// LogFile represents a discovered log file
type LogFile struct {
	Path            string
	SuggestedFormat string // "nginx", "apache", "django", "json"
	Size            int64
	Readable        bool
	Source          string
}

// DetectEnvironment detects the current environment
func DetectEnvironment() *DetectedEnvironment {
	env := &DetectedEnvironment{
		Services:    []DetectedService{},
		LogFiles:    []LogFile{},
		IsContainer: isRunningInContainer(),
		IsK8s:       isRunningInK8s(),
	}

	// Detect web servers
	env.Services = append(env.Services, detectNginx()...)
	env.Services = append(env.Services, detectApache()...)

	// Detect application frameworks
	env.Services = append(env.Services, detectDjango()...)
	env.Services = append(env.Services, detectNodeJS()...)

	// Discover log files
	env.LogFiles = discoverLogFiles()
	env.Journald = hasJournald()

	return env
}

// detectNginx checks for nginx installation and config
func detectNginx() []DetectedService {
	var services []DetectedService

	// Check if nginx is installed
	if _, err := exec.LookPath("nginx"); err != nil {
		return services
	}

	// Check if nginx is running
	running := false
	if output, err := exec.Command("pgrep", "-x", "nginx").Output(); err == nil && len(output) > 0 {
		running = true
	}

	// Find config file
	configPaths := []string{
		"/etc/nginx/nginx.conf",
		"/usr/local/etc/nginx/nginx.conf",
		"/opt/nginx/conf/nginx.conf",
	}

	var configPath string
	var logPaths []string

	for _, path := range configPaths {
		if _, err := os.Stat(path); err == nil {
			configPath = path
			// Parse config for log paths
			logPaths = parseNginxConfig(path)
			break
		}
	}

	if configPath != "" || running {
		services = append(services, DetectedService{
			Name:       "Nginx",
			Type:       "web_server",
			ConfigPath: configPath,
			LogPaths:   logPaths,
			Running:    running,
		})
	}

	return services
}

// detectApache checks for Apache installation
func detectApache() []DetectedService {
	var services []DetectedService

	// Check for apache/httpd
	apacheBinary := ""
	for _, binary := range []string{"apache2", "httpd"} {
		if _, err := exec.LookPath(binary); err == nil {
			apacheBinary = binary
			break
		}
	}

	if apacheBinary == "" {
		return services
	}

	// Check if running
	running := false
	if output, err := exec.Command("pgrep", "-x", apacheBinary).Output(); err == nil && len(output) > 0 {
		running = true
	}

	// Common log paths
	logPaths := []string{
		"/var/log/apache2/access.log",
		"/var/log/httpd/access_log",
		"/usr/local/var/log/apache2/access_log",
	}

	var existingLogs []string
	for _, path := range logPaths {
		if _, err := os.Stat(path); err == nil {
			existingLogs = append(existingLogs, path)
		}
	}

	services = append(services, DetectedService{
		Name:     "Apache",
		Type:     "web_server",
		LogPaths: existingLogs,
		Running:  running,
	})

	return services
}

// detectDjango checks for Django applications
func detectDjango() []DetectedService {
	var services []DetectedService

	// Look for manage.py (Django marker file)
	managePyPaths := []string{
		"manage.py",
		"../manage.py",
		"../../manage.py",
	}

	for _, path := range managePyPaths {
		if _, err := os.Stat(path); err == nil {
			// Found Django project
			services = append(services, DetectedService{
				Name:    "Django",
				Type:    "framework",
				Running: false, // Can't easily detect if Django is running
			})
			break
		}
	}

	// Check for gunicorn process (common Django deployment)
	if output, err := exec.Command("pgrep", "-f", "gunicorn").Output(); err == nil && len(output) > 0 {
		services = append(services, DetectedService{
			Name:    "Gunicorn (Django)",
			Type:    "app_server",
			Running: true,
		})
	}

	return services
}

// detectNodeJS checks for Node.js applications
func detectNodeJS() []DetectedService {
	var services []DetectedService

	// Look for package.json
	if _, err := os.Stat("package.json"); err == nil {
		services = append(services, DetectedService{
			Name: "Node.js",
			Type: "framework",
		})
	}

	// Check for node processes
	if output, err := exec.Command("pgrep", "-f", "node").Output(); err == nil && len(output) > 0 {
		services = append(services, DetectedService{
			Name:    "Node.js",
			Type:    "runtime",
			Running: true,
		})
	}

	return services
}

// discoverLogFiles finds common log file locations
func discoverLogFiles() []LogFile {
	files := discoverFilesystemLogs()
	containerLogs := discoverContainerLogs()
	files = append(files, containerLogs...)

	if len(files) == 0 {
		return files
	}

	sort.SliceStable(files, func(i, j int) bool {
		if files[i].Source == files[j].Source {
			return files[i].Path < files[j].Path
		}
		if files[i].Source == "" {
			return false
		}
		if files[j].Source == "" {
			return true
		}
		return files[i].Source < files[j].Source
	})

	return files
}

func discoverFilesystemLogs() []LogFile {
	var logFiles []LogFile
	// Common log directories and patterns
	searchPaths := []string{
		"/var/log/nginx/access.log",
		"/var/log/nginx/error.log",
		"/var/log/apache2/access.log",
		"/var/log/httpd/access_log",
		"/var/log/app/*.log",
		"/var/log/*.log",
		"./logs/*.log",
		"./log/*.log",
	}

	for _, pattern := range searchPaths {
		matches, err := filepath.Glob(pattern)
		if err != nil {
			continue
		}

		for _, path := range matches {
			info, err := os.Stat(path)
			if err != nil {
				continue
			}

			// Skip if it's a directory or empty
			if info.IsDir() || info.Size() == 0 {
				continue
			}

			// Check if readable
			readable := true
			if _, err := os.Open(path); err != nil {
				readable = false
			}

			// Suggest format based on path
			format := suggestLogFormat(path)

			logFiles = append(logFiles, LogFile{
				Path:            path,
				SuggestedFormat: format,
				Size:            info.Size(),
				Readable:        readable,
			})
		}
	}

	return logFiles
}

func discoverContainerLogs() []LogFile {
	var logFiles []LogFile

	paths := [][]string{
		mustGlob("/var/lib/docker/containers/*/*-json.log"),
		mustGlob("/var/log/containers/*.log"),
		mustGlob("/var/log/pods/*/*/*/*.log"),
	}

	seen := make(map[string]struct{})
	add := func(file LogFile) {
		clean := filepath.Clean(file.Path)
		if _, exists := seen[clean]; exists {
			return
		}
		seen[clean] = struct{}{}
		logFiles = append(logFiles, file)
	}

	for _, set := range paths {
		for _, path := range set {
			info, err := os.Stat(path)
			if err != nil {
				continue
			}

			if info.IsDir() || info.Size() == 0 {
				continue
			}

			readable := true
			if f, err := os.Open(path); err == nil {
				_ = f.Close()
			} else {
				readable = false
			}

			source, format := containerSourceHint(path)
			add(LogFile{
				Path:            path,
				SuggestedFormat: format,
				Size:            info.Size(),
				Readable:        readable,
				Source:          source,
			})
		}
	}

	return logFiles
}

func containerSourceHint(path string) (string, string) {
	switch {
	case strings.Contains(path, "/var/lib/docker/containers/"):
		return dockerSourceHint(path), "docker"
	case strings.Contains(path, "/var/log/containers/"):
		return kubernetesSymlinkHint(path), "docker"
	case strings.Contains(path, "/var/log/pods/"):
		return kubernetesPodHint(path), "docker"
	default:
		return "container runtime", "docker"
	}
}

func dockerSourceHint(path string) string {
	dir := filepath.Dir(path)
	for _, candidate := range []string{
		filepath.Join(dir, "config.v2.json"),
		filepath.Join(dir, "config.json"),
	} {
		data, err := os.ReadFile(candidate)
		if err != nil {
			continue
		}
		var cfg struct {
			Name   string `json:"Name"`
			Config struct {
				Labels map[string]string `json:"Labels"`
			} `json:"Config"`
		}
		if err := json.Unmarshal(data, &cfg); err != nil {
			continue
		}
		name := strings.Trim(cfg.Name, "/")
		if name == "" {
			name = filepath.Base(dir)
		}
		ns := cfg.Config.Labels["io.kubernetes.pod.namespace"]
		pod := cfg.Config.Labels["io.kubernetes.pod.name"]
		container := cfg.Config.Labels["io.kubernetes.container.name"]
		switch {
		case pod != "" && container != "":
			if ns != "" {
				return pod + " / " + container + " (ns: " + ns + ")"
			}
			return pod + " / " + container
		case container != "":
			return container
		case pod != "":
			return pod
		case name != "":
			return name
		default:
			return filepath.Base(dir)
		}
	}

	id := filepath.Base(path)
	id = strings.TrimSuffix(id, "-json.log")
	if id == "" {
		id = "docker container"
	}
	return id
}

func kubernetesSymlinkHint(path string) string {
	base := filepath.Base(path)
	pod, namespace, container := parseKubernetesFilename(base)
	switch {
	case pod != "" && container != "" && namespace != "":
		return pod + " / " + container + " (ns: " + namespace + ")"
	case pod != "" && container != "":
		return pod + " / " + container
	case pod != "":
		return pod
	default:
		return "kubernetes container"
	}
}

func kubernetesPodHint(path string) string {
	parts := strings.Split(path, string(os.PathSeparator))
	if len(parts) < 6 {
		return kubernetesSymlinkHint(path)
	}
	namespace := parts[len(parts)-4]
	pod := parts[len(parts)-3]
	container := parts[len(parts)-2]
	if namespace != "" && pod != "" && container != "" {
		return pod + " / " + container + " (ns: " + namespace + ")"
	}
	return kubernetesSymlinkHint(path)
}

func parseKubernetesFilename(name string) (pod string, namespace string, container string) {
	base := strings.TrimSuffix(name, ".log")
	segments := strings.Split(base, "_")
	if len(segments) < 3 {
		return "", "", ""
	}
	pod = segments[0]
	namespace = segments[1]
	containerWithID := segments[2]
	if idx := strings.Index(containerWithID, "-"); idx != -1 {
		container = containerWithID[:idx]
	} else {
		container = containerWithID
	}
	return
}

func mustGlob(pattern string) []string {
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return nil
	}
	return matches
}

func hasJournald() bool {
	if _, err := os.Stat("/run/systemd/journal"); err == nil {
		return true
	}
	if _, err := os.Stat("/var/log/journal"); err == nil {
		return true
	}
	return false
}

// suggestLogFormat suggests a parser format based on file path
func suggestLogFormat(path string) string {
	lower := strings.ToLower(path)

	if strings.Contains(lower, "nginx") {
		return "nginx"
	}
	if strings.Contains(lower, "apache") || strings.Contains(lower, "httpd") {
		return "apache"
	}
	if strings.Contains(lower, "django") || strings.Contains(lower, "gunicorn") {
		return "django"
	}
	if strings.Contains(lower, ".json") {
		return "json"
	}

	return "generic"
}

// parseNginxConfig extracts log paths from nginx.conf
func parseNginxConfig(configPath string) []string {
	var logPaths []string

	content, err := os.ReadFile(configPath)
	if err != nil {
		return logPaths
	}

	lines := strings.Split(string(content), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "access_log") {
			parts := strings.Fields(line)
			if len(parts) >= 2 {
				logPath := parts[1]
				logPath = strings.TrimSuffix(logPath, ";")
				if logPath != "off" && !strings.HasPrefix(logPath, "syslog:") {
					logPaths = append(logPaths, logPath)
				}
			}
		}
	}

	return logPaths
}

// isRunningInContainer checks if running inside a container
func isRunningInContainer() bool {
	// Check for .dockerenv file
	if _, err := os.Stat("/.dockerenv"); err == nil {
		return true
	}

	// Check cgroup
	if content, err := os.ReadFile("/proc/1/cgroup"); err == nil {
		if strings.Contains(string(content), "docker") ||
			strings.Contains(string(content), "kubepods") ||
			strings.Contains(string(content), "containerd") {
			return true
		}
	}

	return false
}

// isRunningInK8s checks if running in Kubernetes
func isRunningInK8s() bool {
	// Check for Kubernetes service environment variables
	if os.Getenv("KUBERNETES_SERVICE_HOST") != "" {
		return true
	}

	// Check for service account token
	if _, err := os.Stat("/var/run/secrets/kubernetes.io/serviceaccount/token"); err == nil {
		return true
	}

	return false
}
