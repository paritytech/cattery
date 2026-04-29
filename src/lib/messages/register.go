package messages

import (
	"cattery/lib/agents"
)

type RegisterResponse struct {
	Agent         agents.Agent `json:"agent"`
	JitConfig     string       `json:"jit_config"`
	RunnerVersion string       `json:"runner_version,omitempty"`
}

type UnregisterRequest struct {
	Agent   agents.Agent     `json:"agent"`
	Reason  UnregisterReason `json:"reason"`
	Message string           `json:"message"`
}

type UnregisterReason int

const (
	UnregisterReasonUnknown UnregisterReason = iota
	UnregisterReasonDone
	UnregisterReasonPreempted
	UnregisterReasonSigTerm
	UnregisterReasonControllerKill
)

type PingResponse struct {
	Terminate bool   `json:"terminate"`
	Message   string `json:"message"`
}
