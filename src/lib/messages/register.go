package messages

import "cattery/server/trays"

type RegisterResponse struct {
	Tray      trays.Tray `json:"tray"`
	JitConfig string     `json:"jit_config"`
}

type UnregisterRequest struct {
	Tray   trays.Tray       `json:"tray"`
	Reason UnregisterReason `json:"reason"`
}

type UnregisterReason int

const (
	Unknown UnregisterReason = iota
	Done
	Preempted
)
