package main

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
)

func findStudioWorkstation() (string, error) {
	name := "cicada-workstation"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	self, err := os.Executable()
	if err == nil {
		candidate := filepath.Join(filepath.Dir(self), name)
		if info, e := os.Stat(candidate); e == nil && !info.IsDir() {
			return candidate, nil
		}
	}
	if candidate, e := exec.LookPath(name); e == nil {
		return candidate, nil
	}
	return "", fmt.Errorf("GoSX workstation executable is missing; run make build or install cicada-workstation next to cicada")
}

func launchStudioWorkstation(path, backend, address, token string, output io.Writer) (func(), <-chan error, error) {
	reader, writer, err := os.Pipe()
	if err != nil {
		return nil, nil, err
	}
	command := exec.Command(path, "--backend", backend, "--listen", address, "--parent-watch")
	configureStudioWorkstation(command)
	command.Env = append(os.Environ(), "CICADA_BACKEND_TOKEN="+token)
	command.Stdin = reader
	command.Stdout = output
	command.Stderr = os.Stderr
	if err := command.Start(); err != nil {
		reader.Close()
		writer.Close()
		return nil, nil, err
	}
	reader.Close()
	finished := make(chan error, 1)
	go func() { finished <- command.Wait() }()
	var once sync.Once
	return func() { once.Do(func() { _ = writer.Close() }) }, finished, nil
}

// The private domain service accepts only its GoSX child. Browser sessions
// mutate through GoSX actions and cannot bypass their CSRF checks.
func privateStudioService(handler http.Handler) (http.Handler, string, error) {
	var secret [32]byte
	if _, err := rand.Read(secret[:]); err != nil {
		return nil, "", err
	}
	token := hex.EncodeToString(secret[:])
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), []byte("Bearer "+token)) != 1 {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		handler.ServeHTTP(w, r)
	}), token, nil
}
