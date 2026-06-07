package election

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
)

// MemoryElector leads immediately, hands onElected a leaderCtx tied to the
// parent ctx, and returns when the parent is cancelled.
func TestMemoryElector_AlwaysLeads(t *testing.T) {
	e := NewMemoryElector()

	ctx, cancel := context.WithCancel(context.Background())
	started := make(chan struct{})
	runDone := make(chan struct{})
	var leaderCtx context.Context

	go func() {
		_ = e.Run(ctx, "type-a", func(lctx context.Context) {
			leaderCtx = lctx
			close(started)
			<-lctx.Done()
		})
		close(runDone)
	}()

	waitClosed(t, started, "memory elector leads immediately")
	assert.NoError(t, leaderCtx.Err())

	cancel()
	waitClosed(t, runDone, "memory elector Run returns on ctx cancel")
	assert.Error(t, leaderCtx.Err())
}
