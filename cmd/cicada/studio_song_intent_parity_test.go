package main

import (
	"bytes"
	"encoding/json"
	"strconv"
	"testing"

	edits "m31labs.dev/cicada/edit"
)

func TestSongWriterParity(t *testing.T) {
	for _, newline := range []string{"\n", "\r\n"} {
		for _, body := range []studioEdit{
			{Action: "move", Index: 0, Target: 2}, {Action: "move", Index: 2, Target: 0}, {Action: "move", Index: 1, Target: 1},
			{Action: "bars", Index: 0, Bars: 4}, {Action: "bars", Index: 1, Bars: 1}, {Action: "bars", Index: 1, Bars: 4},
			{Action: "append", Scene: "chorus", Bars: 2}, {Action: "duplicate", Index: 1}, {Action: "delete", Index: 0},
			{Action: "scene", Index: 0, Scene: "chorus"}, {Action: "bars", Index: 5, Bars: 4},
			{Action: "move", Index: 0, Target: 5}, {Action: "bars", Index: 0}, {Action: "append", Scene: "missing", Bars: 2},
		} {
			kind := map[string]string{"move": "movesongentry", "bars": "setsongbars", "append": "appendsongentry", "duplicate": "duplicatesongentry", "delete": "deletesongentry", "scene": "setsongscene"}[body.Action]
			fields := map[string]any{"kind": kind}
			if body.Action != "append" {
				fields["entity"] = edits.EntityID("song:" + strconv.Itoa(body.Index))
			}
			switch body.Action {
			case "move":
				fields["target"] = body.Target
			case "bars":
				fields["bars"] = body.Bars
			case "append":
				fields["scene"], fields["bars"] = body.Scene, body.Bars
			case "scene":
				fields["scene"] = body.Scene
			}
			raw, _ := json.Marshal(fields)
			source := bytes.ReplaceAll([]byte(studioSongScore), []byte("\n"), []byte(newline))
			assertWriterParity(t, source, string(raw), studioEditLabel(body), func(s []byte) ([]byte, error) {
				return editedSongBlockSource(s, body.Action, body.Index, body.Target, body.Bars, body.Scene)
			})
		}
	}
}
