//go:build !linux

package metrics

import (
	"fmt"
	"runtime"
	"time"
)

type sampler interface {
	Read() (Counters, error)
}

type Counters struct {
	Timestamp    time.Time
	CPUTotal     uint64
	CPUIdle      uint64
	MemTotal     uint64
	MemAvailable uint64
	DiskTotal    uint64
	DiskFree     uint64
	NetRxBytes   uint64
	NetTxBytes   uint64
}

func newSampler() (sampler, error) {
	return nil, fmt.Errorf("host metrics not supported on %s", runtime.GOOS)
}
