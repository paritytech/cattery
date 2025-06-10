package trays

type TrayStatus int

const (
	TrayStatusCreating TrayStatus = iota
	TrayStatusRegistering
	TrayStatusRegistered
	TrayStatusRunning
	TrayStatusDeleting
)

var stateName = map[TrayStatus]string{
	TrayStatusCreating:    "creating",
	TrayStatusRegistering: "registering",
	TrayStatusRegistered:  "registered",
	TrayStatusRunning:     "running",
	TrayStatusDeleting:    "deleting",
}

func (js TrayStatus) String() string {
	return stateName[js]
}
