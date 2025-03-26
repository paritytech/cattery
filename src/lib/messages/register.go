package messages

import (
	"cattery/lib/agents"
)

type RegisterResponse struct {
	Agent     agents.Agent `json:"agent"`
	JitConfig string       `json:"jit_config"`
}

type UnregisterRequest struct {
	Agent  agents.Agent     `json:"agent"`
	Reason UnregisterReason `json:"reason"`
}

type UnregisterReason int

const (
	Unknown UnregisterReason = iota
	Done
	Preempted
)
