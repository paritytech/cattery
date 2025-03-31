package main

import (
	"cattery/cmd"
	log "github.com/sirupsen/logrus"
)

func main() {

	log.SetLevel(log.DebugLevel)

	cmd.Execute()
}
