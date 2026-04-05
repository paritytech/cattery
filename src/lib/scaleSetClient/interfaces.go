package scaleSetClient

import (
	"context"

	"github.com/actions/scaleset"
)

// JitConfigGenerator generates JIT runner configurations for ephemeral runners.
type JitConfigGenerator interface {
	GenerateJitRunnerConfig(ctx context.Context, runnerName string) (*scaleset.RunnerScaleSetJitRunnerConfig, error)
}
