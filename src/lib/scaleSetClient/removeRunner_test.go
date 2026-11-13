package scaleSetClient

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/actions/scaleset"
	"github.com/stretchr/testify/assert"
)

// The scaleset client reports GitHub's verdict as sentinel errors buried in a
// formatted chain. Callers of RemoveRunner rely on them being told apart.
func TestClassifyRemoveRunnerError(t *testing.T) {
	transient := errors.New("unexpected status code: 503")

	t.Run("removed", func(t *testing.T) {
		assert.NoError(t, classifyRemoveRunnerError(nil))
	})

	t.Run("runner already gone counts as removed", func(t *testing.T) {
		err := fmt.Errorf("request DELETE failed: %w: gone", scaleset.RunnerNotFoundError)
		assert.NoError(t, classifyRemoveRunnerError(err))
	})

	// What the scaleset client returns for a 404 whose body does not name
	// AgentNotFoundException: the status code survives only as text.
	t.Run("unrecognised 404 counts as removed", func(t *testing.T) {
		err := fmt.Errorf(`request DELETE https://x/agents/42 failed(status="404 Not Found"): %w: unknown error`,
			errors.New("unexpected status code: 404"))
		assert.NoError(t, classifyRemoveRunnerError(err))
	})

	t.Run("job still running", func(t *testing.T) {
		err := fmt.Errorf("request DELETE failed: %w: busy", scaleset.JobStillRunningError)
		assert.ErrorIs(t, classifyRemoveRunnerError(err), ErrRunnerBusy)
	})

	t.Run("anything else is passed through", func(t *testing.T) {
		got := classifyRemoveRunnerError(transient)
		assert.ErrorIs(t, got, transient)
		assert.NotErrorIs(t, got, ErrRunnerBusy)
	})
}

type stubGenerator struct{}

func (stubGenerator) GenerateJitRunnerConfig(context.Context, string) (*scaleset.RunnerScaleSetJitRunnerConfig, error) {
	return nil, nil
}

type stubRemover struct {
	stubGenerator
	err   error
	calls []int64
}

func (s *stubRemover) RemoveRunner(_ context.Context, runnerId int64) error {
	s.calls = append(s.calls, runnerId)
	return s.err
}

func TestJitRegistry_RemoveRunner(t *testing.T) {
	t.Run("routes to the tray type's client", func(t *testing.T) {
		remover := &stubRemover{err: ErrRunnerBusy}
		other := &stubRemover{}
		r := NewJitRegistry()
		r.Register("type-a", remover)
		r.Register("type-b", other)

		err := r.RemoveRunner(context.Background(), "type-a", 42)

		assert.ErrorIs(t, err, ErrRunnerBusy)
		assert.Equal(t, []int64{42}, remover.calls)
		assert.Empty(t, other.calls)
	})

	t.Run("unknown tray type", func(t *testing.T) {
		r := NewJitRegistry()

		err := r.RemoveRunner(context.Background(), "gone", 42)

		assert.ErrorIs(t, err, ErrNoClient)
	})

	t.Run("client that cannot remove runners", func(t *testing.T) {
		r := NewJitRegistry()
		r.Register("type-a", stubGenerator{})

		err := r.RemoveRunner(context.Background(), "type-a", 42)

		assert.ErrorIs(t, err, ErrNoClient)
	})
}
