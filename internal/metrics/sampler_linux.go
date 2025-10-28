//go:build linux

package metrics

import (
	"bufio"
	"fmt"
	"os"
	"strings"
	"time"

	"golang.org/x/sys/unix"
)

type sampler interface {
	Read() (Counters, error)
}

// Counters represents raw host counters used to derive metrics.
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
	return &linuxSampler{}, nil
}

type linuxSampler struct{}

func (s *linuxSampler) Read() (Counters, error) {
	now := time.Now().UTC()

	total, idle, err := readCPUStat()
	if err != nil {
		return Counters{}, err
	}

	memTotal, memAvailable, err := readMemInfo()
	if err != nil {
		return Counters{}, err
	}

	diskTotal, diskFree, err := readDiskUsage("/")
	if err != nil {
		return Counters{}, err
	}

	netRx, netTx, err := readNetDev()
	if err != nil {
		return Counters{}, err
	}

	return Counters{
		Timestamp:    now,
		CPUTotal:     total,
		CPUIdle:      idle,
		MemTotal:     memTotal,
		MemAvailable: memAvailable,
		DiskTotal:    diskTotal,
		DiskFree:     diskFree,
		NetRxBytes:   netRx,
		NetTxBytes:   netTx,
	}, nil
}

func readCPUStat() (total uint64, idle uint64, err error) {
	file, err := os.Open("/proc/stat")
	if err != nil {
		return 0, 0, fmt.Errorf("open /proc/stat: %w", err)
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	if !scanner.Scan() {
		return 0, 0, fmt.Errorf("empty /proc/stat")
	}

	fields := strings.Fields(scanner.Text())
	if len(fields) < 5 || fields[0] != "cpu" {
		return 0, 0, fmt.Errorf("unexpected /proc/stat format")
	}

	var values []uint64
	for _, field := range fields[1:] {
		var v uint64
		_, scanErr := fmt.Sscan(field, &v)
		if scanErr != nil {
			return 0, 0, fmt.Errorf("parse /proc/stat field %q: %w", field, scanErr)
		}
		values = append(values, v)
	}

	for _, v := range values {
		total += v
	}

	if len(values) >= 4 {
		idle = values[3]
	}
	if len(values) >= 5 {
		idle += values[4] // iowait
	}

	return total, idle, nil
}

func readMemInfo() (total uint64, available uint64, err error) {
	file, err := os.Open("/proc/meminfo")
	if err != nil {
		return 0, 0, fmt.Errorf("open /proc/meminfo: %w", err)
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "MemTotal:") {
			fmt.Sscanf(line, "MemTotal: %d kB", &total)
			total *= 1024
		}
		if strings.HasPrefix(line, "MemAvailable:") {
			fmt.Sscanf(line, "MemAvailable: %d kB", &available)
			available *= 1024
		}
		if total != 0 && available != 0 {
			break
		}
	}
	if err := scanner.Err(); err != nil {
		return 0, 0, fmt.Errorf("scan /proc/meminfo: %w", err)
	}

	if total == 0 {
		return 0, 0, fmt.Errorf("memtotal not found")
	}

	return total, available, nil
}

func readDiskUsage(path string) (total uint64, free uint64, err error) {
	var stat unix.Statfs_t
	if err := unix.Statfs(path, &stat); err != nil {
		return 0, 0, fmt.Errorf("statfs %s: %w", path, err)
	}

	total = stat.Blocks * uint64(stat.Bsize)
	free = stat.Bavail * uint64(stat.Bsize)
	return total, free, nil
}

func readNetDev() (rx uint64, tx uint64, err error) {
	file, err := os.Open("/proc/net/dev")
	if err != nil {
		return 0, 0, fmt.Errorf("open /proc/net/dev: %w", err)
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	// Skip two header lines
	for i := 0; i < 2 && scanner.Scan(); i++ {
	}

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		parts := strings.Split(line, ":")
		if len(parts) != 2 {
			continue
		}
		iface := strings.TrimSpace(parts[0])
		if iface == "lo" {
			continue
		}

		fields := strings.Fields(parts[1])
		if len(fields) < 16 {
			continue
		}

		var ifaceRx, ifaceTx uint64
		fmt.Sscanf(fields[0], "%d", &ifaceRx)
		fmt.Sscanf(fields[8], "%d", &ifaceTx)

		rx += ifaceRx
		tx += ifaceTx
	}
	if err := scanner.Err(); err != nil {
		return 0, 0, fmt.Errorf("scan /proc/net/dev: %w", err)
	}

	return rx, tx, nil
}
