package health

import (
	"encoding/json"
	"fmt"
	"net/http"
	"runtime"
	"strings"
	"time"

	"github.com/yaat-app/sidecar/internal/diag"
)

// Health provides a health check HTTP endpoint
type Health struct {
	port        int
	version     string
	serviceName string
	startTime   time.Time
	snapshotFn  func() diag.Snapshot
}

// HealthResponse is the JSON response from the health endpoint
type HealthResponse struct {
	Status      string         `json:"status"`
	Version     string         `json:"version"`
	ServiceName string         `json:"service_name"`
	Uptime      string         `json:"uptime"`
	Platform    string         `json:"platform"`
	GoVersion   string         `json:"go_version"`
	Memory      *MemoryStats   `json:"memory,omitempty"`
	Timestamp   string         `json:"timestamp"`
	Diagnostics *diag.Snapshot `json:"diagnostics,omitempty"`
}

// MemoryStats contains memory usage statistics
type MemoryStats struct {
	Alloc      uint64 `json:"alloc_mb"`
	TotalAlloc uint64 `json:"total_alloc_mb"`
	Sys        uint64 `json:"sys_mb"`
	NumGC      uint32 `json:"num_gc"`
}

// New creates a new health check service
func New(port int, version, serviceName string, snapshotFn func() diag.Snapshot) *Health {
	return &Health{
		port:        port,
		version:     version,
		serviceName: serviceName,
		startTime:   time.Now(),
		snapshotFn:  snapshotFn,
	}
}

// Start starts the health check HTTP server
func (h *Health) Start() error {
	mux := http.NewServeMux()
	mux.HandleFunc("/health", h.handleHealth)
	mux.HandleFunc("/", h.handleHealth) // Also respond on root
	mux.HandleFunc("/metrics", h.handleMetrics)

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
	if h.snapshotFn != nil {
		snapshot := h.snapshotFn()
		response.Diagnostics = &snapshot
		if snapshot.LastError != "" {
			response.Status = "degraded"
		}
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(response)
}

func (h *Health) handleMetrics(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var snapshot diag.Snapshot
	if h.snapshotFn != nil {
		snapshot = h.snapshotFn()
	}

	w.Header().Set("Content-Type", "text/plain; version=0.0.4")

	fmt.Fprintf(w, "yaat_sidecar_queue_inmemory %d\n", snapshot.InMemoryQueue)
	fmt.Fprintf(w, "yaat_sidecar_queue_persisted %d\n", snapshot.PersistedQueue)
	fmt.Fprintf(w, "yaat_sidecar_queue_deadletter %d\n", snapshot.DeadLetterQueue)
	fmt.Fprintf(w, "yaat_sidecar_events_sent_total %d\n", snapshot.TotalEventsSent)
	fmt.Fprintf(w, "yaat_sidecar_events_failed_total %d\n", snapshot.TotalEventsFailed)
	fmt.Fprintf(w, "yaat_sidecar_throughput_per_min %.2f\n", snapshot.ThroughputPerMin)
	if snapshot.LastError != "" {
		fmt.Fprintf(w, "yaat_sidecar_last_error{message=\"%s\"} 1\n", escapeLabel(snapshot.LastError))
	} else {
		fmt.Fprintf(w, "yaat_sidecar_last_error 0\n")
	}
}

func escapeLabel(value string) string {
	value = strings.ReplaceAll(value, "\\", "\\\\")
	value = strings.ReplaceAll(value, "\"", "\\\"")
	value = strings.ReplaceAll(value, "\n", "\\n")
	return value
}
