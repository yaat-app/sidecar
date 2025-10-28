package queue

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"math/rand"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/yaat-app/sidecar/internal/buffer"
)

func init() {
	rand.Seed(time.Now().UnixNano())
}

// Storage implements a simple disk-backed queue using JSON batches on disk.
type Storage struct {
	dir    string
	dlqDir string
	mu     sync.Mutex
}

const (
	activeExt     = ".json"
	processingExt = ".processing"
)

// New creates (or opens) a storage directory. Any dangling processing files
// are moved back to active state.
func New(dir string) (*Storage, error) {
	if dir == "" {
		return nil, fmt.Errorf("queue directory is empty")
	}

	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("create queue dir: %w", err)
	}

	dlq := filepath.Join(dir, "deadletter")
	if err := os.MkdirAll(dlq, 0o755); err != nil {
		return nil, fmt.Errorf("create deadletter dir: %w", err)
	}

	s := &Storage{dir: dir, dlqDir: dlq}
	if err := s.recoverProcessing(); err != nil {
		return nil, err
	}
	return s, nil
}

// Dir returns the underlying directory.
func (s *Storage) Dir() string {
	return s.dir
}

// DeadLetterDir returns the dead letter directory.
func (s *Storage) DeadLetterDir() string {
	return s.dlqDir
}

// Enqueue persists a batch of events to disk.
func (s *Storage) Enqueue(events []buffer.Event) error {
	if len(events) == 0 {
		return nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	filename := filepath.Join(s.dir, s.generateFilename())
	file, err := os.Create(filename)
	if err != nil {
		return fmt.Errorf("create queue file: %w", err)
	}
	defer file.Close()

	encoder := json.NewEncoder(file)
	if err := encoder.Encode(events); err != nil {
		return fmt.Errorf("encode queue file: %w", err)
	}

	return nil
}

// Dequeue loads the oldest batch. The returned token must be passed to Ack or Fail.
func (s *Storage) Dequeue() (token string, events []buffer.Event, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	files, err := s.listActive()
	if err != nil {
		return "", nil, err
	}
	if len(files) == 0 {
		return "", nil, nil
	}

	original := files[0]
	processing := original + processingExt
	if err := os.Rename(original, processing); err != nil {
		return "", nil, fmt.Errorf("mark processing: %w", err)
	}

	data, err := os.ReadFile(processing)
	if err != nil {
		_ = os.Rename(processing, original)
		return "", nil, fmt.Errorf("read queue file: %w", err)
	}

	var batch []buffer.Event
	if err := json.Unmarshal(data, &batch); err != nil {
		_ = os.Rename(processing, original)
		return "", nil, fmt.Errorf("decode queue file: %w", err)
	}

	return processing, batch, nil
}

// Ack removes a batch after successful delivery.
func (s *Storage) Ack(token string) error {
	if token == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return os.Remove(token)
}

// Fail re-queues a batch for later retry.
func (s *Storage) Fail(token string) error {
	if token == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	if !strings.HasSuffix(token, processingExt) {
		return fmt.Errorf("unexpected token %s", token)
	}
	original := strings.TrimSuffix(token, processingExt)
	return os.Rename(token, original)
}

// Pending returns the number of queued batches.
func (s *Storage) Pending() (int, error) {
	files, err := s.listActive()
	if err != nil {
		return 0, err
	}
	return len(files), nil
}

// DeadLetterPending returns the number of batches in the DLQ.
func (s *Storage) DeadLetterPending() (int, error) {
	entries, err := os.ReadDir(s.dlqDir)
	if err != nil {
		return 0, fmt.Errorf("read deadletter dir: %w", err)
	}
	count := 0
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		count++
	}
	return count, nil
}

// MoveToDLQ moves a failed batch to the dead letter directory.
func (s *Storage) MoveToDLQ(token string) error {
	if token == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	if !strings.HasSuffix(token, processingExt) {
		return fmt.Errorf("unexpected token %s", token)
	}

	base := filepath.Base(strings.TrimSuffix(token, processingExt))
	dest := filepath.Join(s.dlqDir, base)
	if err := os.Rename(token, dest); err != nil {
		return fmt.Errorf("move to deadletter: %w", err)
	}
	return nil
}

func (s *Storage) recoverProcessing() error {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return fmt.Errorf("read queue dir: %w", err)
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if strings.HasSuffix(name, processingExt) {
			src := filepath.Join(s.dir, name)
			dst := strings.TrimSuffix(src, processingExt)
			if err := os.Rename(src, dst); err != nil {
				return fmt.Errorf("recover %s: %w", name, err)
			}
		}
	}
	return nil
}

func (s *Storage) listActive() ([]string, error) {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return nil, fmt.Errorf("read queue dir: %w", err)
	}
	var files []string
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		if strings.HasSuffix(entry.Name(), activeExt) {
			files = append(files, filepath.Join(s.dir, entry.Name()))
		}
	}
	sort.Strings(files)
	return files, nil
}

func (s *Storage) generateFilename() string {
	now := time.Now().UTC()
	return fmt.Sprintf("%d-%04d%s", now.UnixNano(), rand.Intn(10000), activeExt)
}

// Cleanup removes files older than retention duration.
func (s *Storage) Cleanup(queueRetention, dlqRetention time.Duration) error {
	if queueRetention > 0 {
		cutoff := time.Now().Add(-queueRetention)
		if err := cleanupDir(s.dir, cutoff); err != nil {
			return err
		}
	}
	if dlqRetention > 0 {
		cutoff := time.Now().Add(-dlqRetention)
		if err := cleanupDir(s.dlqDir, cutoff); err != nil {
			return err
		}
	}
	return nil
}

func cleanupDir(dir string, cutoff time.Time) error {
	return filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		info, statErr := d.Info()
		if statErr != nil {
			return statErr
		}
		if info.ModTime().Before(cutoff) {
			_ = os.Remove(path)
		}
		return nil
	})
}

// DefaultDir returns ~/.yaat/queue.
func DefaultDir() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return "./.yaat-queue"
	}
	return filepath.Join(home, ".yaat", "queue")
}
