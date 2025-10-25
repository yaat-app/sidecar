package forwarder

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math"
	"net/http"
	"time"

	"github.com/yaat/sidecar/internal/buffer"
)

// Forwarder sends events to the YAAT API
type Forwarder struct {
	apiEndpoint string
	apiKey      string
	client      *http.Client
}

// New creates a new Forwarder
func New(apiEndpoint, apiKey string) *Forwarder {
	return &Forwarder{
		apiEndpoint: apiEndpoint,
		apiKey:      apiKey,
		client: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// Send sends events to the YAAT API with retry logic
func (f *Forwarder) Send(events []buffer.Event) error {
	if len(events) == 0 {
		return nil
	}

	// Create request payload
	payload := map[string]interface{}{
		"events": events,
	}

	jsonData, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal events: %w", err)
	}

	// Retry with exponential backoff
	maxRetries := 3
	for attempt := 0; attempt < maxRetries; attempt++ {
		if attempt > 0 {
			// Exponential backoff: 1s, 2s, 4s
			backoff := time.Duration(math.Pow(2, float64(attempt))) * time.Second
			log.Printf("[Forwarder] Retry attempt %d after %v", attempt+1, backoff)
			time.Sleep(backoff)
		}

		err = f.sendRequest(jsonData)
		if err == nil {
			log.Printf("[Forwarder] Successfully sent %d events", len(events))
			return nil
		}

		// Check if error is retryable
		if !isRetryable(err) {
			log.Printf("[Forwarder] Non-retryable error: %v", err)
			return err
		}

		log.Printf("[Forwarder] Retryable error (attempt %d/%d): %v", attempt+1, maxRetries, err)
	}

	return fmt.Errorf("failed after %d retries: %w", maxRetries, err)
}

// sendRequest sends a single HTTP request
func (f *Forwarder) sendRequest(jsonData []byte) error {
	req, err := http.NewRequest("POST", f.apiEndpoint, bytes.NewBuffer(jsonData))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	// Set headers
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", f.apiKey))

	// Send request
	resp, err := f.client.Do(req)
	if err != nil {
		return &RetryableError{Err: err}
	}
	defer resp.Body.Close()

	// Read response body
	body, _ := io.ReadAll(resp.Body)

	// Check response status
	switch resp.StatusCode {
	case 200, 201:
		// Success
		return nil
	case 401:
		// Invalid API key - fatal error
		return fmt.Errorf("authentication failed: invalid API key")
	case 429:
		// Rate limited - retryable
		return &RetryableError{Err: fmt.Errorf("rate limited")}
	case 500, 502, 503, 504:
		// Server error - retryable
		return &RetryableError{Err: fmt.Errorf("server error: %d - %s", resp.StatusCode, string(body))}
	default:
		// Client error - non-retryable
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(body))
	}
}

// RetryableError represents an error that can be retried
type RetryableError struct {
	Err error
}

func (e *RetryableError) Error() string {
	return e.Err.Error()
}

// isRetryable checks if an error is retryable
func isRetryable(err error) bool {
	_, ok := err.(*RetryableError)
	return ok
}
