package scaleSetPoller

import (
	"sync"
	"time"
)

const historySize = 100

type MessageKind string

const (
	MessageKindScale        MessageKind = "scale"
	MessageKindJobStarted   MessageKind = "job_started"
	MessageKindJobCompleted MessageKind = "job_completed"
)

// ScaleStats is a display-only snapshot of scale-set statistics, decoupled from
// the scaleset library types so the history package has no upstream dependency.
type ScaleStats struct {
	Available  int
	Assigned   int
	Running    int
	Busy       int
	Idle       int
	Registered int
}

type Message struct {
	Time     time.Time
	Kind     MessageKind
	TrayType string

	// Job event fields (set on job_started / job_completed).
	Repository     string
	WorkflowRunID  int64
	JobID          int64
	JobDisplayName string
	RunnerName     string
	Result         string

	// Scale event fields (set on scale).
	DesiredCount int
	Stats        *ScaleStats
}

func (m *Message) IsScale() bool { return m.Kind == MessageKindScale }

type History struct {
	mu    sync.RWMutex
	buf   [historySize]*Message
	head  int
	count int
}

func (h *History) Add(m *Message) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.buf[h.head] = m
	h.head = (h.head + 1) % historySize
	if h.count < historySize {
		h.count++
	}
}

// Recent returns up to historySize messages, newest first.
func (h *History) Recent() []*Message {
	h.mu.RLock()
	defer h.mu.RUnlock()
	result := make([]*Message, h.count)
	start := (h.head - h.count + historySize) % historySize
	for i := 0; i < h.count; i++ {
		result[h.count-1-i] = h.buf[(start+i)%historySize]
	}
	return result
}
