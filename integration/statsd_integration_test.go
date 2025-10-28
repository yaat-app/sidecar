package integration

import (
	"net"
	"strings"
	"testing"
	"time"

	"github.com/yaat-app/sidecar/internal/buffer"
	"github.com/yaat-app/sidecar/internal/config"
	"github.com/yaat-app/sidecar/internal/statsd"
)

func TestStatsDServer_ParsesCounter(t *testing.T) {
	buf := buffer.New(10)

	cfg := config.StatsDConfig{
		Enabled:    true,
		ListenAddr: ":0",
		Namespace:  "demo",
		Tags: map[string]string{
			"service":     "demo-service",
			"environment": "testing",
		},
	}

	srv := statsd.New(cfg, "demo-service", "testing", buf)
	stop, err := srv.Start()
	if err != nil {
		if strings.Contains(err.Error(), "operation not permitted") {
			t.Skipf("skipping statsd integration test: %v", err)
		}
		t.Fatalf("failed to start statsd server: %v", err)
	}
	defer stop()

	addr := srv.Addr()
	if addr == "" {
		t.Fatalf("listener address not set")
	}
	conn, err := net.Dial("udp", addr)
	if err != nil {
		t.Fatalf("dial udp: %v", err)
	}
	defer conn.Close()

	if _, err := conn.Write([]byte("request.count:5|c")); err != nil {
		t.Fatalf("write: %v", err)
	}

	time.Sleep(100 * time.Millisecond)

	events := buf.Flush()
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}

	evt := events[0]
	if evt["metric_name"] != "demo.request.count" {
		t.Fatalf("unexpected metric_name: %v", evt["metric_name"])
	}
	if evt["metric_value"] != float64(5) {
		t.Fatalf("unexpected value: %v", evt["metric_value"])
	}
	if evt["service_name"] != "demo-service" {
		t.Fatalf("unexpected service_name: %v", evt["service_name"])
	}
	if tags, ok := evt["tags"].(map[string]string); !ok || tags["statsd_type"] != "c" {
		t.Fatalf("expected statsd_type tag, got %v", evt["tags"])
	}
}
