package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	edits "m31labs.dev/cicada/edit"
	"m31labs.dev/cicada/project"

	"m31labs.dev/cicada/edition"
)

// mixerGolden reads bytes captured from the pre-intent /api/mixer handler.
func mixerGolden(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "mixer-parity", name+".cicada"))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

type mixerRouteReply struct {
	Code    int
	Body    map[string]any
	Score   []byte
	History []string
}

// mixerRoute posts one mixer edit to a fresh studio over the files in dir.
func mixerRoute(t *testing.T, scorePath string, body studioEdit) mixerRouteReply {
	t.Helper()
	source, err := os.ReadFile(scorePath)
	if err != nil {
		t.Fatal(err)
	}
	handler, err := studioHandler(scorePath)
	if err != nil {
		t.Fatal(err)
	}
	body.Revision = studioRevision(source)
	response := studioCall(t, handler, "/api/mixer", body)
	reply := mixerRouteReply{Code: response.Code}
	if err := json.Unmarshal(response.Body.Bytes(), &reply.Body); err != nil {
		t.Fatalf("%s: %v", response.Body.String(), err)
	}
	reply.Score, _ = os.ReadFile(scorePath)
	for _, entry := range studioHistoryEdits(t, handler) {
		reply.History = append(reply.History, entry.Label)
	}
	return reply
}

func mixerProject(t *testing.T, manifest, score string) string {
	t.Helper()
	dir := t.TempDir()
	for name, data := range map[string]string{"cicada.mod": manifest, "main.cicada": score} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return filepath.Join(dir, "main.cicada")
}

func TestRouteParity_Mixer(t *testing.T) {
	fixture := []byte(studioMixerScore)
	cases := []struct{ name, golden, path, value, label string }{
		{"level", "level", "bass.level", `-3.47`, "Mix: bass level -6 dB to -3.5 dB"},
		{"send", "send", "bass.send.room", `{"level":0.5}`, "Mix: bass send room send room = 0.25 to send room = 0.5"},
		{"insert removal", "insert-removal", "bass.insert", `"none"`, "Mix: bass insert grit to none"},
		{"effect creation", "effect-creation", "fx.tape", `{"kind":"drive"}`, "Mix: fx tape absent to drive"},
		{"fader off", "fader-off", "bass.level", `"off"`, "Mix: bass level -6 dB to off"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			assertRoute(t, fixture, "/api/mixer", studioEdit{Path: c.path, Value: json.RawMessage(c.value)}, 200, mixerGolden(t, c.golden), c.label, "")
		})
	}
}

func TestRouteParity_MixerErrors(t *testing.T) {
	fixture := []byte(studioMixerScore)
	for _, c := range []struct{ path, value, want string }{
		{"bass.pan", `2`, "value must be within -1..1"}, {"ghost.level", `-3`, "unknown mixer owner \"ghost\""}, {"bass.send.nowhere", `0.1`, "send references unknown effect nowhere"}, {"fx.tape", `{"kind":"nothing"}`, "unknown effect kind \"nothing\""}, {"fx.room", `{"kind":"delay"}`, "effect room already exists"}, {"bass.cutoff", `0.5`, "unsupported mixer field \"cutoff\""},
	} {
		assertRoute(t, fixture, "/api/mixer", studioEdit{Path: c.path, Value: json.RawMessage(c.value)}, 422, nil, "", c.want)
	}
	// The writer accepts this; the compile step in the commit tail refuses it.
	assertRoute(t, fixture, "/api/mixer", studioEdit{Path: "music.pan", Value: json.RawMessage(`0.4`)}, 422, nil, "", "22:3 CICADA-UNSUPPORTED: music bus pan is not implemented")
	assertRoute(t, fixture, "/api/mixer", studioEdit{Path: "", Value: json.RawMessage(`1`)}, 400, nil, "", "mixer path and value are required")
	assertRoute(t, fixture, "/api/mixer", studioEdit{Path: "bass.level"}, 400, nil, "", "mixer path and value are required")
}

func TestRouteParity_MixerResponseFields(t *testing.T) {
	path := studioTestPath(t, studioMixerScore)
	reply := mixerRoute(t, path, studioEdit{Path: "bass.level", Value: json.RawMessage(`-3.47`)})
	var keys []string
	for key := range reply.Body {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	if got := strings.Join(keys, ","); got != "changedRange,path,playingRevision,preserved,previous,revision,source,valid,value" {
		t.Fatalf("response fields: %s", got)
	}
	if reply.Body["path"] != "bass.level" || reply.Body["value"] != "-3.5 dB" || reply.Body["previous"] != "-6 dB" || reply.Body["source"] != string(reply.Score) || reply.Body["valid"] != true {
		t.Fatalf("response values: %+v", reply.Body)
	}
	if reply.Body["revision"] != studioRevision(reply.Score) || reply.Body["playingRevision"] != studioRevision(reply.Score) {
		t.Fatalf("revisions: %+v", reply.Body)
	}
	changed, _ := reply.Body["changedRange"].(map[string]any)
	if changed["start"] == nil || changed["end"] == nil {
		t.Fatalf("changedRange: %+v", reply.Body["changedRange"])
	}
}

func TestRouteParity_MixerEditionOne(t *testing.T) {
	legacy, err := os.ReadFile(filepath.Join("..", "..", "examples", "fx", "delay-send.cicada"))
	if err != nil {
		t.Fatal(err)
	}
	value := json.RawMessage(`-3.5`)
	t.Run("loose score", func(t *testing.T) {
		path := studioTestPath(t, string(legacy))
		refused := mixerRoute(t, path, studioEdit{Path: "bass.level", Value: value})
		if refused.Code != 409 || refused.Body["code"] != "CICADA-VERSION" || refused.Body["upgradeRequired"] != true ||
			refused.Body["error"] != "This score uses edition 1. Upgrade to edition 2 to save mixer changes?" || !bytes.Equal(refused.Score, legacy) {
			t.Fatalf("unconfirmed: %d %+v", refused.Code, refused.Body)
		}
		confirmed := mixerRoute(t, path, studioEdit{Path: "bass.level", Value: value, ConfirmUpgrade: true})
		want := mixerGolden(t, "edition1-loose")
		if confirmed.Code != 200 || !bytes.Equal(confirmed.Score, want) || len(confirmed.History) != 1 || confirmed.History[0] != "Mix: bass level absent to -3.5 dB" {
			t.Fatalf("confirmed: %d %v\n%s", confirmed.Code, confirmed.History, confirmed.Score)
		}
	})
	t.Run("manifest project", func(t *testing.T) {
		manifest := "project patches\ncicada 1\n"
		headerless := strings.Replace(string(legacy), "cicada 1\n", "", 1)
		path := mixerProject(t, manifest, headerless)
		manifestPath := filepath.Join(filepath.Dir(path), "cicada.mod")
		refused := mixerRoute(t, path, studioEdit{Path: "bass.level", Value: value})
		after, _ := os.ReadFile(manifestPath)
		if refused.Code != 409 || refused.Body["upgradeRequired"] != true || string(after) != manifest {
			t.Fatalf("unconfirmed: %d %+v %q", refused.Code, refused.Body, after)
		}
		confirmed := mixerRoute(t, path, studioEdit{Path: "bass.level", Value: value, ConfirmUpgrade: true})
		want := mixerGolden(t, "edition1-project")
		wantManifest, _, err := edition.UpgradeManifestEdition([]byte(manifest))
		after, _ = os.ReadFile(manifestPath)
		if err != nil || confirmed.Code != 200 || !bytes.Equal(confirmed.Score, want) || !bytes.Equal(after, wantManifest) || bytes.HasPrefix(confirmed.Score, []byte("cicada 2")) {
			t.Fatalf("confirmed: %d %v %+v\nmanifest %q\n%s", confirmed.Code, err, confirmed.Body, after, confirmed.Score)
		}
	})
}

func TestRouteParity_MixerLooseScoresAndProjects(t *testing.T) {
	headerless := strings.Replace(studioMixerScore, "cicada 2\n\n", "", 1)
	want, wantHeaderless := mixerGolden(t, "level"), mixerGolden(t, "headerless-level")
	t.Run("loose cicada 2", func(t *testing.T) {
		reply := mixerRoute(t, studioTestPath(t, studioMixerScore), studioEdit{Path: "bass.level", Value: json.RawMessage(`-3.47`)})
		if reply.Code != 200 || !bytes.Equal(reply.Score, want) {
			t.Fatalf("%d %v\n%s", reply.Code, reply.Body["error"], reply.Score)
		}
	})
	t.Run("edition 2 project", func(t *testing.T) {
		reply := mixerRoute(t, mixerProject(t, "project mixer\ncicada 2\n", headerless), studioEdit{Path: "bass.level", Value: json.RawMessage(`-3.47`)})
		if reply.Code != 200 || !bytes.Equal(reply.Score, wantHeaderless) || bytes.HasPrefix(reply.Score, []byte("cicada 2")) {
			t.Fatalf("%d %v\n%s", reply.Code, reply.Body["error"], reply.Score)
		}
	})
	t.Run("manifest project with a second source", func(t *testing.T) {
		path := mixerProject(t, "project mixer\ncicada 2\nentry \"main.cicada\"\nsource \"extra.cicada\"\n", headerless)
		extra := filepath.Join(filepath.Dir(path), "extra.cicada")
		if err := os.WriteFile(extra, []byte("pattern extra acid { 1 . }\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		reply := mixerRoute(t, path, studioEdit{Path: "fx.tape", Value: json.RawMessage(`{"kind":"drive"}`)})
		if reply.Code != 200 || !bytes.Equal(reply.Score, mixerGolden(t, "headerless-effect")) {
			t.Fatalf("%d %v\n%s", reply.Code, reply.Body["error"], reply.Score)
		}
	})
	t.Run("project whose tracks live in another source", func(t *testing.T) {
		path := copyStudioProject(t)
		before, _ := os.ReadFile(path)
		reply := mixerRoute(t, path, studioEdit{Path: "fx.tape", Value: json.RawMessage(`{"kind":"drive"}`)})
		if reply.Code != 422 || reply.Body["error"] != "score must validate before a mixer edit" || !bytes.Equal(reply.Score, before) {
			t.Fatalf("%d %v", reply.Code, reply.Body["error"])
		}
	})
}

func TestStudioCompilerHandsTheCompileToTheCommitTail(t *testing.T) {
	path := studioTestPath(t, studioMixerScore)
	compiler := &studioCompiler{path: path}
	source := []byte(studioMixerScore)
	if compiler.compiled(source, nil) != nil {
		t.Fatal("nothing compiled yet")
	}
	if _, err := compiler.Compile(source, nil); err != nil {
		t.Fatal(err)
	}
	if compiler.compiled(source, nil) == nil {
		t.Fatal("the compile of these exact bytes should be reusable")
	}
	if compiler.compiled(append([]byte("// changed\n"), source...), nil) != nil {
		t.Fatal("different source must compile again")
	}
	if compiler.compiled(source, []edits.File{{Path: "cicada.mod", After: []byte("x")}}) != nil {
		t.Fatal("different auxiliary files must compile again")
	}
}

func TestIntentRouteCompilesTheCandidateOnce(t *testing.T) {
	calls := 0
	original := studioCompile
	studioCompile = func(path string, source []byte, overrides map[string][]byte) (*project.Project, error) {
		calls++
		return original(path, source, overrides)
	}
	defer func() { studioCompile = original }()
	path := studioTestPath(t, studioMixerScore)
	handler, err := studioHandler(path)
	if err != nil {
		t.Fatal(err)
	}
	calls = 0
	r := studioCall(t, handler, "/api/mixer", studioEdit{Revision: studioRevision([]byte(studioMixerScore)), Path: "bass.level", Value: json.RawMessage(`-3.47`)})
	if r.Code != 200 || calls != 1 {
		t.Fatalf("status %d after %d compiles, want one", r.Code, calls)
	}
}
