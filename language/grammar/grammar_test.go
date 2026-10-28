package grammar

import (
	"bytes"
	"compress/gzip"
	"go/version"
	"io"
	"os"
	"runtime"
	"testing"

	gotreesitter "github.com/odvcencio/gotreesitter"
	"github.com/odvcencio/gotreesitter/grammargen"
)

// TestBlobIsGenerated fails when notation/cicada.bin is stale. Regenerate
// it with `go generate ./notation`.
func TestBlobIsGenerated(t *testing.T) {
	blob, err := grammargen.Generate(Cicada())
	if err != nil {
		t.Fatal(err)
	}
	committed, err := os.ReadFile("../../notation/cicada.bin")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(blob, committed) {
		// Go 1.27 changed compress/flate's encoded bytes. The canonical SDK
		// lane still requires an identical container; newer native SDKs must
		// preserve every decompressed byte, version field, and trailer flag.
		if version.Compare(runtime.Version(), "go1.27") < 0 {
			t.Fatal("notation/cicada.bin differs from the grammar; run go generate ./notation")
		}
		equal, err := grammarPayloadEqual(blob, committed)
		if err != nil {
			t.Fatal(err)
		}
		if !equal {
			t.Fatal("notation/cicada.bin payload or version metadata differs from the grammar")
		}
	}
}

func grammarPayloadEqual(left, right []byte) (bool, error) {
	type payload struct {
		info    gotreesitter.LanguageBlobInfo
		trailer bool
		bytes   []byte
	}
	decode := func(data []byte) (payload, error) {
		body, info, err := gotreesitter.UnwrapLanguageBlobVersionHeader(data)
		if err != nil {
			return payload{}, err
		}
		compressed, trailer, err := gotreesitter.UnwrapLanguageBlobEnvelope(body)
		if err != nil {
			return payload{}, err
		}
		reader, err := gzip.NewReader(bytes.NewReader(compressed))
		if err != nil {
			return payload{}, err
		}
		raw, err := io.ReadAll(reader)
		closeErr := reader.Close()
		if err != nil {
			return payload{}, err
		}
		if closeErr != nil {
			return payload{}, closeErr
		}
		return payload{info: info, trailer: trailer, bytes: raw}, nil
	}
	a, err := decode(left)
	if err != nil {
		return false, err
	}
	b, err := decode(right)
	if err != nil {
		return false, err
	}
	return a.info == b.info && a.trailer == b.trailer && bytes.Equal(a.bytes, b.bytes), nil
}

func TestGrammarPayloadComparisonRejectsChangesAndCorruption(t *testing.T) {
	wrap := func(raw, generator string) []byte {
		var compressed bytes.Buffer
		writer := gzip.NewWriter(&compressed)
		if _, err := writer.Write([]byte(raw)); err != nil {
			t.Fatal(err)
		}
		if err := writer.Close(); err != nil {
			t.Fatal(err)
		}
		blob, err := gotreesitter.WrapLanguageBlobVersionHeader(compressed.Bytes(), 1, generator)
		if err != nil {
			t.Fatal(err)
		}
		return blob
	}
	original := wrap("grammar payload", "gotreesitter")
	for _, changed := range [][]byte{wrap("changed grammar", "gotreesitter"), wrap("grammar payload", "different generator")} {
		if equal, err := grammarPayloadEqual(original, changed); err != nil || equal {
			t.Fatalf("changed content/metadata accepted: equal=%v err=%v", equal, err)
		}
	}
	corrupt := bytes.Clone(original)
	corrupt[len(corrupt)-1] ^= 1
	if _, err := grammarPayloadEqual(original, corrupt); err == nil {
		t.Fatal("corrupt gzip checksum accepted")
	}
}

func TestGrammarValidatesAndPassesItsTests(t *testing.T) {
	g := Cicada()
	if warnings := grammargen.Validate(g); len(warnings) > 0 {
		t.Fatalf("grammar warnings: %v", warnings)
	}
	if err := grammargen.RunTests(g); err != nil {
		t.Fatal(err)
	}
}
