package main

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"testing"
)

const keysScore = "cicada 2\ntempo 120\ntrack keys fm_bass {\n  op3_level = 4\n  op1_attack = 0.01\n}\npattern p notes { c3 . }\nscene s { keys = p }\nsong { s }\n"

type catalogControl struct {
	ID         string   `json:"id"`
	Owner      string   `json:"owner"`
	Name       string   `json:"name"`
	Type       string   `json:"type"`
	Unit       string   `json:"unit"`
	Min        *float64 `json:"min"`
	Max        *float64 `json:"max"`
	Default    any      `json:"default"`
	Example    string   `json:"example"`
	SetIn      []string `json:"set_in"`
	Live       bool     `json:"live"`
	RegistryID string   `json:"registry_id"`
	Meaning    string   `json:"meaning"`
	Directions []struct {
		Property string `json:"property"`
		Change   string `json:"change"`
		Basis    string `json:"basis"`
	} `json:"directions"`
	Value       any    `json:"value"`
	ValueSource string `json:"value_source"`
	Overridden  bool   `json:"overridden"`
}

// params --json lists every keyboard patch control, which the registry rows
// alone never did, with its range, default and meaning.
func TestParamsJSONListsEveryPatchControl(t *testing.T) {
	var stdout bytes.Buffer
	if err := paramsCommand([]string{"--json"}, &stdout); err != nil {
		t.Fatal(err)
	}
	document := oneJSONDocument(t, "params --json", stdout.Bytes())
	var catalog struct {
		Schema string `json:"schema"`
		Counts struct {
			Controls int `json:"controls"`
			Owners   int `json:"owners"`
		} `json:"counts"`
		Controls []catalogControl `json:"controls"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &catalog); err != nil {
		t.Fatal(err)
	}
	if catalog.Schema != paramsSchema || catalog.Counts.Controls != len(catalog.Controls) || catalog.Counts.Controls < 800 || document["owners"] == nil {
		t.Fatalf("params --json: schema %q, %d controls counted, %d listed", catalog.Schema, catalog.Counts.Controls, len(catalog.Controls))
	}
	byID := map[string]catalogControl{}
	for _, c := range catalog.Controls {
		byID[c.ID] = c
	}
	op2 := byID["fm_bass.op2_level"]
	if op2.Type != "number" || op2.Min == nil || *op2.Min != 0 || *op2.Max != 8 || op2.Default != 0.6 || op2.Meaning == "" || op2.SetIn[0] != "track" {
		t.Fatalf("fm_bass.op2_level: %+v", op2)
	}
	if gain := byID["fm_bass.gain"]; gain.RegistryID != "keys.fm.gain" || !gain.Live || gain.Default != 0.35 {
		t.Fatalf("fm_bass.gain keeps its registry row and the patch default: %+v", gain)
	}
	stdout.Reset()
	if err := paramsCommand([]string{"fm_bass.op3", "--json"}, &stdout); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(stdout.Bytes(), &catalog); err != nil || len(catalog.Controls) != 12 {
		t.Fatalf("filter fm_bass.op3 keeps operator 3's 12 controls: %d, %v", len(catalog.Controls), err)
	}
}

func TestExplainJSONForControlsOwnersAndTracks(t *testing.T) {
	dir := resolvedTempDir(t)
	t.Chdir(dir)
	writeContractFile(t, filepath.Join(dir, "song.cicada"), keysScore)
	var stdout bytes.Buffer
	if err := explainCommand([]string{"fm_bass.op3_level", "--json"}, &stdout); err != nil {
		t.Fatal(err)
	}
	var control struct {
		Schema  string         `json:"schema"`
		Kind    string         `json:"kind"`
		Control catalogControl `json:"control"`
		Owner   struct {
			Name string `json:"name"`
			Kind string `json:"kind"`
		} `json:"owner"`
	}
	oneJSONDocument(t, "explain control", stdout.Bytes())
	if err := json.Unmarshal(stdout.Bytes(), &control); err != nil || control.Kind != "control" || control.Control.ID != "fm_bass.op3_level" || control.Owner.Kind != "patch" {
		t.Fatalf("explain fm_bass.op3_level --json: %v %+v", err, control)
	}
	stdout.Reset()
	if err := explainCommand([]string{"delay", "--json"}, &stdout); err != nil {
		t.Fatal(err)
	}
	var owner struct {
		Kind     string           `json:"kind"`
		Controls []catalogControl `json:"controls"`
	}
	oneJSONDocument(t, "explain owner", stdout.Bytes())
	if err := json.Unmarshal(stdout.Bytes(), &owner); err != nil || owner.Kind != "owner" || len(owner.Controls) != 6 {
		t.Fatalf("explain delay --json: %v %+v", err, owner)
	}
	stdout.Reset()
	if err := explainCommand([]string{"track:keys", "song.cicada", "--json"}, &stdout); err != nil {
		t.Fatal(err)
	}
	var track struct {
		Kind  string `json:"kind"`
		Score string `json:"score"`
		Track struct {
			Track     string           `json:"track"`
			Owner     string           `json:"owner"`
			OwnerKind string           `json:"owner_kind"`
			Controls  []catalogControl `json:"controls"`
		} `json:"track"`
	}
	oneJSONDocument(t, "explain track", stdout.Bytes())
	if err := json.Unmarshal(stdout.Bytes(), &track); err != nil || track.Kind != "track" || track.Score != "song.cicada" || track.Track.Owner != "fm_bass" {
		t.Fatalf("explain track:keys: %v %+v", err, track)
	}
	values := map[string]catalogControl{}
	for _, c := range track.Track.Controls {
		values[c.Owner+"."+c.Name] = c
	}
	if c := values["fm_bass.op3_level"]; c.Value != 4.0 || !c.Overridden || c.ValueSource != "track" {
		t.Fatalf("op3_level: %+v", c)
	}
	if c := values["fm_bass.op1_attack"]; c.Value != 10.0 || c.Unit != "ms" || !c.Overridden {
		t.Fatalf("op1_attack: a plain keyboard time is seconds, shown in ms: %+v", c)
	}
	if c := values["fm_bass.op2_level"]; c.Value != 0.6 || c.Overridden || c.ValueSource != "default" {
		t.Fatalf("op2_level keeps the patch default: %+v", c)
	}
	if c := values["mix.level"]; c.Owner != "mix" {
		t.Fatalf("the mixer strip is listed: %+v", c)
	}
}
