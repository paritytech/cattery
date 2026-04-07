package scaleSetPoller

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestHistory_EmptyRecent(t *testing.T) {
	h := &History{}
	assert.Empty(t, h.Recent())
}

func TestHistory_AddUnderCapacity(t *testing.T) {
	h := &History{}
	base := time.Now()
	for i := 0; i < 3; i++ {
		h.Add(&Message{Time: base.Add(time.Duration(i) * time.Second), Detail: string(rune('a' + i))})
	}

	got := h.Recent()
	assert.Len(t, got, 3)
	// Newest first
	assert.Equal(t, "c", got[0].Detail)
	assert.Equal(t, "b", got[1].Detail)
	assert.Equal(t, "a", got[2].Detail)
}

func TestHistory_RingOverwrite(t *testing.T) {
	h := &History{}
	// Add 150 > historySize(100); oldest 50 should be dropped.
	for i := 0; i < historySize+50; i++ {
		h.Add(&Message{Time: time.Unix(int64(i), 0), TrayType: "t"})
	}

	got := h.Recent()
	assert.Len(t, got, historySize)
	// Newest should be i=149, oldest kept should be i=50.
	assert.Equal(t, int64(historySize+50-1), got[0].Time.Unix())
	assert.Equal(t, int64(50), got[historySize-1].Time.Unix())

	// Strictly descending order.
	for i := 1; i < len(got); i++ {
		assert.True(t, got[i-1].Time.After(got[i].Time), "not newest-first at index %d", i)
	}
}

func TestHistory_ConcurrentAdd(t *testing.T) {
	h := &History{}
	var wg sync.WaitGroup
	const writers = 10
	const perWriter = 200
	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < perWriter; i++ {
				h.Add(&Message{Time: time.Now()})
			}
		}()
	}
	wg.Wait()

	got := h.Recent()
	assert.Len(t, got, historySize)
	for _, m := range got {
		assert.NotNil(t, m)
	}
}

func TestManager_MessageHistory_MergesAndSorts(t *testing.T) {
	m := NewManager()

	p1 := &Poller{history: &History{}}
	p2 := &Poller{history: &History{}}
	m.pollers["a"] = p1
	m.pollers["b"] = p2

	base := time.Now()
	p1.history.Add(&Message{Time: base.Add(1 * time.Second), TrayType: "a"})
	p1.history.Add(&Message{Time: base.Add(3 * time.Second), TrayType: "a"})
	p2.history.Add(&Message{Time: base.Add(2 * time.Second), TrayType: "b"})
	p2.history.Add(&Message{Time: base.Add(4 * time.Second), TrayType: "b"})

	got := m.MessageHistory()
	assert.Len(t, got, 4)
	for i := 1; i < len(got); i++ {
		assert.False(t, got[i-1].Time.Before(got[i].Time), "not newest-first at %d", i)
	}
	assert.Equal(t, "b", got[0].TrayType)
	assert.Equal(t, "a", got[3].TrayType)
}

func TestManager_MessageHistory_Empty(t *testing.T) {
	m := NewManager()
	assert.Empty(t, m.MessageHistory())
}
