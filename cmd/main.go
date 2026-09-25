package main

import (
	"github.com/mechta-market/pulse_agent/internal/app"
)

func main() {
	a := &app.App{}

	a.Init()
	a.PreStartHook()
	a.Start()
	a.Listen()
	a.Stop()
	a.WaitJobs()
	a.Exit()
}
