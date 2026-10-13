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
	if len(r.Images) > 0 {
		fmt.Fprintf(w, "\nimages: %s\n", strings.Join(r.Images, ", "))
	}
}
