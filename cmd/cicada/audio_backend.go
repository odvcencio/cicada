package main

import (
	"os"

	"m31labs.dev/cicada/internal/audiobackend"
	"m31labs.dev/cicada/internal/audiobackend/rt"
)

func selectCommandAudio(command string, args []string) ([]string, audiobackend.Name, audiobackend.Backend, error) {
	rest, flagValue, flagSet, err := audiobackend.ParseFlag(args)
	if err != nil {
		return nil, "", nil, err
	}
	name, err := audiobackend.Select(flagValue, flagSet, os.Getenv("CICADA_AUDIO"), audiobackend.DefaultFor(command))
	if err != nil {
		return nil, "", nil, err
	}
	backend, err := audiobackend.New(name)
	if err != nil {
		return nil, "", nil, err
	}
	return rest, name, backend, nil
}

func raiseAudioProcessThreads(name audiobackend.Name) func() {
	if name == audiobackend.Null {
		return func() {}
	}
	restore, _, err := rt.RaiseProcessThreads(1)
	_ = err // Audio remains available when the operating system denies priority.
	if restore == nil {
		return func() {}
	}
	return restore
}
