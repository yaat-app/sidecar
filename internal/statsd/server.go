package statsd

import (
	"bufio"
	"fmt"
	"log"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/yaat-app/sidecar/internal/buffer"
	"github.com/yaat-app/sidecar/internal/config"
	"github.com/yaat-app/sidecar/internal/scrubber"
)

// Server listens for StatsD/dogstatsd metrics and forwards them as metric events.
type Server struct {
	addr           string
	namespace      string
	tags           map[string]string
	organizationID string
	service        string
	env            string
	buf            *buffer.Buffer

	mu         sync.RWMutex
	conns      []net.PacketConn
	listenAddr string

	stop chan struct{}
	wg   sync.WaitGroup
}

// New creates a new StatsD server.
func New(cfg config.StatsDConfig, organizationID, serviceName, environment string, globalTags map[string]string, buf *buffer.Buffer) *Server {
	// Merge global tags with StatsD-specific tags (StatsD-specific take priority)
	tagCopy := make(map[string]string, len(globalTags)+len(cfg.Tags))
	for k, v := range globalTags {
		tagCopy[k] = v
	}
	for k, v := range cfg.Tags {
		tagCopy[k] = v
	}
	return &Server{
		addr:           cfg.ListenAddr,
		namespace:      cfg.Namespace,
		tags:           tagCopy,
		organizationID: organizationID,
		service:        serviceName,
		env:            environment,
		buf:            buf,
		stop:           make(chan struct{}),
	}
}

// Start begins listening for UDP packets. Returns a function to stop the server.
func (s *Server) Start() (func(), error) {
	conn, err := net.ListenPacket("udp", s.addr)
	if err != nil {
		return nil, fmt.Errorf("listen udp %s: %w", s.addr, err)
	}

	s.mu.Lock()
	s.listenAddr = conn.LocalAddr().String()
	s.conns = append(s.conns, conn)
	s.mu.Unlock()

	s.wg.Add(1)
	go s.serve(conn)

	return func() {
		close(s.stop)
		s.mu.Lock()
		for _, c := range s.conns {
			_ = c.Close()
		}
		s.mu.Unlock()
		s.wg.Wait()
	}, nil
}

// Addr returns the listener address, useful for tests.
func (s *Server) Addr() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.listenAddr
}

func (s *Server) serve(conn net.PacketConn) {
	defer s.wg.Done()

	buf := make([]byte, 65535)
	for {
		conn.SetReadDeadline(time.Now().Add(2 * time.Second))
		n, _, err := conn.ReadFrom(buf)
		if err != nil {
			select {
			case <-s.stop:
				return
			default:
			}
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				continue
			}
			log.Printf("[StatsD] Read error: %v", err)
			continue
		}

		payload := string(buf[:n])
		s.handleMessage(payload)
	}
}

func (s *Server) handleMessage(payload string) {
	now := time.Now().UTC()
	scanner := bufio.NewScanner(strings.NewReader(payload))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		event, err := s.parseLine(line, now)
		if err != nil {
			log.Printf("[StatsD] Parse error: %v", err)
			continue
		}
		if scrubber.Apply(event) {
			s.buf.Add(event)
		}
	}
	if err := scanner.Err(); err != nil {
		log.Printf("[StatsD] Scanner error: %v", err)
	}
}

func (s *Server) parseLine(line string, now time.Time) (buffer.Event, error) {
	parts := strings.Split(line, "|")
	if len(parts) < 2 {
		return nil, fmt.Errorf("invalid statsd line %q", line)
	}

	nameVal := parts[0]
	typeSpec := parts[1]

	nameValue := strings.SplitN(nameVal, ":", 2)
	if len(nameValue) != 2 {
		return nil, fmt.Errorf("invalid name/value %q", nameVal)
	}

	name := nameValue[0]
	valueStr := nameValue[1]
	value, err := strconv.ParseFloat(valueStr, 64)
	if err != nil {
		return nil, fmt.Errorf("invalid value %q", valueStr)
	}

	metricType := strings.TrimSpace(typeSpec)

	sampleRate := 1.0
	var tags []string
	for _, part := range parts[2:] {
		if strings.HasPrefix(part, "@") {
			if rate, parseErr := strconv.ParseFloat(part[1:], 64); parseErr == nil && rate > 0 {
				sampleRate = rate
			}
		}
		if strings.HasPrefix(part, "#") {
			tags = strings.Split(part[1:], ",")
		}
	}

	finalValue := value
	switch metricType {
	case "c":
		if sampleRate != 0 {
			finalValue = value / sampleRate
		}
	case "ms", "h":
		// send as-is
	case "g":
		// StatsD gauges accept +/- adjustments. If prefixed with +/-, treat as delta.
		if strings.HasPrefix(valueStr, "+") || strings.HasPrefix(valueStr, "-") {
			// Additional state (per gauge) would be required for cumulative gauges.
		}
	case "s":
		// sets; we treat as gauge count
	default:
		// default to gauge semantics
	}

	fullName := name
	if s.namespace != "" {
		fullName = s.namespace + "." + name
	}

	eventTags := make(map[string]string, len(s.tags)+len(tags)+1)
	for k, v := range s.tags {
		eventTags[k] = v
	}
	for _, tag := range tags {
		if tag == "" {
			continue
		}
		if kv := strings.SplitN(tag, ":", 2); len(kv) == 2 {
			eventTags[kv[0]] = kv[1]
		} else {
			eventTags[tag] = "true"
		}
	}
	eventTags["statsd_type"] = metricType

	serviceName := s.service
	if serviceName == "" {
		serviceName = "statsd"
	}

	environment := s.env
	if environment == "" {
		environment = "production"
	}

	return buffer.Event{
		"organization_id": s.organizationID,
		"service_name":    serviceName,
		"environment":     environment,
		"event_type":      "metric",
		"timestamp":       now.Format(time.RFC3339Nano),
		"metric_name":     fullName,
		"metric_value":    finalValue,
		"tags":            eventTags,
	}, nil
}
