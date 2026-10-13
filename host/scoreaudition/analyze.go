// Package scoreaudition describes what a rendered Cicada score sounds like.
// It renders the mix and per-track stems in process, then measures key,
// chords, timbre and rhythm so a score can be checked without listening.
// It is a host package: the kernel does not depend on it.
package scoreaudition

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"

	"m31labs.dev/cicada/host/schedule"
	"m31labs.dev/cicada/kernel/seq"
	"m31labs.dev/cicada/notation"
	"m31labs.dev/cicada/project"
	"m31labs.dev/cicada/render"
)

// FormatVersion names the JSON schema of Report.
const FormatVersion = "cicada.audition/1"

// Options selects the render and the bar range.
type Options struct {
	SampleRate int // default 48000
	From       int // zero-based first bar
	Bars       int // zero analyzes through the song end
	AssetDir   string
	TempDir    string // default os.TempDir()
}

type Report struct {
	Format       string          `json:"format"`
	Title        string          `json:"title"`
	SampleRate   int             `json:"sample_rate"`
	Score        Facts           `json:"score"`
	HeardKey     []keyGuess      `json:"heard_key"`    // from the summed pitched stems
	FullMixKey   []keyGuess      `json:"full_mix_key"` // from the whole mix, drums included
	AudibilityDB float64         `json:"audibility_db"`
	MaskedBars   int             `json:"masked_bars"`
	Bars         []BarReport     `json:"bars"`
	Tracks       []TrackReport   `json:"tracks"`
	Sections     []SectionReport `json:"sections"`
	Mismatches   int             `json:"harmony_mismatches"`
	Images       []string        `json:"images,omitempty"`
}

type Facts struct {
	TempoBPM   float64      `json:"tempo_bpm"`
	WrittenKey string       `json:"written_key"`
	SongBars   int          `json:"song_bars"`
	FromBar    int          `json:"from_bar"`
	Bars       int          `json:"bars"`
	Swing      []SwingGroup `json:"swing,omitempty"`
}

// SwingGroup lists the tracks and patterns that share one swing value.
type SwingGroup struct {
	SwingPercent float64  `json:"swing_percent"`
	Tracks       []string `json:"tracks"`
	Patterns     []string `json:"patterns"`
}

type BarReport struct {
	Bar          int      `json:"bar"`
	WrittenNotes []string `json:"written_notes"`
	Written      string   `json:"written"`
	Heard        string   `json:"heard"`
	HeardFit     float64  `json:"heard_fit"`
	MixRMSdBFS   float64  `json:"mix_rms_dbfs"`
	AudibilityDB float64  `json:"audibility_db"` // pitched stems vs whole mix, 125 Hz to 2 kHz
	Masked       bool     `json:"masked"`        // audibility_db below -15
	Match        string   `json:"match"`         // agree, disagree, no-written-pitch, silent
}

type TrackReport struct {
	ID                string    `json:"id"`
	Silent            bool      `json:"silent"`
	RMSdBFS           float64   `json:"rms_dbfs"`
	LevelVsLoudestDB  float64   `json:"level_vs_loudest_db"`
	EnergySharePct    float64   `json:"energy_share_pct"`
	BandVsMixDB       float64   `json:"band_vs_mix_db"` // 125 Hz-2 kHz energy re the whole mix
	Masked            bool      `json:"masked"`         // band_vs_mix_db below -15
	CentroidHz        float64   `json:"centroid_hz"`
	AttackMs          float64   `json:"attack_ms"`
	Decay20Ms         float64   `json:"decay20_ms"`
	Decay20LowerBound bool      `json:"decay20_lower_bound"`
	Onsets            int       `json:"onsets"`
	ActiveBars        int       `json:"active_bars"` // bars where the track plays or sounds
	OnsetsPerBar      float64   `json:"onsets_per_bar"`
	SwingPercent      float64   `json:"swing_percent"`
	Rhythm            []float64 `json:"rhythm"`
}

type SectionReport struct {
	Name         string   `json:"name"`
	FromBar      int      `json:"from_bar"`
	ToBar        int      `json:"to_bar"`
	RMSdBFS      float64  `json:"rms_dbfs"`
	CentroidHz   float64  `json:"centroid_hz"`
	OnsetsPerBar float64  `json:"onsets_per_bar"`
	Tracks       []string `json:"tracks"`
}

// Analysis holds the report and the data behind the images.
type Analysis struct {
	Report Report

	rate        int
	specRows    [][]float32 // frame x row, dB
	specHop     int
	barCols     []int // spectrogram column of each bar line
	barNumbers  []int
	chroma      [][12]float64
	rhythmRows  [][]float64
	firstBarIdx int
}

const (
	silentDB = -80.0
	// maskedBelowDB is the audibility under which the pitched parts count as
	// masked: they hold under about 3% of the mix energy in the chord band.
	maskedBelowDB = -15.0
	bandLowHz     = 125
	bandHighHz    = 2000
	specRows      = 160
	chromaFFT     = 8192
	onsetFFT      = 1024
	onsetHop      = 256
	specFFT       = 4096
	specHopSize   = 1024
)

func db(x float64) float64 {
	if x <= 1e-12 {
		return -120
	}
	return math.Max(-120, 20*math.Log10(x))
}

func round(x float64, places int) float64 {
	m := math.Pow(10, float64(places))
	r := math.Round(x*m) / m
	if r == 0 {
		return 0
	}
	return r
}

// Analyze renders the score and measures it.
func Analyze(score *notation.Score, opts Options) (*Analysis, error) {
	if score == nil {
		return nil, fmt.Errorf("nil score")
	}
	if opts.SampleRate == 0 {
		opts.SampleRate = 48000
	}
	rate := opts.SampleRate
	p, ds := project.FromScore(score)
	if p == nil {
		for _, d := range ds {
			if d.Severity == "error" {
				return nil, fmt.Errorf("%s: %s", d.Code, d.Message)
			}
		}
		return nil, fmt.Errorf("score cannot compile to a Cicada project")
	}
	root := opts.AssetDir
	if root == "" {
		root = "."
	}
	cfg, err := schedule.Compile(p, root, rate, 4096)
	if err != nil {
		return nil, err
	}
	events, err := project.CompileSchedule(p)
	if err != nil {
		return nil, err
	}
	var endTick int64
	for _, e := range events {
		endTick = max(endTick, e.Tick, e.EndTick)
	}
	songBars := int((endTick + seq.TicksPerBar - 1) / seq.TicksPerBar)
	if opts.From < 0 || opts.From >= songBars {
		return nil, fmt.Errorf("start bar must be within the song")
	}
	if opts.Bars < 0 || opts.Bars > songBars-opts.From {
		return nil, fmt.Errorf("requested bars must fit between the start bar and song end")
	}
	bars := opts.Bars
	if bars == 0 {
		bars = songBars - opts.From
	}
	// The written notes are the note events the renderer plays, tapped during
	// the same render that produces the stems, so chains, scene phase,
	// arrangements, ties, chance and transposition are exactly what sounded.
	var notes noteLog
	tmp, err := os.MkdirTemp(opts.TempDir, "cicada-audition-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmp)
	stemDir := filepath.Join(tmp, "stems")
	ropts := render.Options{SampleRate: rate, Bits: 32, From: opts.From, Bars: bars, AssetRoot: opts.AssetDir, AssetDir: opts.AssetDir, NoteTap: notes.tap}
	if _, err := render.Stems(score, ropts, stemDir); err != nil {
		return nil, err
	}
	mix, _, err := readFloatWAV(filepath.Join(stemDir, "master.wav"))
	if err != nil {
		return nil, err
	}

	clock, err := seq.NewClock(rate, int64(p.TempoMilli))
	if err != nil {
		return nil, err
	}
	baseSample := clock.SampleAtTick(int64(opts.From) * seq.TicksPerBar)
	barSample := func(b int) int { // sample of analyzed bar b, relative to render start
		return int(clock.SampleAtTick(int64(opts.From+b)*seq.TicksPerBar) - baseSample)
	}
	bpm := float64(p.TempoMilli) / 1000

	a := &Analysis{rate: rate, specHop: specHopSize, firstBarIdx: opts.From}
	r := &a.Report
	r.Format, r.Title, r.SampleRate = FormatVersion, p.Title, rate
	r.Score = Facts{TempoBPM: round(bpm, 3), WrittenKey: fmt.Sprintf("%s %s", noteNames[p.Key.Root%12], p.Key.Scale), SongBars: songBars, FromBar: opts.From + 1, Bars: bars}

	// Read the stems and sum the pitched ones: the heard harmony comes from
	// them, so drums and their pitched noise do not decide the chord.
	stems := make([][]float32, len(p.Tracks))
	pitched := make([]bool, len(p.Tracks))
	pitchedMix := make([]float32, len(mix))
	for ti, track := range p.Tracks {
		s, _, err := readFloatWAV(filepath.Join(stemDir, fmt.Sprintf("%02d-%s.wav", ti+1, track.ID)))
		if err != nil {
			return nil, err
		}
		stems[ti] = s
		pitched[ti] = ti < len(cfg.Track) && pitchedKind(cfg.Track[ti].Kind)
		if pitched[ti] {
			for i := 0; i < len(pitchedMix) && i < len(s); i++ {
				pitchedMix[i] += s[i]
			}
		}
	}

	// Chroma and band power, one frame per sixteenth note.
	frames := bars * 16
	chromaPlan := newPlan(chromaFFT)
	binPC := make([]int, chromaFFT/2+1)
	inBand := make([]bool, chromaFFT/2+1)
	for k := range binPC {
		f := float64(k) * float64(rate) / chromaFFT
		binPC[k] = -1
		if f >= bandLowHz && f <= bandHighHz {
			binPC[k] = ((int(math.Round(12*math.Log2(f/440)+9)) % 12) + 12) % 12
			inBand[k] = true
		}
	}
	chromaOf := func(sig []float32) ([][12]float64, []float64) {
		chroma := make([][12]float64, frames)
		band := make([]float64, frames)
		magFrames(sig, chromaPlan, frames, func(i int) int {
			tick := int64(opts.From)*seq.TicksPerBar + int64(i)*(seq.TicksPerStep) + seq.TicksPerStep/2
			return int(clock.SampleAtTick(tick)-baseSample) - chromaFFT/2
		}, func(i int, mag []float64) {
			// Only spectral peaks count for chroma: broadband noise then
			// adds little to any pitch class.
			for k, pc := range binPC {
				if pc < 0 {
					continue
				}
				band[i] += mag[k] * mag[k]
				if k > 0 && k+1 < len(mag) && mag[k] > mag[k-1] && mag[k] >= mag[k+1] {
					chroma[i][pc] += mag[k]
				}
			}
		})
		return chroma, band
	}
	trackBand := func(sig []float32) float64 {
		var total float64
		magFrames(sig, chromaPlan, frames, func(i int) int {
			tick := int64(opts.From)*seq.TicksPerBar + int64(i)*(seq.TicksPerStep) + seq.TicksPerStep/2
			return int(clock.SampleAtTick(tick)-baseSample) - chromaFFT/2
		}, func(_ int, mag []float64) {
			for k := range mag {
				if inBand[k] {
					total += mag[k] * mag[k]
				}
			}
		})
		return total
	}
	var mixBand, pitchedBand []float64
	a.chroma, pitchedBand = chromaOf(pitchedMix)
	var mixChroma [][12]float64
	mixChroma, mixBand = chromaOf(mix)
	sumChroma := func(c [][12]float64) (t [12]float64) {
		for _, f := range c {
			for i := range t {
				t[i] += f[i]
			}
		}
		return t
	}
	r.HeardKey = estimateKey(sumChroma(a.chroma))
	r.FullMixKey = estimateKey(sumChroma(mixChroma))
	bandDB := func(pitchedPower, mixPower float64) float64 {
		if pitchedPower <= 0 || mixPower <= 0 {
			return -120
		}
		return math.Max(-120, 10*math.Log10(pitchedPower/mixPower))
	}
	var allP, allM float64
	for i := range mixBand {
		allP += pitchedBand[i]
		allM += mixBand[i]
	}
	r.AudibilityDB = round(bandDB(allP, allM), 1)

	// Per-bar harmony.
	fromTick := int64(opts.From) * seq.TicksPerBar
	sets := notes.writtenSets(pitched, fromTick, bars)
	for b := 0; b < bars; b++ {
		var v [12]float64
		var bp, bm float64
		for f := 0; f < 16; f++ {
			for i := range v {
				v[i] += a.chroma[b*16+f][i]
			}
			bp += pitchedBand[b*16+f]
			bm += mixBand[b*16+f]
		}
		lo, hi := min(barSample(b), len(mix)), min(barSample(b+1), len(mix))
		pitchedRMS := rmsDB(pitchedMix[lo:hi])
		heard := bestChord(v)
		name, wm, count := writtenChord(sets[b])
		aud := bandDB(bp, bm)
		br := BarReport{Bar: opts.From + b + 1, WrittenNotes: []string{}, Written: name, Heard: heard.Name, HeardFit: heard.Score,
			MixRMSdBFS: round(rmsDB(mix[lo:hi]), 1), AudibilityDB: round(aud, 1), Masked: aud < maskedBelowDB}
		if br.Masked {
			r.MaskedBars++
		}
		for pc, on := range sets[b] {
			if on {
				br.WrittenNotes = append(br.WrittenNotes, noteNames[pc])
			}
		}
		switch {
		case pitchedRMS < silentDB:
			br.Heard, br.HeardFit, br.Match = "", 0, "silent"
			if count == 0 {
				br.Match = "no-written-pitch"
			} else {
				r.Mismatches++
				br.Match = "disagree"
			}
		case count == 0:
			br.Match = "no-written-pitch"
		case chordsAgree(sets[b], wm, heard, count):
			br.Match = "agree"
		default:
			br.Match, r.Mismatches = "disagree", r.Mismatches+1
		}
		r.Bars = append(r.Bars, br)
	}

	// Mix spectrogram.
	specPlan := newPlan(specFFT)
	nSpec := len(mix)/specHopSize + 1
	a.specRows = make([][]float32, nSpec)
	rowLo := make([]int, specRows)
	rowHi := make([]int, specRows)
	rowFrac := make([]float64, specRows)
	binHz := float64(rate) / specFFT
	ratio := math.Pow(12000.0/40.0, 1.0/specRows)
	for row := 0; row < specRows; row++ {
		lo, hi := 40*math.Pow(ratio, float64(row)), 40*math.Pow(ratio, float64(row+1))
		kl, kh := int(math.Ceil(lo/binHz)), int(math.Ceil(hi/binHz))
		if kh > kl {
			rowLo[row], rowHi[row] = kl, kh
		} else {
			k := math.Sqrt(lo*hi) / binHz
			rowLo[row], rowHi[row], rowFrac[row] = int(math.Floor(k)), -1, k-math.Floor(k)
		}
	}
	centNum := make([]float64, nSpec)
	centDen := make([]float64, nSpec)
	magFrames(mix, specPlan, nSpec, func(i int) int { return i*specHopSize - specFFT/2 }, func(i int, mag []float64) {
		row := make([]float32, specRows)
		for j := 0; j < specRows; j++ {
			var v float64
			if rowHi[j] < 0 {
				v = mag[rowLo[j]]*(1-rowFrac[j]) + mag[min(rowLo[j]+1, len(mag)-1)]*rowFrac[j]
			} else {
				for k := rowLo[j]; k < rowHi[j] && k < len(mag); k++ {
					v += mag[k]
				}
				v /= float64(rowHi[j] - rowLo[j])
			}
			row[j] = float32(20 * math.Log10(v+1e-9))
		}
		a.specRows[i] = row
		for k, m := range mag {
			centNum[i] += m * float64(k) * binHz
			centDen[i] += m
		}
	})
	for b := 0; b <= bars; b++ {
		a.barCols = append(a.barCols, barSample(b)/specHopSize)
		a.barNumbers = append(a.barNumbers, opts.From+b)
	}

	// Tracks.
	type trackWork struct {
		onsets []int // sample positions
		energy float64
	}
	work := make([]trackWork, len(p.Tracks))
	onsetPlan := newPlan(onsetFFT)
	acts := notes.activity(cfg, len(p.Tracks), fromTick, bars)
	// Per-bar stem energy: a track with no note events (an audio clip) is
	// active where its stem sounds.
	barSounding := make([][]bool, len(p.Tracks))
	for ti := range p.Tracks {
		barSounding[ti] = make([]bool, bars)
		for b := 0; b < bars; b++ {
			lo, hi := min(barSample(b), len(stems[ti])), min(barSample(b+1), len(stems[ti]))
			barSounding[ti][b] = hi > lo && rmsDB(stems[ti][lo:hi]) >= silentDB
		}
		if !acts[ti].hasEvents {
			copy(acts[ti].active, barSounding[ti])
		}
	}
	var maxEnergy float64
	for ti, track := range p.Tracks {
		tr := TrackReport{ID: track.ID, Rhythm: make([]float64, 16)}
		s := stems[ti]
		var energy float64
		for _, v := range s {
			energy += float64(v) * float64(v)
		}
		work[ti].energy = energy
		tr.RMSdBFS = round(rmsDB(s), 1)
		if rmsDB(s) < silentDB {
			tr.Silent = true
			r.Tracks = append(r.Tracks, tr)
			continue
		}
		maxEnergy = math.Max(maxEnergy, energy)
		env := onsetEnvelope(s, onsetPlan)
		peaks := pickPeaks(env)
		tr.Onsets = len(peaks)
		for _, pk := range peaks {
			work[ti].onsets = append(work[ti].onsets, pk*onsetHop)
		}
		activeBars := 0
		for _, on := range acts[ti].active {
			if on {
				activeBars++
			}
		}
		if activeBars == 0 {
			activeBars = bars
		}
		tr.ActiveBars = activeBars
		tr.OnsetsPerBar = round(float64(tr.Onsets)/float64(activeBars), 2)
		tr.CentroidHz = round(trackCentroid(s, rate), 0)
		tr.BandVsMixDB = round(bandDB(trackBand(s), allM), 1)
		tr.Masked = tr.BandVsMixDB < maskedBelowDB
		tr.AttackMs, tr.Decay20Ms, tr.Decay20LowerBound = noteShape(s, peaks, rate)
		tr.Rhythm, tr.SwingPercent = rhythmProfile(env, rate, clock, baseSample, opts.From, bars, acts[ti])
		r.Tracks = append(r.Tracks, tr)
		a.rhythmRows = append(a.rhythmRows, tr.Rhythm)
	}
	for ti := range r.Tracks {
		if r.Tracks[ti].Silent {
			continue
		}
		share := work[ti].energy / maxEnergy
		r.Tracks[ti].LevelVsLoudestDB = round(10*math.Log10(share), 1)
	}
	var sumEnergy float64
	for _, w := range work {
		sumEnergy += w.energy
	}
	for ti := range r.Tracks {
		if sumEnergy > 0 {
			r.Tracks[ti].EnergySharePct = round(100*work[ti].energy/sumEnergy, 1)
		}
	}

	// Swing facts: one group per distinct swing value.
	patternSwing := map[string]float64{}
	for _, pat := range p.Patterns {
		patternSwing[pat.ID] = float64(pat.SwingPercent100) / 100
	}
	groups := map[float64]*SwingGroup{}
	trackSet, patternSet := map[float64]map[string]bool{}, map[float64]map[string]bool{}
	for _, pl := range notes.slotsUsed() {
		if pl.track >= len(p.Tracks) || pl.slot >= len(p.Tracks[pl.track].Slots) {
			continue
		}
		id := p.Tracks[pl.track].Slots[pl.slot]
		if id == nil || patternSwing[*id] == 50 {
			continue
		}
		v := round(patternSwing[*id], 2)
		if groups[v] == nil {
			groups[v] = &SwingGroup{SwingPercent: v}
			trackSet[v], patternSet[v] = map[string]bool{}, map[string]bool{}
		}
		trackSet[v][p.Tracks[pl.track].ID] = true
		patternSet[v][*id] = true
	}
	for v, g := range groups {
		for t := range trackSet[v] {
			g.Tracks = append(g.Tracks, t)
		}
		for n := range patternSet[v] {
			g.Patterns = append(g.Patterns, n)
		}
		sort.Strings(g.Tracks)
		sort.Strings(g.Patterns)
		r.Score.Swing = append(r.Score.Swing, *g)
	}
	sort.Slice(r.Score.Swing, func(i, j int) bool { return r.Score.Swing[i].SwingPercent < r.Score.Swing[j].SwingPercent })

	// Sections.
	type span struct {
		name     string
		from, to int // absolute bars, to exclusive
	}
	var spans []span
	if p.Arrange == nil {
		at := 0
		for _, e := range p.Song {
			spans = append(spans, span{e.Scene, at, at + int(e.Bars)})
			at += int(e.Bars)
		}
	} else {
		ms := append([]project.Marker(nil), p.Arrange.Markers...)
		sort.SliceStable(ms, func(i, j int) bool { return ms[i].AtTick < ms[j].AtTick })
		if len(ms) == 0 || ms[0].AtTick > 0 {
			ms = append([]project.Marker{{ID: "start"}}, ms...)
		}
		for i, m := range ms {
			to := songBars
			if i+1 < len(ms) {
				to = int(ms[i+1].AtTick / seq.TicksPerBar)
			}
			spans = append(spans, span{m.ID, int(m.AtTick / seq.TicksPerBar), to})
		}
	}
	for _, sp := range spans {
		lo, hi := max(sp.from, opts.From), min(sp.to, opts.From+bars)
		if hi <= lo {
			continue
		}
		s0, s1 := min(barSample(lo-opts.From), len(mix)), min(barSample(hi-opts.From), len(mix))
		sec := SectionReport{Name: sp.name, FromBar: lo + 1, ToBar: hi, RMSdBFS: round(rmsDB(mix[s0:s1]), 1), Tracks: []string{}}
		var num, den float64
		for i := 0; i < nSpec; i++ {
			if c := i * specHopSize; c >= s0 && c < s1 {
				num += centNum[i]
				den += centDen[i]
			}
		}
		if den > 0 {
			sec.CentroidHz = round(num/den, 0)
		}
		count := 0
		for ti := range p.Tracks {
			for _, o := range work[ti].onsets {
				if o >= s0 && o < s1 {
					count++
				}
			}
			// A track belongs to a section when its stem sounds there or it
			// played notes there.
			for b := lo; b < hi; b++ {
				if acts[ti].active[b-opts.From] || acts[ti].played[b-opts.From] || barSounding[ti][b-opts.From] {
					sec.Tracks = append(sec.Tracks, p.Tracks[ti].ID)
					break
				}
			}
		}
		sec.OnsetsPerBar = round(float64(count)/float64(hi-lo), 2)
		r.Sections = append(r.Sections, sec)
	}
	return a, nil
}

func rmsDB(s []float32) float64 {
	if len(s) == 0 {
		return -120
	}
	var sum float64
	for _, v := range s {
		sum += float64(v) * float64(v)
	}
	return db(math.Sqrt(sum / float64(len(s))))
}

// onsetEnvelope is the positive log-magnitude spectral flux; element i is
// centered at sample i*onsetHop.
func onsetEnvelope(s []float32, plan *fftPlan) []float64 {
	count := len(s)/onsetHop + 1
	env := make([]float64, count)
	prev := make([]float64, onsetFFT/2+1)
	cur := make([]float64, onsetFFT/2+1)
	magFrames(s, plan, count, func(i int) int { return i*onsetHop - onsetFFT/2 }, func(i int, mag []float64) {
		var flux float64
		for k, m := range mag {
			cur[k] = math.Log1p(m * 10)
			if i > 0 && cur[k] > prev[k] {
				flux += cur[k] - prev[k]
			}
		}
		env[i] = flux
		prev, cur = cur, prev
	})
	return env
}

// pickPeaks returns env indexes of local maxima above 30 percent of the maximum.
func pickPeaks(env []float64) []int {
	var top float64
	for _, v := range env {
		top = math.Max(top, v)
	}
	var out []int
	for i := 1; i+1 < len(env); i++ {
		if env[i] > env[i-1] && env[i] >= env[i+1] && env[i] > top*0.3 {
			out = append(out, i)
		}
	}
	return out
}

func trackCentroid(s []float32, rate int) float64 {
	plan := newPlan(specFFT)
	count := len(s)/2048 + 1
	var num, den float64
	binHz := float64(rate) / specFFT
	magFrames(s, plan, count, func(i int) int { return i * 2048 }, func(_ int, mag []float64) {
		for k, m := range mag {
			num += m * float64(k) * binHz
			den += m
		}
	})
	if den == 0 {
		return 0
	}
	return num / den
}

// noteShape returns the median attack time and the median time for the
// envelope to fall 20 dB below its peak. Each note is measured only up to the
// next detected attack (or 2 s). A note that does not fall 20 dB by then
// reports the gap as a lower bound; the flag is true when the median note is
// such a lower bound.
func noteShape(s []float32, peaks []int, rate int) (attackMs, decayMs float64, lowerBound bool) {
	// 5 ms centered RMS envelope.
	blk := int(float64(rate) * 0.005)
	prefix := make([]float64, len(s)+1)
	for i, v := range s {
		prefix[i+1] = prefix[i] + float64(v)*float64(v)
	}
	env := func(i int) float64 {
		lo, hi := max(0, i-blk/2), min(len(s), i+blk/2+1)
		return math.Sqrt(math.Max(0, prefix[hi]-prefix[lo])/float64(hi-lo) + 1e-12)
	}
	type dec struct {
		ms    float64
		bound bool
	}
	var attacks []float64
	var decays []dec
	limit := len(peaks)
	if limit > 400 {
		limit = 400
	}
	for n := 0; n < limit; n++ {
		t0 := max(0, peaks[n]*onsetHop-int(float64(rate)*0.012))
		t1 := min(len(s), t0+2*rate)
		if n+1 < len(peaks) {
			t1 = min(t1, max(0, peaks[n+1]*onsetHop-int(float64(rate)*0.012)))
		}
		if t1-t0 < blk*2 {
			continue
		}
		window := min(t1-t0, int(float64(rate)*0.15))
		pk, pv := 0, 0.0
		for i := 0; i < window; i++ {
			if e := env(t0 + i); e > pv {
				pk, pv = i, e
			}
		}
		if pv < 1e-4 {
			continue
		}
		attacks = append(attacks, float64(pk)/float64(rate)*1000)
		d := dec{ms: float64(t1-t0-pk) / float64(rate) * 1000, bound: true}
		for i := pk + 1; t0+i < t1; i++ {
			if env(t0+i) <= pv*0.1 {
				d = dec{ms: float64(i-pk) / float64(rate) * 1000}
				break
			}
		}
		decays = append(decays, d)
	}
	if len(attacks) == 0 {
		return 0, 0, false
	}
	sort.Float64s(attacks)
	sort.SliceStable(decays, func(i, j int) bool { return decays[i].ms < decays[j].ms })
	m := decays[len(decays)/2]
	return round(attacks[len(attacks)/2], 1), round(m.ms, 0), m.bound
}

// rhythmProfile returns the mean onset strength at each sixteenth of the bar,
// scaled so the largest is 1, over the bars where the track plays a pattern.
// Swung slots are looked up where the pattern's swing places them.
func rhythmProfile(env []float64, rate int, clock seq.Clock, base int64, from, bars int, act barActivity) ([]float64, float64) {
	prof, cnt := make([]float64, 16), make([]float64, 16)
	swing := 50.0
	swingSet := false
	for b := 0; b < bars; b++ {
		if !act.active[b] {
			continue
		}
		pat := act.pattern[b]
		for s := 0; s < 16; s++ {
			tick := int64(s) * seq.TicksPerStep
			if pat != nil && pat.Len > 0 {
				g := pat.GridTicks()
				if tick%g == 0 && (tick/g)%2 == 1 {
					tick += (g*int64(pat.SwingPermille) + 500) / 1000
				}
				if !swingSet {
					swing, swingSet = 50+float64(pat.SwingPermille)/20, true
				}
			}
			sample := clock.SampleAtTick(int64(from+b)*seq.TicksPerBar+tick) - base
			t := float64(sample) / float64(rate)
			i0 := int(math.Ceil((t - 0.02) * float64(rate) / onsetHop))
			i1 := int(math.Floor((t + 0.03) * float64(rate) / onsetHop))
			if i1 >= len(env) {
				continue
			}
			var m float64
			for i := max(0, i0); i <= i1; i++ {
				m = math.Max(m, env[i])
			}
			prof[s] += m
			cnt[s]++
		}
	}
	var top float64
	for s := range prof {
		if cnt[s] > 0 {
			prof[s] /= cnt[s]
		}
		top = math.Max(top, prof[s])
	}
	for s := range prof {
		if top > 0 {
			prof[s] = round(prof[s]/top, 3)
		}
	}
	return prof, round(swing, 1)
}
