package shutdownEvents

import (
	"cattery/lib/messages"
	"sync"
)

type ShutdownEvent struct {
	Reason  messages.UnregisterReason
	Message string
}

var mut = new(sync.Mutex)

var channel = make(chan ShutdownEvent, 1)
var emitted = false

func Emit(unregisterReason messages.UnregisterReason, message string) {
	mut.Lock()
	defer mut.Unlock()

	var event = ShutdownEvent{
		Reason:  unregisterReason,
		Message: message,
	}

	if !emitted {
		channel <- event
		emitted = true
	}
}

func WaitEvent() ShutdownEvent {
	return <-channel
}
