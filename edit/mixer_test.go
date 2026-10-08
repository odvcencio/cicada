package edit

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"m31labs.dev/cicada/internal/paramdefs"
	"m31labs.dev/cicada/notation"
)

const mixerScore = `cicada 2

title "Mixer"

tempo 120

key a minor

fx room delay {
  feedback = 0.35
}

fx space reverb {}

fx grit drive {}

fx edge drive {}

fx glue comp {}

bus music {
  insert = glue
}

bus sfx {}

master {
  level = -1dB
  mute = off
  solo = off
  insert = none
}

track bass acid {
  // stored level
  level = -6dB
  mute = off
  insert = grit
  send room = 0.25
  out = music
}

pattern pulse acid {
  1 . 5 .
}

scene main {
  bass = pulse
}

song {
  main
}
`

func TestStudioMixerWriterEditsInPlaceAndKeepsComments(t *testing.T) {
	updated, before, after, changed, err := mixerSource([]byte(mixerScore), "bass.level", json.RawMessage(`-3.47`))
	if err != nil {
		t.Fatal(err)
	}
	if before != "-6dB" || after != "-3.5dB" {
		t.Fatalf("rounded change %q to %q", before, after)
	}
	if !strings.Contains(string(updated), "// stored level\n  level = -3.5dB") {
		t.Fatalf("in-place value edit lost nearby source: %s", updated)
	}
	if changed.Start == 0 || changed.End != changed.Start {
		t.Fatalf("changed source range: %+v", changed)
	}
	if string(updated) == string(mixerScore) {
		t.Fatal("writer did not update source")
	}
}

func TestStudioMixerWriterInsertsInFormatterOrderAndIsIdempotent(t *testing.T) {
	updated, _, _, _, err := mixerSource([]byte(mixerScore), "bass.pan", json.RawMessage(`0.256`))
	if err != nil {
		t.Fatal(err)
	}
	track := strings.Index(string(updated), "track bass acid {")
	level := strings.Index(string(updated[track:]), "level = -6dB")
	pan := strings.Index(string(updated[track:]), "pan = 0.26")
	mute := strings.Index(string(updated[track:]), "mute = off")
	if level < 0 || pan < 0 || mute < 0 || !(level < pan && pan < mute) {
		t.Fatalf("mixer insertion order is wrong: %s", updated[track:])
	}
	document, err := notation.ParseDocument(updated)
	if err != nil {
		t.Fatal(err)
	}
	formatted, err := notation.Format(document)
	if err != nil {
		t.Fatal(err)
	}
	if string(formatted) != string(updated) {
		t.Fatalf("writer output is not formatter-clean; source:\n%s\nformatted:\n%s", updated, formatted)
	}
	second, _, _, _, err := mixerSource(updated, "bass.pan", json.RawMessage(`0.256`))
	if err != nil {
		t.Fatal(err)
	}
	if string(second) != string(updated) {
		t.Fatal("repeating a mixer edit changed the source")
	}
}

func TestStudioMixerWriterAddsSettingsInsideInlineTrack(t *testing.T) {
	source := []byte("cicada 2\ntempo 120\nkey c minor\n// Keep this track comment.\ntrack keys acid { cutoff=700Hz }\npattern pulse notes { 1 . . . }\nscene main { keys=pulse }\nsong { main }\n")
	for _, test := range []struct {
		field string
		value string
	}{
		{"level", "-7.25"},
		{"pan", "0.125"},
		{"mute", "true"},
	} {
		t.Run(test.field, func(t *testing.T) {
			updated, _, _, _, err := mixerSource(source, "keys."+test.field, json.RawMessage(test.value))
			if err != nil {
				t.Fatal(err)
			}
			score, diagnostics := notation.Parse(updated)
			if score == nil || hasErrors(diagnostics) {
				t.Fatalf("inline mixer edit produced invalid source: %v\n%s", diagnostics, updated)
			}
			if len(score.Tracks) != 1 || !strings.Contains(string(updated), "// Keep this track comment.\ntrack keys acid {") || !strings.Contains(string(updated), "cutoff=700Hz") {
				t.Fatalf("inline mixer edit damaged its owner: %s", updated)
			}
			found := false
			for _, parameter := range score.Tracks[0].Params {
				found = found || parameter.Name == test.field
			}
			if !found {
				t.Fatalf("new %s is outside the track: %s", test.field, updated)
			}
			second, _, _, _, err := mixerSource(updated, "keys."+test.field, json.RawMessage(test.value))
			if err != nil || !bytes.Equal(updated, second) {
				t.Fatalf("repeating inline mixer edit changed source: %v\n%s", err, second)
			}
		})
	}
}

func TestStudioMixerWriterTrackLevelOffUsesMuteInEditionTwo(t *testing.T) {
	updated, _, _, _, err := mixerSource([]byte(mixerScore), "bass.level", json.RawMessage(`"off"`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(updated), "level = -6dB") || !strings.Contains(string(updated), "mute = on") {
		t.Fatalf("track off did not retain the valid level and save mute: %s", updated)
	}
}

func TestStudioMixerWriterBusAndMasterOffAlsoSaveMute(t *testing.T) {
	for _, path := range []string{"music.level", "master.level"} {
		updated, _, after, _, err := mixerSource([]byte(mixerScore), path, json.RawMessage(`"off"`))
		if err != nil || after != "off" {
			t.Fatalf("%s off: %v, after %q", path, err, after)
		}
		owner := strings.Split(path, ".")[0]
		if !strings.Contains(string(updated), owner+" {\n  level = off\n  mute = on") {
			t.Fatalf("%s off did not save the mute switch: %s", owner, updated)
		}
		document, parseErr := notation.ParseDocument(updated)
		if parseErr != nil {
			t.Fatal(parseErr)
		}
		formatted, formatErr := notation.Format(document)
		if formatErr != nil || !bytes.Equal(formatted, updated) {
			t.Fatalf("%s off is not formatter-clean: %v\n%s\nformatted:\n%s", owner, formatErr, updated, formatted)
		}
	}
}

func TestStudioMixerWriterAddsChangesAndRemovesNamedSends(t *testing.T) {
	added, _, _, _, err := mixerSource([]byte(mixerScore), "bass.send.space", json.RawMessage(`{"level":"-12dB","pre":true}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(added), "send space = -12dB pre") {
		t.Fatalf("named send was not added: %s", added)
	}
	stepped, _, after, _, err := mixerSource([]byte(mixerScore), "bass.send.space", json.RawMessage(`"-12.345dB"`))
	if err != nil || after != "send space = -12.35dB" || !strings.Contains(string(stepped), "send space = -12.35dB") {
		t.Fatalf("named send did not use its registry display step: %v, %q\n%s", err, after, stepped)
	}
	changed, before, after, _, err := mixerSource(added, "bass.send.space", json.RawMessage(`{"level":0.4,"pre":false}`))
	if err != nil {
		t.Fatal(err)
	}
	if before != "send space = -12dB pre" || after != "send space = 0.4" || !strings.Contains(string(changed), "send space = 0.4") {
		t.Fatalf("named send change: %q to %q\n%s", before, after, changed)
	}
	removed, _, _, _, err := mixerSource(changed, "bass.send.space", json.RawMessage(`{"remove":true}`))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(removed), "send space") {
		t.Fatalf("named send was not removed: %s", removed)
	}
}

func TestStudioMixerWriterReordersAddsAndRemovesInsertChain(t *testing.T) {
	ordered, _, _, _, err := mixerSource([]byte(mixerScore), "bass.insert", json.RawMessage(`"edge -> grit"`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(ordered), "insert = edge -> grit") {
		t.Fatalf("insert reorder was not written: %s", ordered)
	}
	added, _, _, _, err := mixerSource([]byte(mixerScore), "bass.insert", json.RawMessage(`"grit -> edge"`))
	if err != nil || !strings.Contains(string(added), "insert = grit -> edge") {
		t.Fatalf("insert chain add: %v\n%s", err, added)
	}
	removed, _, _, _, err := mixerSource([]byte(mixerScore), "bass.insert", json.RawMessage(`"none"`))
	if err != nil || !strings.Contains(string(removed), "insert = none") {
		t.Fatalf("insert chain remove: %v\n%s", err, removed)
	}
}

func TestStudioMixerWriterAddsFXBlockInFormatOrder(t *testing.T) {
	updated, before, after, _, err := mixerSource([]byte(mixerScore), "fx.texture", json.RawMessage(`{"kind":"drive"}`))
	if err != nil {
		t.Fatal(err)
	}
	if before != "absent" || after != "drive" || !strings.Contains(string(updated), "fx texture drive {}") {
		t.Fatalf("effect block was not added: %q to %q\n%s", before, after, updated)
	}
	document, err := notation.ParseDocument(updated)
	if err != nil {
		t.Fatal(err)
	}
	formatted, err := notation.Format(document)
	if err != nil || string(formatted) != string(updated) {
		t.Fatalf("added effect is not formatter-clean: %v\nsource:\n%s\nformatted:\n%s", err, updated, formatted)
	}
}

func TestStudioMixerWriterCoversEffectUnitsAndEnums(t *testing.T) {
	updated, _, _, _, err := mixerSource([]byte(mixerScore), "room.time", json.RawMessage(`"1/16T"`))
	if err != nil || !strings.Contains(string(updated), "time = 1/16T") {
		t.Fatalf("delay division: %v\n%s", err, updated)
	}
	updated, _, _, _, err = mixerSource([]byte(mixerScore), "room.damp", json.RawMessage(`"6khz"`))
	if err != nil || !strings.Contains(string(updated), "damp = 6000Hz") {
		t.Fatalf("effect unit normalization: %v\n%s", err, updated)
	}
	updated, _, _, _, err = mixerSource([]byte(mixerScore), "glue.makeup", json.RawMessage(`"auto"`))
	if err != nil || !strings.Contains(string(updated), "makeup = auto") {
		t.Fatalf("compressor auto makeup: %v\n%s", err, updated)
	}
}

func TestStudioMixerWriterInsertsEveryEffectFieldInFormatterOrder(t *testing.T) {
	for _, descriptor := range paramdefs.Registry {
		if descriptor.Scope != "global" || !strings.HasPrefix(descriptor.ID, "fx.") {
			continue
		}
		parts := strings.Split(descriptor.ID, ".")
		if len(parts) != 3 {
			t.Fatalf("unexpected effect descriptor ID %q", descriptor.ID)
		}
		kind := parts[1]
		name := map[string]string{"delay": "room", "reverb": "space", "drive": "grit", "comp": "glue"}[kind]
		if name == "" {
			t.Fatalf("no fixture effect for %s", kind)
		}
		var value any = descriptor.Default
		if descriptor.Curve == "enum" && len(descriptor.Values) > 0 {
			value = descriptor.Values[0]
		} else if descriptor.Curve == "toggle" {
			value = false
		}
		raw, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		updated, _, _, _, err := mixerSource([]byte(mixerScore), name+"."+descriptor.Source, raw)
		if err != nil {
			t.Fatalf("insert %s: %v", descriptor.ID, err)
		}
		document, err := notation.ParseDocument(updated)
		if err != nil {
			t.Fatalf("parse %s: %v", descriptor.ID, err)
		}
		formatted, err := notation.Format(document)
		if err != nil || !bytes.Equal(formatted, updated) {
			t.Fatalf("%s insertion is not formatter-clean: %v\nsource:\n%s\nformatted:\n%s", descriptor.ID, err, updated, formatted)
		}
	}
}

func TestStudioMixerWriterRejectsInvalidValuesAndLeavesValidationToRoute(t *testing.T) {
	if _, _, _, _, err := mixerSource([]byte(mixerScore), "bass.pan", json.RawMessage(`2`)); err == nil {
		t.Fatal("accepted pan outside the parameter range")
	}
	updated, _, _, _, err := mixerSource([]byte(mixerScore), "bass.send.music", json.RawMessage(`0.3`))
	if err != nil {
		t.Fatalf("writer should make the grammar-backed unsupported send edit for validation: %v", err)
	}
	if !strings.Contains(string(updated), "send music = 0.3") {
		t.Fatalf("unsupported route edit missing from candidate: %s", updated)
	}
}

func TestStudioMixerWritesPresetEffectParameters(t *testing.T) {
	source := []byte("cicada 2\npreset wet { instrument=builtin.delay feedback=0.3 }\nfx echo wet {}\ntrack lead acid { send echo=0.4 }\npattern melody { 1 . }\nscene main { lead=melody }\nsong { main }\n")
	for _, value := range []json.RawMessage{json.RawMessage(`0.6`), json.RawMessage(`0.7`)} {
		updated, _, after, _, err := mixerSource(source, "echo.feedback", value)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Contains(updated, []byte("preset wet { instrument=builtin.delay feedback=0.3 }")) || !bytes.Contains(updated, []byte("fx echo wet")) {
			t.Fatalf("preset binding changed: %s", updated)
		}
		score, ds := notation.Parse(updated)
		if score == nil || hasErrors(ds) {
			t.Fatalf("updated score invalid: %+v", ds)
		}
		resolved, ds := notation.ResolvePresets(score)
		if hasErrors(ds) || len(resolved.Effects) != 1 || TrackMixerSourceText(resolved.Effects[0].Params, "feedback") != after {
			t.Fatalf("effect override not saved: %+v %+v", resolved.Effects, ds)
		}
		source = updated
	}
}

func fakeUpgrade(source []byte) ([]byte, []File, error) {
	return append([]byte("cicada 2\n\n"), source...), []File{{Path: "cicada.mod", Before: []byte("old"), After: []byte("new")}}, nil
}

func applyOne(t *testing.T, source []byte, opts Options, intent Intent) (*Result, error) {
	t.Helper()
	if opts.Compiler == nil {
		opts.Compiler = parseCompiler{}
	}
	return Apply(source, Envelope{Version: EnvelopeVersion, Intents: []Intent{intent}}, opts)
}

func TestMixerSetParamMatchesWriter(t *testing.T) {
	want, previous, after, changed, err := mixerSource([]byte(mixerScore), "bass.level", json.RawMessage(`-3.47`))
	if err != nil || previous != "-6dB" || after != "-3.5dB" {
		t.Fatalf("writer: %v %q %q", err, previous, after)
	}
	result, err := applyOne(t, []byte(mixerScore), Options{}, &SetParam{Entity: "param:bass.level", Value: json.RawMessage(`-3.47`)})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(result.Source, want) || result.Label != "Mix: bass level -6 dB to -3.5 dB" {
		t.Fatalf("source or label differ: %q", result.Label)
	}
	for key, value := range map[string]any{"path": "bass.level", "value": "-3.5 dB", "previous": "-6 dB", "changedRange": changed, "source": string(want)} {
		if !reflect.DeepEqual(result.Response[key], value) {
			t.Fatalf("response %s = %#v, want %#v", key, result.Response[key], value)
		}
	}
	// The double space before "dB" is today's label: the writer already spaces the prior value and the route spaces it again.
	off, err := applyOne(t, []byte(mixerScore), Options{}, &SetParam{Entity: "param:bass.level", Value: json.RawMessage(`"off"`)})
	if err != nil || off.Response["value"] != "off" || off.Label != "Mix: bass level -6  dB to off" {
		t.Fatalf("off: %v %+v %q", err, off.Response, off.Label)
	}
	for _, path := range []string{"bass.send.room", "music.insert", "room.time", "master.level"} {
		if !isMixerPathFor(t, path) {
			t.Fatalf("%s should dispatch to the mixer writer", path)
		}
	}
}

func isMixerPathFor(t *testing.T, path string) bool {
	t.Helper()
	score, ds := notation.Parse([]byte(mixerScore))
	if hasErrors(ds) {
		t.Fatal(ds)
	}
	owner, field, _ := strings.Cut(path, ".")
	writer, err := paramWriterFor(score, owner, field)
	return err == nil && writer == ParamWriterMixer
}

func TestMixerAddEffectMatchesWriter(t *testing.T) {
	want, _, _, _, err := mixerSource([]byte(mixerScore), "fx.tape", json.RawMessage(`{"kind":"drive"}`))
	if err != nil {
		t.Fatal(err)
	}
	result, err := applyOne(t, []byte(mixerScore), Options{}, &AddEffect{Name: "tape", EffectKind: "drive"})
	if err != nil || !bytes.Equal(result.Source, want) {
		t.Fatalf("add effect: %v", err)
	}
	viaSetParam, err := applyOne(t, []byte(mixerScore), Options{}, &SetParam{Entity: "param:fx.tape", Value: json.RawMessage(`{"kind":"drive"}`)})
	if err != nil || !bytes.Equal(viaSetParam.Source, want) {
		t.Fatalf("set param fx path: %v", err)
	}
}

const wobScore = `cicada 2

instrument wob {
  octave = 3
  param pan = 0.5
  param level = 0.5
  voice mono {
    let shape = env(gate, 100ms)
    out = saw(pitch) * shape * level * pan
  }
}

track lead wob {}

track master wob {}

fx room delay {}

pattern line {
  1 . 3 .
}

scene main {
  lead = line
}

song {
  main
}
`

func TestMixerPathDispatch(t *testing.T) {
	score, ds := notation.Parse([]byte(wobScore))
	if hasErrors(ds) {
		t.Fatal(ds)
	}
	for _, c := range []struct {
		owner, field string
		want         ParamWriter
	}{
		{"lead", "pan", ParamWriterInstrument},      // the instrument declares pan
		{"lead", "level", ParamWriterInstrument},    // the instrument declares level
		{"lead", "mute", ParamWriterMixer},          // the instrument does not
		{"lead", "cutoff", ParamWriterInstrument},   // no mixer name at all
		{"room", "feedback", ParamWriterMixer},      // a declared effect
		{"fx", "tape", ParamWriterMixer},            // effect creation
		{"master", "cutoff", ParamWriterInstrument}, // a track named master, instrument-only field
	} {
		got, err := paramWriterFor(score, c.owner, c.field)
		if err != nil || got != c.want {
			t.Errorf("%s.%s: %q %v, want %q", c.owner, c.field, got, err, c.want)
		}
	}
	// A track named master with a mixer-named field cannot be resolved by name.
	if _, err := paramWriterFor(score, "master", "mute"); err == nil || !strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("ambiguous path: %v", err)
	}
}

func TestMixerSetParamPinsAndAmbiguity(t *testing.T) {
	source := []byte(wobScore)
	set := func(opts Options, entity string, value string) (*Result, error) {
		return applyOne(t, source, opts, &SetParam{Entity: EntityID(entity), Value: json.RawMessage(value)})
	}
	// Generic surface: the instrument declares pan, so the instrument writer runs.
	if r, err := set(Options{}, "param:lead.pan", `0.3`); err != nil || r.Label != "Set lead.pan" {
		t.Fatalf("declared pan: %v", err)
	}
	if _, err := set(Options{}, "param:master.mute", `"on"`); err == nil || !strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("ambiguous: %v", err)
	}
	// Pinned to the instrument writer, mixer names get the instrument writer's text.
	if _, err := set(Options{ParamWriter: ParamWriterInstrument}, "param:lead.mute", `"on"`); err == nil || err.Error() != `instrument wob does not declare parameter "mute"` {
		t.Fatalf("pinned instrument: %v", err)
	}
	// Pinned to the mixer writer, a path without a dot gets the mixer text.
	if _, err := set(Options{ParamWriter: ParamWriterMixer}, "param:lead", `0.5`); err == nil || err.Error() != `mixer path "lead" must name an owner and field` {
		t.Fatalf("pinned mixer: %v", err)
	}
	// The pin is not part of the intent's JSON.
	raw, _ := json.Marshal(&SetParam{Entity: "param:lead.pan"})
	if strings.Contains(strings.ToLower(string(raw)), "mixer") || strings.Contains(strings.ToLower(string(raw)), "writer") {
		t.Fatalf("serialized intent leaks the route: %s", raw)
	}
}

func TestMixerEditionOneUpgrade(t *testing.T) {
	legacy, err := os.ReadFile(filepath.Join("..", "examples", "fx", "delay-send.cicada"))
	if err != nil {
		t.Fatal(err)
	}
	intent := func(confirm bool) *SetParam {
		return &SetParam{Entity: "param:bass.level", Value: json.RawMessage(`-3.5`), ConfirmUpgrade: confirm}
	}
	_, err = applyOne(t, legacy, Options{Edition: 1, UpgradeEdition: fakeUpgrade}, intent(false))
	if err == nil || err.Error() != "This score uses edition 1. Upgrade to edition 2 to save mixer changes?" {
		t.Fatalf("unconfirmed: %v", err)
	}
	_, err = applyOne(t, legacy, Options{Edition: 1}, intent(true))
	if !errors.Is(err, ErrNoUpgrade) {
		t.Fatalf("no upgrade hook: %v", err)
	}
	result, err := applyOne(t, legacy, Options{Edition: 1, UpgradeEdition: fakeUpgrade}, intent(true))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(result.Source, []byte("cicada 2\n\n")) || !bytes.Contains(result.Source, []byte("level = -3.5dB")) {
		t.Fatalf("upgraded source:\n%s", result.Source)
	}
	if len(result.Files) != 1 || result.Files[0].Path != "cicada.mod" {
		t.Fatalf("files: %+v", result.Files)
	}
}
