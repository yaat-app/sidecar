package metrics

import (
	"log"
	"sync"
	"time"

	"github.com/yaat-app/sidecar/internal/buffer"
	"github.com/yaat-app/sidecar/internal/config"
	"github.com/yaat-app/sidecar/internal/scrubber"
)

// Collector periodically samples host metrics and enqueues metric events.
type Collector struct {
	serviceName string
	environment string
	tags        map[string]string
	interval    time.Duration
	buf         *buffer.Buffer

	sampler sampler

	stop chan struct{}
	wg   sync.WaitGroup

	prev *Counters
}

// NewCollector constructs a collector using the provided configuration.
func NewCollector(serviceName, environment string, cfg config.MetricsConfig, buf *buffer.Buffer) (*Collector, error) {
	sampler, err := newSampler()
	if err != nil {
		return nil, err
	}

	tagsCopy := make(map[string]string, len(cfg.Tags))
	for k, v := range cfg.Tags {
		tagsCopy[k] = v
	}

	return &Collector{
		serviceName: serviceName,
		environment: environment,
		tags:        tagsCopy,
		interval:    cfg.IntervalDuration,
		buf:         buf,
		sampler:     sampler,
		stop:        make(chan struct{}),
	}, nil
}

// Start begins sampling on the configured interval. Call the returned function
// to stop the collector gracefully.
func (c *Collector) Start() func() {
	c.wg.Add(1)
	ticker := time.NewTicker(c.interval)

	go func() {
		defer c.wg.Done()
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				c.sample()
			case <-c.stop:
				return
			}
		}
	}()

	return func() {
		close(c.stop)
		c.wg.Wait()
	}
}

func (c *Collector) sample() {
	counters, err := c.sampler.Read()
	if err != nil {
		log.Printf("[Metrics] Sample failed: %v", err)
		return
	}

	events := c.buildEvents(counters)
	for _, evt := range events {
		if scrubber.Apply(evt) {
			c.buf.Add(evt)
		}
	}

	c.prev = &counters
}

func (c *Collector) buildEvents(curr Counters) []buffer.Event {
	var events []buffer.Event
	now := curr.Timestamp

	toEvent := func(name string, value float64, tags map[string]string) buffer.Event {
		eventTags := make(map[string]string, len(c.tags)+len(tags))
		for k, v := range c.tags {
			eventTags[k] = v
		}
		for k, v := range tags {
			eventTags[k] = v
		}
		return buffer.Event{
			"service_name": c.serviceName,
			"environment":  c.environment,
			"event_type":   "metric",
			"timestamp":    now.Format(time.RFC3339Nano),
			"metric_name":  name,
			"metric_value": value,
			"tags":         eventTags,
		}
	}

	if c.prev != nil && curr.CPUTotal > c.prev.CPUTotal {
		totalDelta := float64(curr.CPUTotal - c.prev.CPUTotal)
		idleDelta := float64(curr.CPUIdle - c.prev.CPUIdle)
		if totalDelta > 0 && idleDelta >= 0 {
			cpuUsage := (1.0 - idleDelta/totalDelta) * 100.0
			events = append(events, toEvent("host.cpu.usage_percent", cpuUsage, map[string]string{
				"unit": "percent",
			}))
		}
	}

	if curr.MemTotal > 0 && curr.MemAvailable <= curr.MemTotal {
		memUsed := float64(curr.MemTotal-curr.MemAvailable) / float64(curr.MemTotal) * 100.0
		events = append(events, toEvent("host.memory.usage_percent", memUsed, map[string]string{
			"unit": "percent",
		}))
		events = append(events, toEvent("host.memory.used_bytes", float64(curr.MemTotal-curr.MemAvailable), map[string]string{
			"unit": "bytes",
		}))
		events = append(events, toEvent("host.memory.total_bytes", float64(curr.MemTotal), map[string]string{
			"unit": "bytes",
		}))
	}

	if curr.DiskTotal > 0 && curr.DiskFree <= curr.DiskTotal {
		diskUsed := float64(curr.DiskTotal-curr.DiskFree) / float64(curr.DiskTotal) * 100.0
		events = append(events, toEvent("host.disk.usage_percent", diskUsed, map[string]string{
			"unit": "percent",
			"path": "/",
		}))
		events = append(events, toEvent("host.disk.used_bytes", float64(curr.DiskTotal-curr.DiskFree), map[string]string{
			"unit": "bytes",
			"path": "/",
		}))
	}

	if c.prev != nil {
		elapsed := curr.Timestamp.Sub(c.prev.Timestamp).Seconds()
		if elapsed > 0 {
			if curr.NetRxBytes >= c.prev.NetRxBytes {
				rxRate := float64(curr.NetRxBytes-c.prev.NetRxBytes) / elapsed
				events = append(events, toEvent("host.net.rx_bytes_per_sec", rxRate, map[string]string{
					"unit": "bytes_per_sec",
				}))
			}
			if curr.NetTxBytes >= c.prev.NetTxBytes {
				txRate := float64(curr.NetTxBytes-c.prev.NetTxBytes) / elapsed
				events = append(events, toEvent("host.net.tx_bytes_per_sec", txRate, map[string]string{
					"unit": "bytes_per_sec",
				}))
			}
		}
	}

	return events
}
