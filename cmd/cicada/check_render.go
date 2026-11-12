package main

import (
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"m31labs.dev/cicada/host/instrumentpack"
	"m31labs.dev/cicada/host/sampleasset"
	"m31labs.dev/cicada/host/schedule"
	"m31labs.dev/cicada/notation"
	"m31labs.dev/cicada/project"
	"m31labs.dev/cicada/render"
)

// checkBeyondValidator runs the checks that cicada check adds to the
// validator (inspectScore), so that a score check accepts is one render
// plays. In order, each stage only when the stages before it found no error:
//
//  1. the sampler packs, which it loads as render does (checkSamplerPacks);
//  2. what samplers and clips cannot play: notes too far from a sampler's
//     root and assets at rates the sample voice cannot read
//     (samplerRangeDiagnostics, assetRateDiagnostics);
//  3. for a score that plays audio files, the engine compile that render
//     runs, without reading audio: the resident PCM budget, clip slots and
//     the engine configuration, which inspectScore compiles only for a score
//     without audio;
//  4. the limits a render of the whole song meets (render.WholeSongLimits).
//
// Diagnostics are in source order. The pack reports are those of stage 1.
func checkBeyondValidator(path string, inspection scoreInspection, limit int64) ([]notation.Diagnostic, []checkPackReport) {
	diagnostics, packs := checkSamplerPacks(path, inspection, limit)
	p, score := inspection.semantic, inspection.score
	if p == nil || score == nil || hasDiagnosticErrors(diagnostics) {
		return diagnostics, packs
	}
	diagnostics = append(diagnostics, samplerRangeDiagnostics(p, score)...)
	diagnostics = append(diagnostics, assetRateDiagnostics(p, score)...)
	if !hasDiagnosticErrors(diagnostics) && p.NeedsSampleEngine() {
		if err := schedule.Validate(p, inspection.root(path), 48_000, 128, schedule.Options{MaxResidentPCM: limit}); err != nil {
			diagnostics = append(diagnostics, engineDiagnostic(err))
		}
	}
	if !hasDiagnosticErrors(diagnostics) {
		diagnostics = append(diagnostics, songLimitDiagnostics(score)...)
	}
	sort.SliceStable(diagnostics, func(i, j int) bool {
		a, b := diagnostics[i].Position, diagnostics[j].Position
		if a.File != b.File {
			return a.File < b.File
		}
		if a.Line != b.Line {
			return a.Line < b.Line
		}
		return a.Column < b.Column
	})
	return diagnostics, packs
}

// engineDiagnostic is an error of the engine compile as a diagnostic: the
// resident PCM budget and the pack cap under their own codes, and every other
// refusal as CICADA-PARAM at the top of the score, as inspectScore reports the
// compile of a score without audio.
func engineDiagnostic(err error) notation.Diagnostic {
	d := notation.Diagnostic{Code: "CICADA-PARAM", Severity: "error", Message: err.Error(), Position: notation.Position{Line: 1, Column: 1}}
	var budget *schedule.ResidentBudgetError
	var memory *instrumentpack.ResidentPCMError
	switch {
	case errors.As(err, &budget):
		d.Code, d.Expected, d.Found = "CICADA-LIMIT", "at most 64.0 MiB of resident PCM", fmt.Sprintf("%.1f MiB", float64(budget.Used+budget.Bytes)/(1<<20))
	case errors.As(err, &memory):
		d.Code = samplerMemoryCode
	}
	return d
}

// assetRateDiagnostics reports each sampler and clip whose asset the sample
// voice cannot read at an output rate (sampleasset.AssetRateFindings), at the
// sampler's or clip's declaration, under CICADA-ASSET-FORMAT. Like a note out
// of a sampler's range, it is an error when the asset cannot play at 48 kHz,
// where render renders by default, and a warning when it fails only at
// another rate.
func assetRateDiagnostics(p *project.Project, score *notation.Score) []notation.Diagnostic {
	findings := sampleasset.AssetRateFindings(p, sampleasset.OutputRates)
	if len(findings) == 0 {
		return nil
	}
	positions := map[string]notation.Position{}
	for _, sampler := range score.Samplers {
		positions["sampler\x00"+sampler.Name] = sampler.Position
	}
	for _, clip := range score.Clips {
		positions["clip\x00"+clip.Name] = clip.Position
	}
	var diagnostics []notation.Diagnostic
	for _, finding := range findings {
		d := notation.Diagnostic{Code: "CICADA-ASSET-FORMAT", Severity: rangeSeverity(finding.Rates),
			Expected: fmt.Sprintf("%d to %d Hz", finding.Low, finding.High), Found: fmt.Sprintf("%d Hz", finding.AssetRate)}
		if finding.Sampler != "" {
			d.Position = positions["sampler\x00"+finding.Sampler]
			d.Message = fmt.Sprintf("sampler %s plays asset %s, recorded at %d Hz; the sample voice reads assets recorded at %d to %d Hz, so the sampler plays no note",
				finding.Sampler, finding.Asset, finding.AssetRate, finding.Low, finding.High)
		} else {
			d.Position = positions["clip\x00"+finding.Clip]
			d.Message = fmt.Sprintf("clip %s plays asset %s, recorded at %d Hz, unpitched, which the sample voice cannot do at %s output: a clip's asset must be recorded at %d to %d Hz",
				finding.Clip, finding.Asset, finding.AssetRate, kilohertz(finding.Rates), finding.Low, finding.High)
		}
		diagnostics = append(diagnostics, d)
	}
	return diagnostics
}

// rangeSeverity is "error" when an output rate that fails is 48 kHz, the
// rate render renders at by default and Studio checks edits at, and
// "warning" when only other rates fail.
func rangeSeverity(rates []int) string {
	for _, rate := range rates {
		if rate == 48_000 {
			return "error"
		}
	}
	return "warning"
}

// kilohertz spells output rates as "44.1 and 96 kHz".
func kilohertz(rates []int) string {
	words := make([]string, len(rates))
	for i, rate := range rates {
		words[i] = strconv.FormatFloat(float64(rate)/1000, 'f', -1, 64)
	}
	switch len(words) {
	case 0:
		return ""
	case 1:
		return words[0] + " kHz"
	}
	return strings.Join(words[:len(words)-1], ", ") + " and " + words[len(words)-1] + " kHz"
}

// songLimitDiagnostics reports the limits that a render of the whole song
// meets (render.WholeSongLimits) under CICADA-LIMIT, at the song block or the
// arrangement. A render of a shorter range still succeeds, so the message
// says how to render the song in parts.
func songLimitDiagnostics(score *notation.Score) []notation.Diagnostic {
	limits := render.WholeSongLimits(score)
	if len(limits) == 0 {
		return nil
	}
	position := score.SongPosition
	if score.Arrange != nil {
		position = score.Arrange.Position
	}
	if position.Line < 1 {
		position = notation.Position{Line: 1, Column: 1}
	}
	var diagnostics []notation.Diagnostic
	for _, limit := range limits {
		d := notation.Diagnostic{Code: "CICADA-LIMIT", Severity: "error", Position: position,
			Expected: fmt.Sprintf("at most %d %s", limit.Max, limit.What), Found: fmt.Sprintf("%d %s", limit.Found, limit.What)}
		switch limit.What {
		case "bars":
			d.Message = fmt.Sprintf("the song is %d bars, and cicada render renders at most %d bars of this score in one run, so a render of the whole song stops before it writes audio; render it in ranges with --from and --bars", limit.Found, limit.Max)
		default:
			d.Message = fmt.Sprintf("the song changes tempo %d times, and the WAV timing chunk records at most %d, so a render of the whole song stops before it writes audio; render it in ranges with --from and --bars that hold at most %d changes each", limit.Found, limit.Max, limit.Max)
		}
		diagnostics = append(diagnostics, d)
	}
	return diagnostics
}
