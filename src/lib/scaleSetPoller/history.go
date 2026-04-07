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

type Message struct {
	Time     time.Time
	Kind     MessageKind
	TrayType string
	Detail   string
}

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
	for i := range h.count {
		idx := ((h.head - 1 - i) % historySize + historySize) % historySize
		result[i] = h.buf[idx]
	}
	return result
}
