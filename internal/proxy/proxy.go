package proxy

import (
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"time"

	"github.com/google/uuid"
	"github.com/yaat/sidecar/internal/buffer"
)

// Proxy is an HTTP reverse proxy that captures requests/responses
type Proxy struct {
	listenPort  int
	upstreamURL *url.URL
	serviceName string
	environment string
	buffer      *buffer.Buffer
}

// New creates a new Proxy
func New(listenPort int, upstreamURL, serviceName, environment string, buf *buffer.Buffer) (*Proxy, error) {
	upstream, err := url.Parse(upstreamURL)
	if err != nil {
		return nil, fmt.Errorf("invalid upstream URL: %w", err)
	}

	return &Proxy{
		listenPort:  listenPort,
		upstreamURL: upstream,
		serviceName: serviceName,
		environment: environment,
		buffer:      buf,
	}, nil
}

// Start starts the HTTP proxy server
func (p *Proxy) Start() error {
	addr := fmt.Sprintf(":%d", p.listenPort)
	log.Printf("[Proxy] Starting HTTP proxy on %s -> %s", addr, p.upstreamURL.String())

	// Create HTTP server with custom handler
	server := &http.Server{
		Addr:         addr,
		Handler:      http.HandlerFunc(p.handleRequest),
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 30 * time.Second,
	}

	return server.ListenAndServe()
}

// handleRequest handles an HTTP request
func (p *Proxy) handleRequest(w http.ResponseWriter, r *http.Request) {
	// Generate trace and span IDs
	traceID := uuid.New().String()
	spanID := uuid.New().String()

	// Record start time
	startTime := time.Now()

	// Create upstream request
	upstreamReq, err := http.NewRequest(r.Method, p.upstreamURL.String()+r.RequestURI, r.Body)
	if err != nil {
		log.Printf("[Proxy] Failed to create upstream request: %v", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	// Copy headers
	for key, values := range r.Header {
		for _, value := range values {
			upstreamReq.Header.Add(key, value)
		}
	}

	// Add tracing headers
	upstreamReq.Header.Set("X-Trace-Id", traceID)
	upstreamReq.Header.Set("X-Span-Id", spanID)

	// Send request to upstream
	client := &http.Client{
		Timeout: 30 * time.Second,
	}
	resp, err := client.Do(upstreamReq)
	if err != nil {
		log.Printf("[Proxy] Upstream request failed: %v", err)
		http.Error(w, "Bad Gateway", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	// Calculate duration
	duration := time.Since(startTime)

	// Copy response headers
	for key, values := range resp.Header {
		for _, value := range values {
			w.Header().Add(key, value)
		}
	}

	// Write status code
	w.WriteHeader(resp.StatusCode)

	// Copy response body
	io.Copy(w, resp.Body)

	// Create span event
	event := buffer.Event{
		"service_name": p.serviceName,
		"event_id":     uuid.New().String(),
		"timestamp":    startTime.UTC().Format(time.RFC3339),
		"event_type":   "span",
		"environment":  p.environment,
		"trace_id":     traceID,
		"span_id":      spanID,
		"operation":    fmt.Sprintf("%s %s", r.Method, r.URL.Path),
		"duration_ms":  float64(duration.Milliseconds()),
		"status_code":  resp.StatusCode,
		"tags": map[string]string{
			"method": r.Method,
			"path":   r.URL.Path,
			"host":   r.Host,
		},
	}

	// Add to buffer
	p.buffer.Add(event)

	log.Printf("[Proxy] %s %s -> %d (%dms)", r.Method, r.URL.Path, resp.StatusCode, duration.Milliseconds())
}
