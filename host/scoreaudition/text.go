package scoreaudition

import (
	"fmt"
	"io"
	"math"
	"strings"
)

var blocks = []rune("·▁▂▃▄▅▆▇█")

func rhythmRow(profile []float64) string {
	var b strings.Builder
	for i, v := range profile {
		if i > 0 && i%4 == 0 {
			b.WriteByte(' ')
		}
		b.WriteRune(blocks[int(math.Round(math.Max(0, math.Min(1, v))*8))])
	}
	return b.String()
}

// WriteText prints the report for a person or an agent.
func (r *Report) WriteText(w io.Writer) {
	f := r.Score
	fmt.Fprintf(w, "== %s: %d bars from bar %d (song has %d) at %g BPM, written key %s\n", r.Title, f.Bars, f.FromBar, f.SongBars, f.TempoBPM, f.WrittenKey)
	if len(f.TempoChanges) > 0 {
		var steps []string
		for _, c := range f.TempoChanges {
			steps = append(steps, fmt.Sprintf("bar %d: %g BPM", c.Bar, c.BPM))
		}
		fmt.Fprintf(w, "tempo changes (the score tempo applies before the first): %s\n", strings.Join(steps, ", "))
	}
	for _, s := range f.Swing {
		fmt.Fprintf(w, "swing %g%%: %s\n", s.SwingPercent, strings.Join(s.Tracks, ", "))
	}
	keyLine := func(gs []keyGuess) string {
		var keys []string
		for _, k := range gs {
			keys = append(keys, fmt.Sprintf("%s (%.2f)", k.Key, k.Score))
		}
		return strings.Join(keys, ", ")
	}
	fmt.Fprintf(w, "heard key (pitched stems): %s\n", keyLine(r.HeardKey))
	fmt.Fprintf(w, "full mix key: %s\n", keyLine(r.FullMixKey))
	fmt.Fprintf(w, "audibility: pitched stems are %.1f dB re the mix in 125 Hz-2 kHz (MASKED below %g dB); %d of %d bars masked\n", r.AudibilityDB, maskedBelowDB, r.MaskedBars, len(r.Bars))
	fmt.Fprintf(w, "\nharmony per bar, heard from the pitched stems (%d of %d disagree):\n", r.Mismatches, len(r.Bars))
	fmt.Fprintf(w, "  %4s  %-14s %-8s %6s %7s  %s\n", "bar", "written", "heard", "mix dB", "aud dB", "match")
	for _, b := range r.Bars {
		flag := b.Match
		if b.Match == "disagree" {
			flag = "DISAGREE"
		}
		if b.Masked {
			flag += " MASKED"
		}
		fmt.Fprintf(w, "  %4d  %-14s %-8s %6.1f %7.1f  %s\n", b.Bar, b.Written, b.Heard, b.MixRMSdBFS, b.AudibilityDB, flag)
	}
	fmt.Fprintf(w, "\ntracks (level: RMS dBFS and dB under the loudest track; band dB: 125 Hz-2 kHz energy re the whole mix, a track under -15 is masked; bright: centroid Hz; attack and decay20 in ms; > marks a decay that did not fall 20 dB before the next attack):\n")
	fmt.Fprintf(w, "  %-10s %7s %7s %6s %8s %7s %7s %8s %7s  %s\n", "track", "rms", "vs top", "share", "band dB", "bright", "attack", "decay20", "on/bar", "step 1-16 (swing)")
	for _, t := range r.Tracks {
		if t.Silent {
			fmt.Fprintf(w, "  %-10s silent\n", t.ID)
			continue
		}
		bound := " "
		if t.Decay20LowerBound {
			bound = ">"
		}
		fmt.Fprintf(w, "  %-10s %7.1f %7.1f %5.1f%% %8.1f %7.0f %7.1f %s%7.0f %7.2f  %s (%.0f%%)\n", t.ID, t.RMSdBFS, t.LevelVsLoudestDB, t.EnergySharePct, t.BandVsMixDB, t.CentroidHz, t.AttackMs, bound, t.Decay20Ms, t.OnsetsPerBar, rhythmRow(t.Rhythm), t.SwingPercent)
	}
	fmt.Fprintf(w, "\nsections:\n")
	for _, s := range r.Sections {
		fmt.Fprintf(w, "  %-12s bars %d-%d  %g BPM  %6.1f dBFS  bright %5.0f Hz  %5.2f onsets/bar  %s\n", s.Name, s.FromBar, s.ToBar, s.TempoBPM, s.RMSdBFS, s.CentroidHz, s.OnsetsPerBar, strings.Join(s.Tracks, " "))
	}
	r.writeEnergy(w)
	r.writeBands(w)
	r.writeActivity(w)
	r.writePeaks(w)
	if len(r.Images) > 0 {
		fmt.Fprintf(w, "\nimages: %s\n", strings.Join(r.Images, ", "))
	}
	if len(r.Pictures) > len(r.Images) {
		var more []string
		for _, p := range r.Pictures[len(r.Images):] {
			more = append(more, p.File)
		}
		fmt.Fprintf(w, "more pictures: %s\n", strings.Join(more, ", "))
	}
}

func lufsText(v *float64) string {
	if v == nil {
		return "n/a"
	}
	return fmt.Sprintf("%.1f", *v)
}

// levelBlock draws a level re a top as a block: full within 3 dB, one step per
// 3 dB below, and a dot more than 24 dB under or with no value.
func levelBlock(v *float64, top float64) rune {
	if v == nil {
		return '·'
	}
	step := int(math.Floor((top - *v) / 3))
	if step < 0 {
		step = 0
	}
	if step >= 8 {
		return '·'
	}
	return blocks[8-step]
}

func (r *Report) writeEnergy(w io.Writer) {
	l := r.Loudness
	if l == nil {
		return
	}
	fmt.Fprintf(w, "\nenergy (ITU-R BS.1770-4, the render's loudness meter; short-term = 3 s window ending at the bar line):\n")
	fmt.Fprintf(w, "  whole range: %s LUFS integrated, loudness range %.1f LU, true peak %.1f dBTP, max short-term %s LUFS\n", lufsText(l.IntegratedLUFS), l.LoudnessRangeLU, l.TruePeakDBTP, lufsText(l.MaxShortTermLUFS))
	top := -120.0
	for _, b := range r.Bars {
		if b.ShortTermLUFS != nil {
			top = math.Max(top, *b.ShortTermLUFS)
		}
	}
	fmt.Fprintf(w, "  %-14s %-7s %6s %6s %5s %7s  %s\n", "section", "bars", "LUFS", "vs top", "LRA", "TP dBTP", "short-term per bar (█ within 3 LU of the loudest bar, one step per 3 LU, · 24 LU under or n/a)")
	for _, s := range r.Sections {
		var cells []rune
		for _, b := range r.Bars {
			if b.Bar >= s.FromBar && b.Bar <= s.ToBar {
				cells = append(cells, levelBlock(b.ShortTermLUFS, top))
			}
		}
		fmt.Fprintf(w, "  %-14s %-7s %6s %6s %5.1f %7.1f  %s\n", s.Name, fmt.Sprintf("%d-%d", s.FromBar, s.ToBar), lufsText(s.IntegratedLUFS), lufsText(s.VsLoudestLU), s.LoudnessRangeLU, s.TruePeakDBTP, string(cells))
	}
	if l.LoudestSection != "" {
		var loud, quiet *float64
		for _, s := range r.Sections {
			if s.Name == l.LoudestSection && loud == nil {
				loud = s.IntegratedLUFS
			}
			if s.Name == l.QuietestSection && quiet == nil {
				quiet = s.IntegratedLUFS
			}
		}
		fmt.Fprintf(w, "  loudest section: %s (%s LUFS); quietest: %s (%s LUFS, %s LU under)\n", l.LoudestSection, lufsText(loud), l.QuietestSection, lufsText(quiet), lufsText(l.SectionSpreadLU))
	}
}

func shortID(id string, n int) string {
	if len([]rune(id)) > n {
		return string([]rune(id)[:n])
	}
	return id
}

// barRanges writes bar numbers as ranges: 9-12, 14.
func barRanges(bars []int) string {
	var parts []string
	for i := 0; i < len(bars); {
		j := i
		for j+1 < len(bars) && bars[j+1] == bars[j]+1 {
			j++
		}
		if j > i {
			parts = append(parts, fmt.Sprintf("%d-%d", bars[i], bars[j]))
		} else {
			parts = append(parts, fmt.Sprintf("%d", bars[i]))
		}
		i = j + 1
	}
	return strings.Join(parts, ", ")
}

var bandLabels = [numBands]string{"sub 20-40", "low 40-80", "bass 80-160", "low-mid 160-400", "mid 400-1.6k", "presence 1.6-5k", "air 5-20k"}

func (r *Report) writeBands(w io.Writer) {
	if r.Bands == nil || len(r.Bars) == 0 || len(r.Bars[0].Bands) == 0 {
		return
	}
	fmt.Fprintf(w, "\nband owners per bar (share of the analysed tracks' energy in each band, Hz: %s; * collision: two tracks each over %g%%; quiet: %g dB under the band's loudest bar, %g dB under the bar's loudest band or %g dB under the loudest band of the range):\n", strings.Join(bandLabels[:], ", "), r.Bands.CollisionSharePct, r.Bands.QuietBelowBandDB, r.Bands.QuietBelowSpanDB, r.Bands.QuietBelowRangeDB)
	fmt.Fprintf(w, "  %4s", "bar")
	for _, name := range []string{"sub", "low", "bass", "low-mid", "mid", "presence", "air"} {
		fmt.Fprintf(w, "  %-12s", name)
	}
	fmt.Fprintln(w)
	for _, b := range r.Bars {
		fmt.Fprintf(w, "  %4d", b.Bar)
		for _, o := range b.Bands {
			cell := "quiet"
			if !o.Quiet {
				cell = fmt.Sprintf("%s %.0f", shortID(o.Owner, 8), o.SharePct)
				if o.Collision {
					cell += "*"
				}
			}
			fmt.Fprintf(w, "  %-12s", cell)
		}
		fmt.Fprintln(w)
	}
	fmt.Fprintf(w, "  collisions per section (bars), bands sub low bass low-mid mid presence air:\n")
	for _, s := range r.Sections {
		var counts []string
		for _, sb := range s.Bands {
			counts = append(counts, fmt.Sprintf("%d", sb.CollisionBars))
		}
		fmt.Fprintf(w, "    %-14s %-7s %s\n", s.Name, fmt.Sprintf("%d-%d", s.FromBar, s.ToBar), strings.Join(counts, " "))
	}
	for k, c := range r.Bands.Collisions {
		if len(c.Bars) == 0 {
			continue
		}
		var pairs []string
		for _, p := range c.Pairs {
			pairs = append(pairs, fmt.Sprintf("%s+%s %d", p.Tracks[0], p.Tracks[1], p.Bars))
		}
		fmt.Fprintf(w, "  %s Hz collisions: %d of %d bars (%s); pairs: %s\n", bandLabels[k], len(c.Bars), len(r.Bars), barRanges(c.Bars), strings.Join(pairs, ", "))
	}
}

func (r *Report) writeActivity(w io.Writer) {
	if r.Activity == nil {
		return
	}
	a := r.Activity
	fmt.Fprintf(w, "\nactivity per bar (█ within 3 dB of the track's loudest bar, one step per 3 dB, · not sounding: under %g dB or silent; a bar with no note start is measured after its first %g s) and repeats (bars whose played notes equal the bar before, =4: four bars before):\n", a.SoundingThresholdDB, a.TailGraceS)
	fmt.Fprintf(w, "  %-12s %5s %5s %5s %4s  bars %d-%d\n", "track", "notes", "=prev", "=4", "run", r.Score.FromBar, r.Score.FromBar+r.Score.Bars-1)
	for _, t := range r.Tracks {
		var row strings.Builder
		for b, v := range t.BarLevelDB {
			if b > 0 && b%4 == 0 {
				row.WriteByte(' ')
			}
			if b < len(t.Sounding) && t.Sounding[b] {
				x := v
				row.WriteRune(levelBlock(&x, 0))
			} else {
				row.WriteRune('·')
			}
		}
		fmt.Fprintf(w, "  %-12s %5d %5d %5d %4d  %s\n", shortID(t.ID, 12), t.NoteBars, t.SameAsPreviousBar, t.SameAsFourBarsBefore, t.LongestSameRun, row.String())
	}
	if len(a.Silences) == 0 {
		fmt.Fprintf(w, "  mix silences (under %g dBFS for at least %g s): none\n", a.SilenceBelowDBFS, a.SilenceMinS)
		return
	}
	var parts []string
	for _, s := range a.Silences {
		parts = append(parts, fmt.Sprintf("%s to %s (%.2f s)", s.Start.Text, s.End.Text, s.LengthS))
	}
	fmt.Fprintf(w, "  mix silences (under %g dBFS for at least %g s): %s\n", a.SilenceBelowDBFS, a.SilenceMinS, strings.Join(parts, "; "))
}

func (r *Report) writePeaks(w io.Writer) {
	if len(r.Peaks) == 0 {
		return
	}
	fmt.Fprintf(w, "\npeaks (true peak of the mix, highest first; tracks: share of the stems' energy in the 5 ms around the peak):\n")
	for _, p := range r.Peaks {
		var parts []string
		for _, t := range p.Tracks {
			parts = append(parts, fmt.Sprintf("%s %.0f%%", t.ID, t.SharePct))
		}
		fmt.Fprintf(w, "  %2d  %6.1f dBTP  at %-8s (%7.2f s)  %s\n", p.Rank, p.TruePeakDBTP, p.At.Text, p.At.TimeS, strings.Join(parts, ", "))
	}
}
