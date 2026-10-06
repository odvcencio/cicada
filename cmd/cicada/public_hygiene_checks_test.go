package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestPublicHygieneRejectsViolations(t *testing.T) {
	tests := []struct{ name, path, content string }{
		{"directory", "docs/" + "design/example.pdf", ""},
		{"filename", "docs/manual/roadmap.md", ""},
		{"measurement file", "docs/audio/decode-report.json", "{}"},
		{"evidence directory", "experimental/expressive/evidence/metrics.json", "{}"},
		{"heading", "docs/manual/example.md", "# Road" + "map\n"},
		{"setext heading", "docs/manual/example.md", "Coming " + "next\n===========\n"},
		{"fence", "docs/manual/example.md", "```cicada-" + "accepted\n"},
		{"process comment", "example.go", "// roadmap" + " lane\n"},
		{"approval comment", "example.js", "/* owner-" + "accepted */\n"},
		{"approval documentation", "README.md", "owner " + "approved\n"},
		{"listening process", "example.go", "// listening " + "acceptance\n"},
		{"attribution comment", "example.go", "// Co-" + "Authored-By: Tool\n"},
		{"generation comment", "example.js", "// Generated " + "with Tool\n"},
		{"address documentation", "README.md", "noreply@" + "anthropic.com\n"},
		{"recording copy", "example.html", publicTextRules[0].pattern.String()},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if len(publicHygieneViolations(test.path, []byte(test.content))) == 0 {
				t.Fatal("seeded violation was accepted")
			}
		})
	}
	for _, path := range []string{"README.md", "docs/spec/edition-2.md", "example.go"} {
		if got := publicHygieneViolations(path, []byte("## Live command and status buffers\nEach drum lane has its own voice. Later notes retrigger it.\n")); len(got) != 0 {
			t.Fatalf("technical wording rejected: %v", got)
		}
	}
}

func TestCommitHygiene(t *testing.T) {
	script := filepath.Join(repositoryRoot(), "scripts", "check-commit-hygiene.sh")
	dir := t.TempDir()
	git := func(input string, args ...string) string {
		t.Helper()
		command := exec.Command("git", args...)
		command.Dir = dir
		command.Stdin = strings.NewReader(input)
		output, err := command.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, output)
		}
		return strings.TrimSpace(string(output))
	}
	git("", "init", "--quiet")
	tree := git("", "mktree")
	address := strings.Join([]string{"contributor", "example.invalid"}, "@")
	commit := func(parent, message, author, committer string) string {
		t.Helper()
		args := []string{"-c", "commit.gpgsign=false", "commit-tree", tree}
		if parent != "" {
			args = append(args, "-p", parent)
		}
		command := exec.Command("git", args...)
		command.Dir = dir
		command.Stdin = strings.NewReader(message)
		command.Env = append(os.Environ(), "GIT_AUTHOR_NAME=Contributor", "GIT_COMMITTER_NAME=Contributor", "GIT_AUTHOR_EMAIL="+author, "GIT_COMMITTER_EMAIL="+committer)
		output, err := command.CombinedOutput()
		if err != nil {
			t.Fatalf("commit fixture: %v\n%s", err, output)
		}
		return strings.TrimSpace(string(output))
	}
	base := commit("", "Initial fixture", address, address)
	for _, test := range []struct {
		name, message, author, committer string
		fail                             bool
	}{
		{"clean", "Update recording instructions", address, address, false},
		{"human trailer", "Fix playback\n\nCo-" + "Authored-By: Contributor", address, address, false},
		{"author", "Update instructions", "noreply@" + "anthropic.com", address, true},
		{"committer", "Update instructions", address, "noreply@" + "anthropic.com", true},
		{"coauthor", "Update instructions\n\nCo-" + "Authored-By: Claude", address, address, true},
		{"generated", "Update instructions\n\nGenerated " + "with Codex", address, address, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			first := commit(base, test.message, test.author, test.committer)
			head := commit(first, "A later clean commit", address, address)
			command := exec.Command("bash", script, base, head)
			command.Dir = dir
			output, err := command.CombinedOutput()
			if test.fail {
				if err == nil || !strings.Contains(string(output), "prohibited") {
					t.Fatalf("seeded commit accepted: %v\n%s", err, output)
				}
				t.Logf("seeded range rejected: %v\n%s", err, output)
			} else if err != nil {
				t.Fatalf("clean range rejected: %v\n%s", err, output)
			}
		})
	}
}
