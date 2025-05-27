package trays

type TrayStatus int

const (
	TrayStatusCreating TrayStatus = iota
	TrayStatusIdle
	TrayStatusRunning
	TrayStatusDeleting
)

var stateName = map[TrayStatus]string{
	TrayStatusCreating: "creating",
	TrayStatusIdle:     "idle",
	TrayStatusRunning:  "running",
	TrayStatusDeleting: "deleting",
}

func (js TrayStatus) String() string {
	return stateName[js]
}
