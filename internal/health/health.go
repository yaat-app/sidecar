package health

import (
	"encoding/json"
	"fmt"
	"net/http"
	"runtime"
	"time"
)

// Health provides a health check HTTP endpoint
type Health struct {
	port        int
	version     string
	serviceName string
	startTime   time.Time
}

// HealthResponse is the JSON response from the health endpoint
type HealthResponse struct {
	Status      string            `json:"status"`
	Version     string            `json:"version"`
	ServiceName string            `json:"service_name"`
	Uptime      string            `json:"uptime"`
	Platform    string            `json:"platform"`
	GoVersion   string            `json:"go_version"`
	Memory      *MemoryStats      `json:"memory,omitempty"`
	Timestamp   string            `json:"timestamp"`
}

// MemoryStats contains memory usage statistics
type MemoryStats struct {
	Alloc      uint64 `json:"alloc_mb"`
	TotalAlloc uint64 `json:"total_alloc_mb"`
	Sys        uint64 `json:"sys_mb"`
	NumGC      uint32 `json:"num_gc"`
}

// New creates a new health check service
func New(port int, version, serviceName string) *Health {
	return &Health{
		port:        port,
		version:     version,
		serviceName: serviceName,
		startTime:   time.Now(),
	}
}

// Start starts the health check HTTP server
func (h *Health) Start() error {
	mux := http.NewServeMux()
	mux.HandleFunc("/health", h.handleHealth)
	mux.HandleFunc("/", h.handleHealth) // Also respond on root

	addr := fmt.Sprintf(":%d", h.port)
	return http.ListenAndServe(addr, mux)
}

// handleHealth handles health check requests
func (h *Health) handleHealth(w http.ResponseWriter, r *http.Request) {
	// Only allow GET requests
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Get memory stats
	var memStats runtime.MemStats
	runtime.ReadMemStats(&memStats)

	response := HealthResponse{
		Status:      "ok",
		Version:     h.version,
		ServiceName: h.serviceName,
		Uptime:      time.Since(h.startTime).String(),
		Platform:    fmt.Sprintf("%s/%s", runtime.GOOS, runtime.GOARCH),
		GoVersion:   runtime.Version(),
		Memory: &MemoryStats{
			Alloc:      memStats.Alloc / 1024 / 1024,
			TotalAlloc: memStats.TotalAlloc / 1024 / 1024,
			Sys:        memStats.Sys / 1024 / 1024,
			NumGC:      memStats.NumGC,
		},
		Timestamp: time.Now().UTC().Format(time.RFC3339),
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(response)
}
