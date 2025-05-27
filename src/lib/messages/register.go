package messages

import (
	"cattery/lib/agents"
)

type RegisterResponse struct {
	Agent         agents.Agent `json:"agent"`
	JitConfig     string       `json:"jit_config"`
	GitHubOrgName string       `json:"github_org_name"`
}

type UnregisterRequest struct {
	Agent         agents.Agent     `json:"agent"`
	Reason        UnregisterReason `json:"reason"`
	GitHubOrgName string           `json:"github_org_name"`
}

type UnregisterReason int

const (
	UnregisterReasonUnknown UnregisterReason = iota
	UnregisterReasonDone
	UnregisterReasonPreempted
)
