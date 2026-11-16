package scaleSetClient

import (
	"context"
	"errors"
	"testing"

	"github.com/actions/scaleset"
	"github.com/stretchr/testify/assert"
)

type stubClient struct {
	removeErr error
	removed   []int64
}

func (s *stubClient) GenerateJitRunnerConfig(context.Context, string) (*scaleset.RunnerScaleSetJitRunnerConfig, error) {
	return nil, nil
}

func (s *stubClient) RemoveRunner(_ context.Context, runnerId int64) error {
	s.removed = append(s.removed, runnerId)
	return s.removeErr
}

func TestJitRegistry_RemoveRunner(t *testing.T) {
	busy := errors.New("job still running")
	a, b := &stubClient{removeErr: busy}, &stubClient{}

	r := NewJitRegistry()
	r.Register("type-a", a)
	r.Register("type-b", b)

	assert.ErrorIs(t, r.RemoveRunner(context.Background(), "type-a", 42), busy)
	assert.Equal(t, []int64{42}, a.removed)
	assert.Empty(t, b.removed, "the runner belongs to the other tray type")

	// A tray type that is no longer configured has no client to ask.
	assert.NoError(t, r.RemoveRunner(context.Background(), "gone", 42))
}
