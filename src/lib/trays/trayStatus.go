package trays

import (
	"fmt"
	"strings"
)

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

var stateByName = func() map[string]TrayStatus {
	m := make(map[string]TrayStatus, len(stateName))
	for s, n := range stateName {
		m[n] = s
	}
	return m
}()

func (js TrayStatus) String() string {
	return stateName[js]
}

// TrayStatusFromString parses a status name (case-insensitive) into a TrayStatus.
func TrayStatusFromString(name string) (TrayStatus, error) {
	if s, ok := stateByName[strings.ToLower(name)]; ok {
		return s, nil
	}
	return 0, fmt.Errorf("unknown tray status %q", name)
}
