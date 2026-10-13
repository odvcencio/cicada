package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestStudioAndLSPShareOneProcess(t *testing.T) {
	dir := t.TempDir()
	binary := filepath.Join(dir, "cicada")
	build := exec.Command("go", "build", "-o", binary, ".")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, output)
	}
	score := filepath.Join(dir, "main.cicada")
	if err := os.WriteFile(score, []byte(studioScore), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	// This core test owns the native/LSP service. The GoSX module exercises
	// the public app separately, without changing the core's toolchain.
	cmd := exec.CommandContext(ctx, binary, "studio", score, "--service", "--lsp-stdio")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	finished := false
	defer func() {
		if !finished {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}
	}()
	address, err := bufio.NewReader(stderr).ReadString('\n')
	if err != nil || !strings.HasPrefix(address, "Cicada Studio: http://127.0.0.1:") {
		t.Fatalf("studio address: %q, %v", address, err)
	}
	url := strings.TrimSpace(strings.TrimPrefix(address, "Cicada Studio: "))
	response, err := http.Get(url + "api/state")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("studio state: %d", response.StatusCode)
	}
	body, _ := io.ReadAll(response.Body)
	if !strings.Contains(string(body), `"valid":true`) {
		t.Fatalf("studio state: %s", body)
	}
	message, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "initialize", "params": map[string]any{}})
	if _, err := fmt.Fprintf(stdin, "Content-Length: %d\r\n\r\n%s", len(message), message); err != nil {
		t.Fatal(err)
	}
	reader := bufio.NewReader(stdout)
	header, err := reader.ReadString('\n')
	if err != nil || !strings.HasPrefix(header, "Content-Length: ") {
		t.Fatalf("LSP stdout header: %q, %v", header, err)
	}
	length, err := strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(header, "Content-Length: ")))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := reader.ReadString('\n'); err != nil {
		t.Fatal(err)
	}
	lspBody := make([]byte, length)
	if _, err := io.ReadFull(reader, lspBody); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(lspBody), `"hoverProvider":true`) {
		t.Fatalf("LSP initialize: %s", lspBody)
	}
	_ = stdin.Close()
	if err := cmd.Wait(); err != nil {
		t.Fatalf("Studio did not exit with LSP input: %v", err)
	}
	finished = true
}

// A bad request on the stdio LSP wire gets an error reply and Studio keeps serving.
func TestStudioLSPSurvivesNumericCodesAndBadParams(t *testing.T) {
	dir := t.TempDir()
	binary := filepath.Join(dir, "cicada")
	if output, err := exec.Command("go", "build", "-o", binary, ".").CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, output)
	}
	score := filepath.Join(dir, "main.cicada")
	if err := os.WriteFile(score, []byte(studioScore), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, binary, "studio", score, "--service", "--lsp-stdio")
	stdin, _ := cmd.StdinPipe()
	stdout, _ := cmd.StdoutPipe()
	stderr, _ := cmd.StderrPipe()
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	finished := false
	defer func() {
		if !finished {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}
	}()
	address, err := bufio.NewReader(stderr).ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	url := strings.TrimSpace(strings.TrimPrefix(address, "Cicada Studio: "))
	reader := bufio.NewReader(stdout)
	send := func(raw string) {
		t.Helper()
		if _, err := fmt.Fprintf(stdin, "Content-Length: %d\r\n\r\n%s", len(raw), raw); err != nil {
			t.Fatal(err)
		}
	}
	read := func() string {
		t.Helper()
		header, err := reader.ReadString('\n')
		if err != nil {
			t.Fatalf("LSP session ended: %v", err)
		}
		length, _ := strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(header, "Content-Length: ")))
		_, _ = reader.ReadString('\n')
		body := make([]byte, length)
		if _, err := io.ReadFull(reader, body); err != nil {
			t.Fatal(err)
		}
		return string(body)
	}
	uri := "file://" + filepath.ToSlash(score)
	send(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"capabilities":{"workspace":{"workspaceEdit":{"documentChanges":true}}}}}`)
	read()
	send(`{"jsonrpc":"2.0","method":"textDocument/didOpen","params":{"textDocument":{"uri":"` + uri + `","version":1,"text":"title \"x\"\n"}}}`)
	read() // diagnostics
	send(`{"jsonrpc":"2.0","id":2,"method":"textDocument/codeAction","params":{"textDocument":{"uri":"` + uri + `"},"range":{"start":{"line":0,"character":0},"end":{"line":0,"character":0}},"context":{"diagnostics":[{"range":{"start":{"line":0,"character":0},"end":{"line":0,"character":1}},"code":42,"message":"other tool"}]}}}`)
	if reply := read(); !strings.Contains(reply, `"id":2`) || strings.Contains(reply, `"error"`) {
		t.Fatalf("numeric code: %s", reply)
	}
	send(`{"jsonrpc":"2.0","id":3,"method":"textDocument/hover","params":{"textDocument":7}}`)
	if reply := read(); !strings.Contains(reply, `"id":3`) || !strings.Contains(reply, `-32602`) {
		t.Fatalf("bad params: %s", reply)
	}
	send(`{"jsonrpc":"2.0","id":4,"method":"shutdown","params":null}`)
	if reply := read(); !strings.Contains(reply, `"id":4`) {
		t.Fatalf("session did not continue: %s", reply)
	}
	response, err := http.Get(url + "api/state")
	if err != nil || response.StatusCode != http.StatusOK {
		t.Fatalf("Studio stopped serving: %v", err)
	}
	response.Body.Close()
	_ = stdin.Close()
	if err := cmd.Wait(); err != nil {
		t.Fatal(err)
	}
	finished = true
}
