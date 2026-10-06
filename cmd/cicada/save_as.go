package main

import (
	"fmt"
	"m31labs.dev/cicada/host/projectcopy"
)

func saveAsCommand(args []string) error {
	if len(args) != 2 {
		return fmt.Errorf("usage: cicada save-as <score.cicada> <target.cicada>")
	}
	return projectcopy.SaveAs(args[0], args[1])
}
