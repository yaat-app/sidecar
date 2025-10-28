//go:build !linux || !cgo
// +build !linux !cgo

package logs

import (
	"log"

	"github.com/yaat-app/sidecar/internal/buffer"
)

type JournaldTailer struct{}

func NewJournaldTailer(serviceName, environment string, buf *buffer.Buffer) *JournaldTailer {
	return &JournaldTailer{}
}

func (t *JournaldTailer) Start(matchUnit string) error {
	log.Printf("[Journald] Streaming not supported on this platform")
	return nil
}

func (t *JournaldTailer) Stop() {}
