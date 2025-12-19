package Watchers

import (
	"cattery/agent/catteryClient"
	"cattery/agent/shutdownEvents"
	"cattery/lib/messages"
	"context"
	"time"
)

func WatchPing(ctx context.Context, client *catteryClient.CatteryClient) {
	go func() {
		var msg string
		var finished = false

		for !finished {
			select {
			case <-ctx.Done(): // selected when context is canceled or times out
				msg = "cattery client shutdown"
				finished = true
				break
			default:
				pingResponse, err := client.Ping()
				if err != nil {
					msg = "error pinging controller: " + err.Error()
					finished = true
					break
				}

				if pingResponse.Terminate {
					msg = "controller ping receive 'terminate': " + pingResponse.Message
					finished = true
					break
				}

				time.Sleep(60 * time.Second)
			}
		}

		shutdownEvents.Emit(messages.UnregisterReasonControllerKill, msg)
	}()
}
