package messages

type RegisterResponse struct {
	AgentID   string `json:"agent_id"`
	JitConfig string `json:"jit_config"`
}

type UnregisterRequest struct {
	AgentID string           `json:"agent_id"`
	Reason  UnregisterReason `json:"reason"`
}

type UnregisterReason int

const (
	Unknown UnregisterReason = iota
	Done
	Preempted
)
