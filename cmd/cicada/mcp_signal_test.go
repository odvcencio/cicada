//go:build !windows

package main

import (
	"bufio"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// mcpInitialize is a legacy-era initialize request.
const mcpInitialize = `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"test","version":"1"}}}` + "\n"

// startMCPChild starts cicada mcp --root root as a child process whose
// temporary folder is tmp, checks that it made its stand-in for the user
// library there, and waits for its reply to an initialize request, so its
// signal handling is in place. The caller owns the pipes.
func startMCPChild(t *testing.T, root, tmp string) (*exec.Cmd, io.WriteCloser, io.ReadCloser) {
	t.Helper()
	child := exec.Command(os.Args[0], "-test.run=^TestMainChild$")
	child.Env = append(os.Environ(), mainChildArgs+"="+strings.Join([]string{"mcp", "--root", root}, "\x1f"), "TMPDIR="+tmp, "CICADA_LIBRARY=")
	stdin, err := child.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := child.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = child.Process.Kill(); _ = child.Wait() })
	if _, err := io.WriteString(stdin, mcpInitialize); err != nil {
		t.Fatal(err)
	}
	reply := make(chan error, 1)
	go func() {
		_, err := bufio.NewReader(stdout).ReadString('\n')
		reply <- err
	}()
	select {
	case err := <-reply:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("the server did not answer initialize")
	}
	if len(standIns(t, tmp)) != 1 {
		t.Fatalf("the server made no stand-in folder in %s", tmp)
	}
	return child, stdin, stdout
}

func standIns(t *testing.T, tmp string) []string {
	t.Helper()
	found, err := filepath.Glob(filepath.Join(tmp, "cicada-mcp-no-user-library-*"))
	if err != nil {
		t.Fatal(err)
	}
	return found
}

// waitExit waits for the child and returns its exit code, or fails when a
// signal killed it, since then no cleanup ran.
func waitExit(t *testing.T, child *exec.Cmd) int {
	t.Helper()
	done := make(chan error, 1)
	go func() { done <- child.Wait() }()
	select {
	case err := <-done:
		var exit *exec.ExitError
		if err != nil && !errors.As(err, &exit) {
			t.Fatal(err)
		}
		status := child.ProcessState.Sys().(syscall.WaitStatus)
		if status.Signaled() {
			t.Fatalf("the server died of %v, so its cleanup did not run", status.Signal())
		}
		return status.ExitStatus()
	case <-time.After(30 * time.Second):
		t.Fatal("the server did not exit")
	}
	return -1
}

// TestMCPRemovesItsStandInOnHangUpAndBrokenOutput: the server removes the
// empty stand-in for the user library when its terminal hangs up (SIGHUP) and
// when the client closes the server's output and then sends a request, which
// used to kill it with SIGPIPE before its cleanup ran.
func TestMCPRemovesItsStandInOnHangUpAndBrokenOutput(t *testing.T) {
	root := inTempWorkingDirectory(t)
	if err := newProject("demo", os.WriteFile); err != nil {
		t.Fatal(err)
	}
	project := filepath.Join(root, "demo")

	t.Run("SIGHUP", func(t *testing.T) {
		tmp := t.TempDir()
		child, _, _ := startMCPChild(t, project, tmp)
		if err := child.Process.Signal(syscall.SIGHUP); err != nil {
			t.Fatal(err)
		}
		if code := waitExit(t, child); code != 1 {
			t.Errorf("exit %d after SIGHUP, want 1", code)
		}
		if left := standIns(t, tmp); len(left) != 0 {
			t.Fatalf("the stand-in was left behind: %v", left)
		}
	})

	t.Run("closed output", func(t *testing.T) {
		tmp := t.TempDir()
		child, stdin, stdout := startMCPChild(t, project, tmp)
		if err := stdout.Close(); err != nil {
			t.Fatal(err)
		}
		if _, err := io.WriteString(stdin, `{"jsonrpc":"2.0","id":2,"method":"tools/list"}`+"\n"); err != nil {
			t.Fatal(err)
		}
		if code := waitExit(t, child); code != 1 {
			t.Errorf("exit %d after a reply to a closed output, want 1", code)
		}
		if left := standIns(t, tmp); len(left) != 0 {
			t.Fatalf("the stand-in was left behind: %v", left)
		}
	})
}
