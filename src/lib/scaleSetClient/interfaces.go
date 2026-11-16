package scaleSetClient

import (
	"context"

	"github.com/actions/scaleset"
)

// JitConfigGenerator generates JIT runner configurations for ephemeral runners
// and deregisters those runners again.
type JitConfigGenerator interface {
	GenerateJitRunnerConfig(ctx context.Context, runnerName string) (*scaleset.RunnerScaleSetJitRunnerConfig, error)
	RemoveRunner(ctx context.Context, runnerId int64) error
}
