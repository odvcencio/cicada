package benchscore

import (
	"fmt"
	"strings"
	"sync"

	"m31labs.dev/cicada/kernel/seq"
)

// The entities of REF-L that the hot edits act on. They sit in the middle of
// the file, so a later incremental parser cannot be helped by an edit near
// either end.
const (
	// LargeStepPattern is a 32-step pattern near the middle of the patterns.
	LargeStepPattern = "veil-riff-1"
	// LargePerformance is the 512-note performance.
	LargePerformance = "quill-take-long"
	// LargePlacement is a placement near the middle of the arrange block with
	// at least one bar free after it on its track.
	LargePlacement = "veil-part-06"
)

// The shape of REF-L, from line matches in the largest production (no text
// was copied): 22 tracks; 98 patterns, of which 43 are performances holding
// 2,267 notes and 6 are drum patterns; 226 placements and 9 markers in one
// arrange block; 45 effects; 14 instruments; 18 automation lanes holding 168
// points; 2 kits; 1 bus; a master. The production's 8 samplers and its asset
// are replaced by built-in voices, because sample packs are not in the
// repository, so REF-L has 206 top-level declarations where the production has
// 215. One performance holds 512 notes, the most a performance may hold. The
// declarations follow the production's order: the header, instruments with
// the kits among them, effects, the bus and the master, tracks, drum
// patterns, step patterns, the performances, five more step patterns, the
// arrange block, and the automation lanes last. Its 140 comments average
// about 80 bytes, as the production's do (75).
const (
	largeTracks       = 22
	largeInstruments  = 14
	largeEffects      = 45
	largePerformances = 43
	largeNotes        = 2267
	largeNotePatterns = 49
	largeDrumPatterns = 6
	largePlacements   = 226
	largeMarkers      = 9
	largeAutomation   = 18
	largeAutoPoints   = 168
	largeBars         = 128
	longTakeNotes     = 512
	longTake          = 21 // the index of the 512-note performance
)

var large = sync.OnceValue(generateLarge)

// Large returns REF-L, a copy each call. It is generated, never read from disk:
// a seeded generator writes the same bytes on every machine and architecture
// (it uses integers only), and references_test.go pins their SHA-256.
func Large() []byte { return []byte(large()) }

// splitmix is the repository's small deterministic generator (as in
// internal/testwav): REF-L must not depend on the global random source.
type splitmix uint64

func (s *splitmix) next() uint64 {
	*s += 0x9e3779b97f4a7c15
	z := uint64(*s)
	z = (z ^ (z >> 30)) * 0xbf58476d1ce4e5b9
	z = (z ^ (z >> 27)) * 0x94d049bb133111eb
	return z ^ (z >> 31)
}

// intn returns a number from 0 to n-1.
func (s *splitmix) intn(n int) int { return int(s.next() % uint64(n)) }

// between returns a number from lo to hi inclusive.
func (s *splitmix) between(lo, hi int) int { return lo + s.intn(hi-lo+1) }

// pick returns one of the choices.
func pick[T any](s *splitmix, choices ...T) T { return choices[s.intn(len(choices))] }

// hundredths spells n/100 with two decimals, such as 0.46 or -1.05.
func hundredths(n int) string {
	sign := ""
	if n < 0 {
		sign, n = "-", -n
	}
	return fmt.Sprintf("%s%d.%02d", sign, n/100, n%100)
}

// The key is D natural minor; pitch names use flats.
var (
	minorSteps = []int{0, 2, 3, 5, 7, 8, 10}
	pitchNames = []string{"c", "db", "d", "eb", "e", "f", "gb", "g", "ab", "a", "bb", "b"}
)

const tonic = 62 // d4

// scalePitch returns the MIDI pitch of a scale degree counted from d4 (0 is
// d4, 7 is d5, -7 is d3).
func scalePitch(degree int) int {
	octave := degree / 7
	index := degree % 7
	if index < 0 {
		index += 7
		octave--
	}
	return tonic + 12*octave + minorSteps[index]
}

func pitchName(midi int) string { return fmt.Sprintf("%s%d", pitchNames[midi%12], midi/12-1) }

// instrumentSpec is one authored synthesizer of REF-L.
type instrumentSpec struct {
	name, track string
	poly        bool
	low, high   int // the scale degrees its lines move between
	body        func(s *splitmix) []string
}

func hz(s *splitmix, lo, hi, step int) string {
	return fmt.Sprintf("%dHz", lo+step*s.intn((hi-lo)/step+1))
}
func ms(s *splitmix, lo, hi int) string     { return fmt.Sprintf("%dms", s.between(lo, hi)) }
func amount(s *splitmix, lo, hi int) string { return hundredths(s.between(lo, hi)) }

// The fourteen instruments, each played by its own track. pressure and timbre
// shape most of them, so the performances' gestures are heard.
var largeInstrumentSpecs = []instrumentSpec{
	{name: "glass", track: "glass", poly: true, low: 0, high: 12, body: func(s *splitmix) []string {
		return []string{
			"let strike = noise() * env(gate, 2ms) * velocity",
			fmt.Sprintf("let string = comb(strike, 1 / pitch, 0.98%d, 0.%d)", s.between(4, 9), s.between(5, 7)),
			fmt.Sprintf("let shine = sine(pitch * 2) * env(gate, %s) * (0.05 + timbre * 0.08)", ms(s, 300, 700)),
			fmt.Sprintf("out = (lowpass(string, %s) * 0.3 + shine) * env(gate, %s) * (0.7 + pressure * 0.3)", hz(s, 2800, 4600, 100), ms(s, 900, 1600)),
		}
	}},
	{name: "ember", track: "ember", low: -7, high: 4, body: func(s *splitmix) []string {
		return []string{
			fmt.Sprintf("let amp = adsr(gate, %s, %s, 0.%d, %s)", ms(s, 5, 20), ms(s, 120, 260), s.between(55, 80), ms(s, 120, 240)),
			"let tone = sine(pitch) * 0.7 + saw(pitch) * 0.14",
			fmt.Sprintf("out = lowpass(tone, %s) * amp * velocity * (0.4 + pressure * 0.2)", hz(s, 400, 900, 20)),
		}
	}},
	{name: "tide", track: "tide", poly: true, low: -3, high: 9, body: func(s *splitmix) []string {
		return []string{
			fmt.Sprintf("let amp = adsr(gate, %s, %s, 0.%d, %s)", ms(s, 300, 600), ms(s, 700, 1100), s.between(55, 75), ms(s, 1100, 1700)),
			fmt.Sprintf("let body = (saw(pitch) + saw(pitch * 1.00%d)) * 0.5", s.between(2, 4)),
			fmt.Sprintf("out = svf(body, %s, 0.%d) * amp * velocity * 0.2 * (0.8 + timbre * 0.2)", hz(s, 1100, 1900, 50), s.between(12, 20)),
		}
	}},
	{name: "cinder", track: "cinder", low: -10, high: 0, body: func(s *splitmix) []string {
		return []string{
			fmt.Sprintf("let amp = adsr(gate, 3ms, %s, 0.%d, %s)", ms(s, 140, 260), s.between(50, 70), ms(s, 90, 180)),
			fmt.Sprintf("let edge = lowpass(square(pitch), %s) * env(gate, %s)", hz(s, 600, 1200, 20), ms(s, 140, 300)),
			"out = (sine(pitch) * 0.6 + edge * 0.25) * amp * velocity * (0.45 + pressure * 0.15)",
		}
	}},
	{name: "reedling", track: "reed", low: 0, high: 10, body: func(s *splitmix) []string {
		return []string{
			fmt.Sprintf("let amp = adsr(gate, %s, %s, 0.%d, %s)", ms(s, 20, 50), ms(s, 180, 300), s.between(65, 80), ms(s, 140, 220)),
			fmt.Sprintf("let reed = highpass(saw(pitch), %s)", hz(s, 160, 300, 10)),
			fmt.Sprintf("out = lowpass(reed, %s) * amp * velocity * 0.22 * (0.7 + timbre * 0.3)", hz(s, 1800, 3200, 100)),
		}
	}},
	{name: "halo", track: "halo", low: 4, high: 14, body: func(s *splitmix) []string {
		return []string{
			fmt.Sprintf("let amp = env(gate, %s)", ms(s, 600, 1100)),
			fmt.Sprintf("out = pm(pitch, sine(pitch * %d), amp * 1.%d) * amp * velocity * 0.15 * (0.8 + pressure * 0.2)", s.between(2, 3), s.between(0, 4)),
		}
	}},
	{name: "murmur", track: "murmur", poly: true, low: -2, high: 8, body: func(s *splitmix) []string {
		return []string{
			"param breath = 0.015",
			fmt.Sprintf("let amp = adsr(gate, %s, %s, 0.%d, %s)", ms(s, 380, 520), ms(s, 900, 1200), s.between(68, 80), ms(s, 1400, 1800)),
			"let tone = sine(pitch) * 0.5 + (saw(pitch * 0.999) + saw(pitch * 1.0012)) * 0.12",
			fmt.Sprintf("let air = lowpass(noise(), %s) * breath", hz(s, 3600, 4800, 100)),
			fmt.Sprintf("out = highpass(lowpass(tone, %s) + air, %s) * amp * velocity * 0.32", hz(s, 1000, 1400, 20), hz(s, 240, 320, 10)),
		}
	}},
	{name: "quill", track: "quill", low: 2, high: 12, body: func(s *splitmix) []string {
		return []string{
			"let strike = noise() * env(gate, 1ms) * velocity",
			fmt.Sprintf("let string = comb(strike, 1 / pitch, 0.99%d, 0.5%d)", s.between(0, 3), s.between(0, 9)),
			fmt.Sprintf("out = lowpass(string, %s) * env(gate, %s) * 0.3 * (0.75 + timbre * 0.25)", hz(s, 3800, 5200, 100), ms(s, 1200, 2000)),
		}
	}},
	{name: "lantern", track: "lantern", low: 0, high: 9, body: func(s *splitmix) []string {
		return []string{
			fmt.Sprintf("let amp = adsr(gate, %s, %s, 0.%d, %s)", ms(s, 25, 45), ms(s, 180, 260), s.between(62, 76), ms(s, 150, 210)),
			"let tone = sine(pitch) * 0.6 + sine(pitch * 2) * 0.22 + sine(pitch * 3) * 0.08",
			fmt.Sprintf("out = lowpass(tone, %s) * amp * velocity * 0.28 * (0.75 + pressure * 0.25)", hz(s, 1800, 2400, 50)),
		}
	}},
	{name: "drift", track: "drift", poly: true, low: -4, high: 7, body: func(s *splitmix) []string {
		return []string{
			fmt.Sprintf("let amp = adsr(gate, %s, %s, 0.%d, %s)", ms(s, 200, 400), ms(s, 600, 900), s.between(60, 72), ms(s, 900, 1300)),
			"let body = saw(pitch) * 0.5 + saw(pitch * 1.004) * 0.3",
			fmt.Sprintf("let wide = body + delay(body, %s) * 0.2", ms(s, 9, 17)),
			fmt.Sprintf("out = svf(wide, %s, 0.%d) * amp * velocity * 0.18", hz(s, 1300, 2100, 50), s.between(10, 18)),
		}
	}},
	{name: "wick", track: "wick", low: -14, high: -4, body: func(s *splitmix) []string {
		return []string{
			"glide = 35ms",
			fmt.Sprintf("let amp = adsr(gate, 4ms, %s, 0.%d, %s)", ms(s, 160, 240), s.between(60, 75), ms(s, 100, 160)),
			fmt.Sprintf("let grit = tanh(square(pitch) * %d) * 0.2", s.between(2, 4)),
			fmt.Sprintf("out = lowpass(sine(pitch) * 0.7 + grit, %s) * amp * velocity * 0.4", hz(s, 500, 800, 20)),
		}
	}},
	{name: "frost", track: "frost", poly: true, low: 5, high: 16, body: func(s *splitmix) []string {
		return []string{
			fmt.Sprintf("let amp = adsr(gate, %s, %s, 0.%d, %s)", ms(s, 10, 30), ms(s, 400, 700), s.between(40, 60), ms(s, 700, 1100)),
			fmt.Sprintf("out = (sine(pitch) * 0.5 + pm(pitch, sine(pitch * 4), 0.%d) * 0.3) * amp * velocity * 0.14", s.between(3, 8)),
		}
	}},
	{name: "hollow", track: "hollow", low: -5, high: 5, body: func(s *splitmix) []string {
		return []string{
			"param shade = 1400Hz",
			fmt.Sprintf("let amp = adsr(gate, %s, %s, 0.%d, %s)", ms(s, 8, 20), ms(s, 200, 320), s.between(55, 70), ms(s, 140, 220)),
			fmt.Sprintf("out = svf(square(pitch) * 0.4 + sine(pitch) * 0.4, shade, 0.%d) * amp * velocity * 0.3 * (0.7 + timbre * 0.3)", s.between(15, 25)),
		}
	}},
	{name: "loom", track: "loom", poly: true, low: -1, high: 10, body: func(s *splitmix) []string {
		return []string{
			"param weave = 0.3",
			"param warmth = 2200Hz",
			fmt.Sprintf("let amp = adsr(gate, %s, %s, 0.%d, %s)", ms(s, 60, 140), ms(s, 500, 800), s.between(60, 74), ms(s, 800, 1200)),
			"let tone = sine(pitch) * 0.55 + saw(pitch * 1.002) * weave * 0.3",
			"out = lowpass(tone, warmth) * amp * velocity * 0.24 * (0.7 + pressure * 0.3)",
		}
	}},
}

// trackSpec is one of REF-L's 22 tracks.
type trackSpec struct {
	name, voice string
	poly        bool
	drums       bool
	performs    bool // plays performances (the instrument tracks)
	low, high   int
	lanes       []string // drum lanes, for drum tracks
	inserts     []string // insert effects, created for this track
	sends       []string
}

// The eight tracks whose production counterparts play samplers use built-in
// voices: three drum tracks (the built-in kit and two authored kits of
// built-in pieces) and five built-in keyboards.
var largeBuiltinTracks = []trackSpec{
	{name: "drumkit", voice: "drums", drums: true, lanes: []string{"bd", "sd", "ch", "oh", "rs"}},
	{name: "tins", voice: "tinware", drums: true, lanes: []string{"bd", "sd", "ch"}},
	{name: "brushes", voice: "brushwork", drums: true, lanes: []string{"sd", "ch", "oh"}},
	{name: "keys", voice: "tine_ep", poly: true, low: -4, high: 9},
	{name: "veil", voice: "soft_pad", poly: true, low: -2, high: 8},
	{name: "stab", voice: "clav", poly: true, low: 0, high: 10},
	{name: "low", voice: "fm_bass", low: -14, high: -5},
	{name: "strings", voice: "string_machine", poly: true, low: 0, high: 12},
}

// The tracks with three inserts each: 13 tracks times 3 is 39 insert effects,
// which with 3 send and 3 master effects makes 45.
var largeInsertTracks = map[string]bool{
	"glass": true, "ember": true, "tide": true, "reed": true, "halo": true, "quill": true, "lantern": true,
	"wick": true, "hollow": true, "loom": true, "keys": true, "veil": true, "strings": true,
}

type largeWriter struct {
	strings.Builder
	rng      splitmix
	comments int
	meter    seq.MeterMap // REF-L declares no meter: 4/4 throughout
}

func (w *largeWriter) line(format string, args ...any) {
	fmt.Fprintf(w, format, args...)
	w.WriteByte('\n')
}

// comment writes one comment line. The production has 140 of them, 75 bytes
// on average; REF-L has as many, about as long.
func (w *largeWriter) comment(text string) {
	w.line("// %s", text)
	w.comments++
}

// phrases end REF-L's comments.
var phrases = []string{
	"kept low in the mix until the second half of the song",
	"answers the lead from the left and steps back in the chorus",
	"carries the harmony while the drums thin out to a pulse",
	"doubles the line an octave up only where the song opens",
	"plays softer here, so the bends and the pressure come through",
	"was trimmed after the first pass so the low end stays clear",
	"leans behind the beat by a few ticks, as a player would",
	"holds its notes long enough to overlap the next section",
	"enters late and leaves early, framing the middle of the song",
	"stays in the background, felt more than heard most of the time",
}

// sentence fills format with args and one phrase, which takes the last verb.
func (w *largeWriter) sentence(format string, args ...string) string {
	all := make([]any, 0, len(args)+1)
	for _, arg := range args {
		all = append(all, arg)
	}
	return fmt.Sprintf(format, append(all, pick(&w.rng, phrases...))...)
}

func generateLarge() string {
	meter, err := seq.NewMeterMap([]seq.MeterChange{{Bar: 0, Meter: seq.Meter{Beats: 4, Unit: 4}}})
	if err != nil {
		panic(err)
	}
	w := &largeWriter{rng: splitmix(0x20261112), meter: meter}
	s := &w.rng

	// Tracks: the fourteen instrument tracks, then the built-in ones.
	var tracks []trackSpec
	for _, inst := range largeInstrumentSpecs {
		tracks = append(tracks, trackSpec{name: inst.track, voice: inst.name, poly: inst.poly, performs: true, low: inst.low, high: inst.high})
	}
	tracks = append(tracks, largeBuiltinTracks...)
	if len(tracks) != largeTracks {
		panic("benchscore: REF-L track count")
	}

	// Effects: two sends (a score may hold one reverb and one delay), three
	// on the master, one on the music bus and three inserts on 13 tracks.
	type effect struct{ name, kind string }
	var effects []effect
	sharedEffects := []effect{{"room", "reverb"}, {"echo", "delay"}, {"air", "eq"}, {"glue", "comp"}, {"ceiling", "limiter"}, {"bus_glue", "comp"}}
	effects = append(effects, sharedEffects...)
	sends := 0
	for i := range tracks {
		t := &tracks[i]
		if largeInsertTracks[t.name] {
			for _, role := range []string{"floor", "tone", pick(s, "press", "heat")} {
				kind := map[string]string{"floor": "eq", "tone": "eq", "press": "comp", "heat": "drive"}[role]
				name := t.name + "_" + role
				t.inserts = append(t.inserts, name)
				effects = append(effects, effect{name, kind})
			}
		}
		// 23 sends in all: one for every track and a second for one.
		t.sends = []string{pick(s, "room", "echo")}
		if i == 4 {
			t.sends = append(t.sends, map[string]string{"room": "echo", "echo": "room"}[t.sends[0]])
		}
		sends += len(t.sends)
	}
	if len(effects) != largeEffects || sends != 23 {
		panic(fmt.Sprintf("benchscore: REF-L has %d effects and %d sends", len(effects), sends))
	}

	w.comment("REF-L: the large reference score of Cicada's edit benchmark (internal/benchscore).")
	w.comment("Generated by a seeded generator; its shape follows the largest production, its music is its own.")
	w.line("cicada 2")
	w.line("")
	w.line("title \"Reference L\"")
	w.line("")
	w.line("tempo 96")
	w.line("")
	w.line("key d minor")
	w.line("")
	w.line("seed 20261112")
	w.line("")

	w.comment("Fourteen authored voices. Pressure and timbre shape most of them, so a gesture is heard.")
	for i, inst := range largeInstrumentSpecs {
		if i == 9 {
			// The production declares its kits among its instruments.
			w.writeKits()
		}
		mode := "mono"
		if inst.poly {
			mode = "poly"
		}
		w.comment(w.sentence("%s %s, %s.", inst.name, mode))
		w.line("instrument %s {", inst.name)
		body := inst.body(s)
		// Every voice ends in a gentle highpass, as a mix engineer would add.
		last := body[len(body)-1]
		body = append(body[:len(body)-1], "let voiced = "+strings.TrimPrefix(last, "out = "), fmt.Sprintf("out = highpass(voiced, %s) * (0.92 + velocity * 0.08)", hz(s, 30, 90, 5)))
		var settings []string
		for len(body) > 0 && (strings.HasPrefix(body[0], "param ") || strings.HasPrefix(body[0], "glide ")) {
			settings, body = append(settings, body[0]), body[1:]
		}
		for _, setting := range settings {
			w.line("  %s", setting)
		}
		w.line("  voice %s {", mode)
		for _, statement := range body {
			w.line("    %s", statement)
		}
		w.line("  }")
		w.line("}")
		w.line("")
	}

	w.comment("One reverb and one delay take the sends; the master and the music bus have their own chains.")
	for i, e := range effects {
		if i == len(sharedEffects) {
			w.comment("Inserts: three for each of thirteen tracks, a floor, a tone and a press or some heat.")
		}
		if i >= len(sharedEffects) && (i-len(sharedEffects))%3 == 0 {
			w.comment(w.sentence("The chain for %s %s.", strings.TrimSuffix(e.name, "_floor")))
		}
		w.writeEffect(e.name, e.kind)
	}

	w.comment("The music bus glues the tracks before the master chain shapes and limits the whole song.")
	w.line("bus music {")
	w.line("  insert = bus_glue")
	w.line("}")
	w.line("")
	w.line("master {")
	w.line("  insert = air -> glue -> ceiling")
	w.line("  level = -1dB")
	w.line("}")
	w.line("")

	w.comment("Twenty-two tracks: fourteen authored voices, five built-in keyboards and three drum kits.")
	for _, t := range tracks {
		w.comment(w.sentence("Track %s %s.", t.name))
		w.line("track %s %s {", t.name, t.voice)
		w.line("  level = %ddB", -s.between(6, 16))
		if !t.drums {
			w.line("  pan = %s", hundredths(s.between(-60, 60)))
		}
		if len(t.inserts) > 0 {
			w.line("  insert = %s", strings.Join(t.inserts, " -> "))
		}
		for _, send := range t.sends {
			w.line("  send %s = %ddB", send, -s.between(6, 22))
		}
		w.line("}")
		w.line("")
	}
	// Patterns. Each track owns its patterns; placements play only its own.
	owned := make(map[string][]string, len(tracks))
	var melodic, performers, drummers []int
	for i, t := range tracks {
		switch {
		case t.drums:
			drummers = append(drummers, i)
		case t.performs:
			performers = append(performers, i)
			melodic = append(melodic, i)
		default:
			melodic = append(melodic, i)
		}
	}

	w.comment("Drum patterns, two for each kit, swung a little so the hand-played takes sit on them.")
	for k := range largeDrumPatterns {
		t := tracks[drummers[k%len(drummers)]]
		name := fmt.Sprintf("%s-beat-%d", t.name, k/len(drummers)+1)
		owned[t.name] = append(owned[t.name], name)
		w.writeDrums(name, t.lanes)
	}

	w.comment("Step patterns: riffs, chord progressions and lines for every melodic track, in seven groups.")
	riffs := map[string]int{}
	steps := func(from, to int) {
		for k := from; k < to; k++ {
			t := tracks[melodic[k%len(melodic)]]
			riffs[t.name]++
			name := fmt.Sprintf("%s-riff-%d", t.name, riffs[t.name])
			owned[t.name] = append(owned[t.name], name)
			if k%8 == 0 {
				w.comment(w.sentence("Riff group %s %s.", fmt.Sprint(k/8+1)))
			}
			w.writeSteps(name, t, k)
		}
	}
	// As in the production, five step patterns follow the performances.
	steps(0, largeNotePatterns-5)
	if !contains(owned["veil"], LargeStepPattern) {
		panic("benchscore: REF-L lost its step pattern target")
	}

	// Performances: 42 of 8 to 140 notes, and one of 512 in the middle,
	// holding 2,267 notes in all.
	counts := make([]int, largePerformances)
	sum := 0
	for k := range counts {
		if k != longTake {
			counts[k] = s.between(18, 66)
			sum += counts[k]
		}
	}
	counts[longTake] = longTakeNotes
	for sum += longTakeNotes; sum != largeNotes; {
		k := s.intn(largePerformances)
		switch {
		case k == longTake:
		case sum < largeNotes && counts[k] < 140:
			counts[k]++
			sum++
		case sum > largeNotes && counts[k] > 8:
			counts[k]--
			sum--
		}
	}
	w.comment("Performances played in by hand, with one gesture on every note: bend, pressure and timbre.")
	takes := map[string]int{}
	for k, notes := range counts {
		t := tracks[performers[k%len(performers)]]
		name := LargePerformance
		if k != longTake {
			takes[t.name]++
			name = fmt.Sprintf("%s-take-%d", t.name, takes[t.name])
		} else if !strings.HasPrefix(name, t.name+"-") {
			panic("benchscore: REF-L long take is on track " + t.name)
		}
		owned[t.name] = append(owned[t.name], name)
		w.comment(w.sentence("Take %s on %s: %s notes %s.", fmt.Sprintf("%d of %d", k+1, largePerformances), t.name, fmt.Sprint(notes)))
		w.writePerformance(name, t, notes)
	}

	steps(largeNotePatterns-5, largeNotePatterns)
	w.writeArrange(tracks, owned)
	w.writeAutomation(tracks)
	if w.comments != 140 {
		panic(fmt.Sprintf("benchscore: REF-L has %d comments; the production has 140", w.comments))
	}
	return w.String()
}

// writeKits writes the two kits of built-in pieces.
func (w *largeWriter) writeKits() {
	w.comment("Two kits of built-in pieces stand where the production played its own samples.")
	w.line("kit tinware {")
	w.line("  bd = builtin.bd")
	w.line("  sd = builtin.sd")
	w.line("  ch = builtin.ch")
	w.line("}")
	w.line("")
	w.line("kit brushwork {")
	w.line("  sd = builtin.sd")
	w.line("  ch = builtin.ch")
	w.line("  oh = builtin.oh")
	w.line("}")
	w.line("")

}

func contains(list []string, want string) bool {
	for _, item := range list {
		if item == want {
			return true
		}
	}
	return false
}

func (w *largeWriter) writeEffect(name, kind string) {
	s := &w.rng
	w.line("fx %s %s {", name, kind)
	switch kind {
	case "reverb":
		w.line("  size = %s", amount(s, 80, 120))
		w.line("  decay = %s", hundredths(s.between(220, 380))+"s")
		w.line("  damp = %s", hz(s, 4200, 6000, 100))
		w.line("  predelay = %s", ms(s, 12, 40))
		w.line("  mix = 1")
	case "delay":
		w.line("  time = 1/8.")
		w.line("  feedback = %s", amount(s, 24, 40))
		w.line("  damp = %s", hz(s, 3000, 4200, 100))
		w.line("  mix = 1")
	case "comp":
		w.line("  threshold = %ddB", -s.between(12, 22))
		w.line("  ratio = %s", hundredths(s.between(150, 400)))
		w.line("  attack = %s", ms(s, 5, 35))
		w.line("  release = %s", ms(s, 90, 220))
		w.line("  makeup = %ddB", s.between(2, 6))
		w.line("  mix = %s", amount(s, 40, 100))
	case "limiter":
		w.line("  ceiling = -1dBTP")
		w.line("  lookahead = 4ms")
		w.line("  release = %s", ms(s, 100, 160))
	case "drive":
		w.line("  shape = %s", pick(s, "soft", "hard"))
		w.line("  gain = %ddB", s.between(4, 12))
		w.line("  tone = %dkHz", s.between(6, 12))
		w.line("  mix = %s", amount(s, 30, 70))
	case "eq":
		switch {
		case name == "air":
			w.line("  type = high_shelf")
			w.line("  frequency = 9kHz")
			w.line("  q = 0.707")
			w.line("  gain = 0.5dB")
		case strings.HasSuffix(name, "_floor"):
			w.line("  type = highpass")
			w.line("  frequency = %s", hz(s, 60, 280, 5))
			w.line("  q = %s", amount(s, 60, 75))
		default:
			w.line("  type = %s", pick(s, "peak", "high_shelf"))
			w.line("  frequency = %s", hz(s, 1200, 5200, 100))
			w.line("  q = %s", amount(s, 60, 90))
			w.line("  gain = %s", hundredths(-s.between(50, 250))+"dB")
		}
	}
	w.line("}")
	w.line("")
}

func (w *largeWriter) writeDrums(name string, lanes []string) {
	s := &w.rng
	w.line("pattern %s drums {", name)
	w.line("  swing = %d%%", s.between(52, 58))
	// Two or three lanes of 32 steps, in groups of four.
	use := lanes[:2+s.intn(len(lanes)-1)]
	for _, lane := range use {
		var groups []string
		for range 8 {
			var cell strings.Builder
			for step := range 4 {
				switch {
				case step == 0 && (lane == "bd" || lane == "ch") && s.intn(3) != 0:
					cell.WriteString(pick(s, "X", "x", "x6"))
				case s.intn(4) == 0:
					cell.WriteString(fmt.Sprintf("x%d", s.between(2, 7)))
				default:
					cell.WriteByte('.')
				}
			}
			groups = append(groups, cell.String())
		}
		w.line("  %s: %s", lane, strings.Join(groups, " "))
	}
	w.line("}")
	w.line("")
}

// writeSteps writes one step pattern: 32 eighth notes, 64 sixteenths, or a
// short chord progression on a polyphonic track.
func (w *largeWriter) writeSteps(name string, t trackSpec, k int) {
	s := &w.rng
	header := "pattern " + name
	if s.intn(2) == 0 {
		header += " notes"
	}
	w.line("%s {", header)
	steps, perBar, step := 32, 8, "1/8"
	chords := false
	switch {
	case k%5 == 4 && t.poly:
		steps, perBar, step, chords = 8, 2, "1/2", true
	case k%3 == 2:
		steps, perBar, step = 64, 16, "1/16"
	}
	w.line("  step = %s", step)
	w.line("  gate = %d%%", s.between(60, 95))
	var cells []string
	var velocities []string
	degree := s.between(t.low, t.high)
	for i := range steps {
		if i > 0 && i%perBar == 0 {
			cells = append(cells, "|")
		}
		switch {
		case chords && i%2 == 1:
			cells = append(cells, "-")
			velocities = append(velocities, ".")
		case chords:
			root := s.between(t.low, t.high-4)
			cells = append(cells, fmt.Sprintf("[%s %s %s]", pitchName(scalePitch(root)), pitchName(scalePitch(root+2)), pitchName(scalePitch(root+4))))
			velocities = append(velocities, fmt.Sprintf("%d", s.between(60, 100)))
		case s.intn(5) < 2:
			cells = append(cells, ".")
			velocities = append(velocities, ".")
		default:
			degree += s.between(-2, 2)
			degree = max(t.low, min(t.high, degree))
			cells = append(cells, pitchName(scalePitch(degree)))
			velocities = append(velocities, fmt.Sprintf("%d", s.between(56, 110)))
		}
	}
	if name == LargeStepPattern {
		// togglestep and setpitch act on steps 1 and 0: keep a note on step 0.
		cells[0] = pitchName(scalePitch(t.low + 2))
	}
	if velocities[0] == "." {
		velocities[0] = fmt.Sprint(s.between(56, 110)) // a row starts with a value
	}
	if steps == 64 {
		// Two bars of sixteenths to a line.
		half := len(cells) / 2
		w.line("  %s", strings.Join(cells[:half], " "))
		w.line("  %s", strings.Join(cells[half:], " "))
	} else {
		w.line("  %s", strings.Join(cells, " "))
	}
	if s.intn(8) < 5 {
		w.line("  velocity: %s", strings.Join(velocities, " "))
	}
	w.line("}")
	w.line("")
}

// writePerformance writes a monophonic played line: no two notes overlap, so
// a 16-note move by a semitone never makes two gates of one pitch overlap.
func (w *largeWriter) writePerformance(name string, t trackSpec, notes int) {
	s := &w.rng
	type note struct{ pitch, at, length, velocity, bend, pressure, timbre int }
	var played []note
	at := s.between(0, 40)
	degree := (t.low + t.high) / 2
	for range notes {
		degree += s.between(-2, 2)
		if degree < t.low || degree > t.high {
			degree = (t.low + t.high) / 2
		}
		length := pick(s, 110, 160, 220, 240, 330, 480, 700)
		played = append(played, note{
			pitch: scalePitch(degree), at: at, length: length, velocity: s.between(52, 112),
			bend: s.between(-30, 30), pressure: s.between(20, 85), timbre: s.between(20, 85),
		})
		at += length + s.between(8, 140)
	}
	// The loop ends on the bar line at or after the last release.
	bar, offset := w.meter.BarAt(int64(at))
	if offset > 0 {
		bar++
	}
	w.line("pattern %s performance length=%dticks {", name, w.meter.BarStart(bar))
	for _, n := range played {
		w.line("  note %d at=%dticks length=%dticks velocity=%d { gesture at=0ticks bend=%dct pressure=%s timbre=%s }",
			n.pitch, n.at, n.length, n.velocity, n.bend, hundredths(n.pressure), hundredths(n.timbre))
	}
	w.line("}")
	w.line("")
}

func (w *largeWriter) writeAutomation(tracks []trackSpec) {
	s := &w.rng
	w.comment("Levels and pans that follow the form: eighteen lanes, a point every few bars, mostly smooth.")
	points := largeAutoPoints
	for lane := range largeAutomation {
		t := tracks[(lane*5)%len(tracks)]
		param := "level"
		if lane%3 == 2 && !t.drums {
			param = "pan"
		}
		count := points / (largeAutomation - lane)
		if lane < largeAutomation-1 {
			count += s.between(-2, 2)
		}
		points -= count
		// Small moves a few bars apart: a smooth segment becomes a control
		// every sixteenth and every 1/4096 of the range it crosses, and a song
		// holds at most 65,535 controls (project/automation.go).
		bar := 1 + 8*s.intn(6)
		level, pan := -s.between(8, 20), s.between(-40, 40)
		var parts []string
		for p := range count {
			if p > 0 {
				bar += s.between(2, 5)
				level = max(-24, min(-6, level+s.between(-3, 3)))
				pan = max(-70, min(70, pan+s.between(-12, 12)))
			}
			value := fmt.Sprintf("%ddB", level)
			if param == "pan" {
				value = hundredths(pan)
			}
			switch {
			case p == 0:
				parts = append(parts, fmt.Sprintf("@%d.1.1 %s", bar, value))
			case s.intn(7) == 0:
				parts = append(parts, fmt.Sprintf("@%d.%d.1 %s", bar, s.between(1, 4), value))
			default:
				parts = append(parts, fmt.Sprintf("@%d.%d.1 %s smooth", bar, s.between(1, 4), value))
			}
		}
		if lane%2 == 0 {
			w.comment(w.sentence("The %s of %s %s.", param, t.name))
		}
		w.line("automate %s.%s {", t.name, param)
		w.line("  %s", strings.Join(parts, " "))
		w.line("}")
		w.line("")
	}
	if points != 0 {
		panic("benchscore: REF-L automation points")
	}
}

func (w *largeWriter) writeArrange(tracks []trackSpec, owned map[string][]string) {
	s := &w.rng
	w.comment(fmt.Sprintf("%d bars in nine sections. Every track plays its own patterns, one after another.", largeBars))
	w.line("arrange {")
	sections := []string{"intro", "first-light", "gathering", "open-road", "turn", "second-light", "storm", "clearing", "outro"}
	for i, section := range sections {
		w.line("  marker %s {", section)
		w.line("    at = @%d.1.1", 1+i*largeBars/len(sections))
		w.line("  }")
	}
	// Ten placements per track, eleven on six tracks: 226. Each starts after
	// the one before it on its track ends, with a gap of zero to two bars.
	for i, t := range tracks {
		count := 10
		if i < largePlacements-10*len(tracks) {
			count = 11
		}
		patterns := owned[t.name]
		if i < 18 {
			w.line("  // %s", w.sentence("%s %s.", t.name))
			w.comments++
		}
		bar := 1 + s.intn(3)
		for k := range count {
			length := pick(s, 4, 4, 8, 6, 8)
			if bar+length > largeBars+1 {
				length = largeBars + 1 - bar
			}
			name := fmt.Sprintf("%s-part-%02d", t.name, k+1)
			content := patterns[k%len(patterns)]
			w.line("  place %s %s %s {", name, t.name, content)
			w.line("    at = @%d.1.1 length = %dbars", bar, length)
			w.line("  }")
			gap := s.between(0, 2)
			if name == LargePlacement {
				gap = max(gap, 1)
			}
			bar += length + gap
		}
		if bar > largeBars+3 {
			panic(fmt.Sprintf("benchscore: REF-L track %s runs past bar %d", t.name, largeBars))
		}
	}
	w.line("}")
	w.comment("End of the arrangement: the last placements ring out under the final marker.")
}
