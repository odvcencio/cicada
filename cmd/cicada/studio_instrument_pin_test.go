package main

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// pinScore has an authored instrument "pad" on track keys, the built-in acid on
// track bass, effects, buses and a master block.
const pinScore = `cicada 2

title "Mixer"

tempo 120

key a minor

fx room delay {
  feedback = 0.35
}

fx space reverb {}

fx grit drive {}

fx glue comp {}

bus music {
  insert = glue
}

master {
  level = -1dB
}

instrument pad {
  octave = 3
  param cutoff = 540Hz
  voice mono {
    let body = saw(pitch)
    let shape = env(gate, 260ms)
    out = ladder(body, cutoff, 0.48) * shape
  }
}

track bass acid {
  // stored level
  level = -6dB
  insert = grit
  send room = 0.25
  out = music
}

track keys pad {
  cutoff = 620Hz
}

pattern pulse acid {
  1 . 5 .
}

pattern chords {
  1 . 3 .
}

scene main {
  bass = pulse
  keys = chords
}

song {
  main
}
`

func pinCollide(name string) string {
	fixture := strings.Replace(pinScore, "track keys pad", "track "+name+" pad", 1)
	return strings.Replace(fixture, "  keys = chords", "  "+name+" = chords", 1)
}

// TestRouteParity_InstrumentRefusesMixerNames pins /api/instrument to the
// instrument writer: a mixer-named field never reaches the mixer writer. Texts
// are main's.
func TestRouteParity_InstrumentRefusesMixerNames(t *testing.T) {
	value := func(v any) json.RawMessage { raw, _ := json.Marshal(v); return raw }
	declares := func(name string) string { return `instrument pad does not declare parameter "` + name + `"` }
	noInstrument := func(track string) string { return `track "` + track + `" has no authored instrument` }
	for _, c := range []struct {
		fixture, action, track, name string
		value                        json.RawMessage
		want                         string
	}{
		{pinScore, "set-parameter", "keys", "level", value(-3), declares("level")},
		{pinScore, "reset-parameter", "keys", "level", nil, declares("level")},
		{pinScore, "set-parameter", "keys", "level", value("off"), declares("level")},
		{pinScore, "set-parameter", "keys", "pan", value(0.3), declares("pan")},
		{pinScore, "set-parameter", "keys", "mute", value("on"), declares("mute")},
		{pinScore, "set-parameter", "keys", "solo", value("on"), declares("solo")},
		{pinScore, "set-parameter", "keys", "insert", value("grit"), declares("insert")},
		{pinScore, "set-parameter", "keys", "out", value("music"), declares("out")},
		{pinScore, "set-parameter", "keys", "send.room", value(0.4), declares("send.room")},
		{pinScore, "set-parameter", "keys", "send", value(0.4), declares("send")},
		{pinScore, "set-parameter", "bass", "level", value(-3), noInstrument("bass")},
		{pinScore, "reset-parameter", "bass", "level", nil, noInstrument("bass")},
		{pinScore, "set-parameter", "bass", "send.room", value(0.5), noInstrument("bass")},
		{pinScore, "set-parameter", "master", "level", value(-2), noInstrument("master")},
		{pinScore, "set-parameter", "music", "level", value(-2), noInstrument("music")},
		{pinScore, "set-parameter", "music", "insert", value("none"), noInstrument("music")},
		{pinScore, "set-parameter", "room", "feedback", value(0.5), noInstrument("room")},
		{pinScore, "set-parameter", "glue", "threshold", value(-10), noInstrument("glue")},
		{pinScore, "set-parameter", "fx", "tape", value("drive"), noInstrument("fx")},
		{pinScore, "set-parameter", "fx", "room", value("delay"), noInstrument("fx")},
		{pinScore, "set-parameter", "ghost", "level", value(-3), noInstrument("ghost")},
		{pinCollide("master"), "set-parameter", "master", "level", value(-3), declares("level")},
		{pinCollide("master"), "set-parameter", "master", "insert", value("none"), declares("insert")},
		{pinCollide("fx"), "set-parameter", "fx", "level", value(-3), declares("level")},
		{pinCollide("fx"), "set-parameter", "fx", "feedback", value(0.5), declares("feedback")},
	} {
		assertRoute(t, []byte(c.fixture), "/api/instrument", studioEdit{Action: c.action, Track: c.track, NewName: c.name, Value: c.value}, 422, nil, "", c.want)
	}
}

func TestRouteParity_InstrumentKeepsDeclaredMixerNames(t *testing.T) {
	fixture := []byte("cicada 2\n\ninstrument wob {\n  octave = 3\n  param pan = 0.5\n  param level = 0.5\n  voice mono {\n    let shape = env(gate, 100ms)\n    out = saw(pitch) * shape * level * pan\n  }\n}\n\ntrack lead wob {}\n\npattern line {\n  1 . 3 .\n}\n\nscene main {\n  lead = line\n}\n\nsong {\n  main\n}\n")
	handler, path := studioMixerTestHandlerBytes(t, fixture)
	r := studioCall(t, handler, "/api/instrument", studioEdit{Revision: studioRevision(fixture), Action: "set-parameter", Track: "lead", NewName: "pan", Value: json.RawMessage(`0.3`)})
	if r.Code != 200 {
		t.Fatalf("%d %s", r.Code, r.Body.String())
	}
	if h := studioHistoryEdits(t, handler); len(h) != 1 || h[0].Label != "Set lead.pan" {
		t.Fatalf("%+v", h)
	}
	_ = path
}

func studioMixerTestHandlerBytes(t *testing.T, source []byte) (http.Handler, string) {
	t.Helper()
	return studioMixerTestHandler(t, string(source))
}

func TestRouteParity_MixerPathWithoutDotKeepsMainText(t *testing.T) {
	for _, path := range []string{"bass", "fx", "master"} {
		assertRoute(t, []byte(studioMixerScore), "/api/mixer", studioEdit{Path: path, Value: json.RawMessage(`0.5`)}, 422, nil, "", "mixer path \""+path+"\" must name an owner and field")
	}
}
