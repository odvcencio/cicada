package main

import (
	"fmt"
	"os"
	"runtime/pprof"
	"strings"
)

// cpuProfileCommands are the commands that accept --cpuprofile <file>.
var cpuProfileCommands = map[string]bool{"render": true, "stems": true, "apply": true, "check": true, "audition": true}

// stopCPUProfile ends the CPU profile that --cpuprofile started, if any. exitProcess
// calls it, so a command that fails still writes its profile.
var stopCPUProfile = func() {}

// exitProcess stops a running CPU profile and ends the process with code.
func exitProcess(code int) {
	stopCPUProfile()
	os.Exit(code)
}

// startCPUProfileFlag looks for --cpuprofile <file> or --cpuprofile=<file>
// after a command that accepts it, starts a Go CPU profile written to that
// file, and returns the arguments without the flag. It returns args unchanged
// when the flag is absent or the command does not accept it.
func startCPUProfileFlag(args []string) ([]string, error) {
	if len(args) < 2 || !cpuProfileCommands[args[1]] {
		return args, nil
	}
	path := ""
	rest := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case i >= 2 && (arg == "--cpuprofile" || arg == "-cpuprofile"):
			if i+1 >= len(args) || args[i+1] == "" {
				return args, fmt.Errorf("--cpuprofile needs a file")
			}
			path = args[i+1]
			i++
		case i >= 2 && (strings.HasPrefix(arg, "--cpuprofile=") || strings.HasPrefix(arg, "-cpuprofile=")):
			_, path, _ = strings.Cut(arg, "=")
			if path == "" {
				return args, fmt.Errorf("--cpuprofile needs a file")
			}
		default:
			rest = append(rest, arg)
		}
	}
	if path == "" {
		return args, nil
	}
	file, err := os.Create(path)
	if err != nil {
		return args, fmt.Errorf("--cpuprofile: %w", err)
	}
	if err := pprof.StartCPUProfile(file); err != nil {
		file.Close()
		return args, fmt.Errorf("--cpuprofile: %w", err)
	}
	stopCPUProfile = func() {
		pprof.StopCPUProfile()
		if err := file.Close(); err != nil {
			fmt.Fprintf(os.Stderr, "cicada: --cpuprofile: %v\n", err)
		}
		stopCPUProfile = func() {}
	}
	return rest, nil
}
