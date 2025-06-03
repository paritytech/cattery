package trays

type TrayStatus int

const (
	TrayStatusCreating TrayStatus = iota
	TrayStatusRegistered
	TrayStatusRunning
	TrayStatusDeleting
)

var stateName = map[TrayStatus]string{
	TrayStatusCreating:   "creating",
	TrayStatusRegistered: "registered",
	TrayStatusRunning:    "running",
	TrayStatusDeleting:   "deleting",
}

func (js TrayStatus) String() string {
	return stateName[js]
}
