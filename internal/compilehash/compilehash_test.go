package compilehash

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"
)

// TestHashesMatchReference compiles every score that a reference hash file
// lists with an "image" entry, and compares the portable kernel images. The
// file is the one cmd/cicada's TestExampleWAVParity writes on amd64 (lines
// "score mode sum exit=status"). CI runs this test under Go's js/wasm (GOOS=js
// GOARCH=wasm, with go_js_wasm_exec and Node), so an image built by WebAssembly,
// as the hosted demo builds it in a browser, must equal amd64's byte for byte.
//
// The engine configurations are not compared here: their fixed arrays are
// sized by kernel/limits for the build (64 tracks natively, 16 in a js or
// TinyGo build), so their JSON differs by construction. The image is the
// portable format the browser receives; amd64 and arm64 compare both.
//
// GOMAXPROCS must be unset or 1 (wasm_exec passes the environment through, and
// js/wasm cannot start threads):
//
//	CICADA_COMPILE_HASHES_REFERENCE=$PWD/build/parity/wav-hashes.txt \
//	  PATH="$(go env GOROOT)/lib/wasm:$PATH" GOOS=js GOARCH=wasm \
//	  env -u GOMAXPROCS go test ./internal/compilehash -run '^TestHashesMatchReference$' -count=1 -v
func TestHashesMatchReference(t *testing.T) {
	path := os.Getenv("CICADA_COMPILE_HASHES_REFERENCE")
	if path == "" {
		t.Skip("set CICADA_COMPILE_HASHES_REFERENCE to a hash file from TestExampleWAVParity")
	}
	want, err := readReference(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(want) == 0 {
		t.Fatalf("%s has no config or image entries", path)
	}
	root := repositoryRoot(t)
	scores := map[string]bool{}
	for key := range want {
		if score, mode, _ := strings.Cut(key, " "); mode == "image" {
			scores[score] = true
		}
	}
	start := time.Now()
	same, none := 0, 0
	var problems []string
	for score := range scores {
		_, got := Score(root, score)
		key := score + " image"
		reference := want[key]
		known, isKnown := portableImageDifferences[key]
		switch {
		case isKnown && got == known.here && reference == known.reference:
			none++ // listed: neither side has an image, and the failing step differs as stated
		case isKnown:
			problems = append(problems, fmt.Sprintf("%s: listed in portableImageDifferences (%s) but now reference %s, here %s; update or remove the entry", key, known.cause, reference, got))
		case got != reference:
			problems = append(problems, fmt.Sprintf("%s: reference %s, here %s", key, reference, got))
		default:
			same++
			if got.Sum == "none" {
				none++
			}
		}
	}
	sort.Strings(problems)
	for _, p := range problems {
		t.Error(p)
	}
	t.Logf("%d of %d images identical to %s, %d with no image on either side, in %s", same, len(scores), path, none, time.Since(start).Round(time.Millisecond))
}

// portableImageDifferences lists the images whose result differs between a
// native build and a js or TinyGo build, keyed "score image". Each must still
// differ exactly as listed, so the list can only shrink.
var portableImageDifferences = map[string]struct {
	reference, here Entry
	cause           string
}{
	"examples/songs/anywhere-you-are.cicada image": {
		reference: Entry{Sum: "none", Status: StatusImageEncode},
		here:      Entry{Sum: "none", Status: StatusLoad},
		cause:     "the song allocates 52 voices; natively the configuration compiles and kernelimage.Encode refuses it (more than 32), while under the portable limits project.FromScore already refuses it; neither has an image",
	},
}

// readReference keeps the "score config" and "score image" lines of a hash file.
func readReference(path string) (map[string]Entry, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	entries := map[string]Entry{}
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) != 4 || (fields[1] != "config" && fields[1] != "image") || !strings.HasPrefix(fields[3], "exit=") {
			continue
		}
		status, err := strconv.Atoi(strings.TrimPrefix(fields[3], "exit="))
		if err != nil {
			return nil, err
		}
		entries[fields[0]+" "+fields[1]] = Entry{Sum: fields[2], Status: status}
	}
	return entries, scanner.Err()
}

func repositoryRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("go.mod not found above the test directory")
		}
		dir = parent
	}
}
