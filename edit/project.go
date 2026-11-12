package edit

import (
	"bytes"
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"

	"m31labs.dev/cicada/notation"
)

// SetProjectSettings sets the score's title, tempo, key root and scale
// together, and its meter when Meter is given. A meter change rewrites the
// declaration only; it is refused when a position no longer fits its bar.
//
// Example: {"kind":"setprojectsettings","title":"Intent catalog","tempomilli":124000,"root":"a","scale":"minor"}
// Example: {"kind":"setprojectsettings","title":"Waltz","tempomilli":132000,"root":"a","scale":"minor","meter":"3/4"}
type SetProjectSettings struct {
	Title      string `json:"title" cicada:"required"`                       // 1–256 characters
	TempoMilli int    `json:"tempomilli" unit:"0.001 BPM" cicada:"required"` // tempo in thousandths of a BPM, 20000–300000
	Root       string `json:"root" values:"root" cicada:"required"`          // key root
	Scale      string `json:"scale" values:"scale" cicada:"required"`        // scale
	Meter      string `json:"meter,omitempty"`                               // N/D with optional beat groups, such as 3/4 or 7/8 2+2+3; empty keeps the meter
}

func (SetProjectSettings) Kind() string { return "setprojectsettings" }

// projectRoots and projectScales are the key roots and scales
// SetProjectSettings accepts.
var (
	projectRoots  = valueList("root", sourcePitchNames[:]...)
	projectScales = valueList("scale", "minor", "major", "dorian", "phrygian", "harmonic", "mixo", "pent", "blues")
)

func init() {
	Register("setprojectsettings", func() Intent { return &SetProjectSettings{} })
	Handle("setprojectsettings", func(ctx *Context, intent Intent) error {
		updated, err := projectSettingsSource(ctx, intent.(*SetProjectSettings))
		if err != nil {
			return err
		}
		ctx.Source, ctx.plan = updated, nil
		label := "Project title, tempo, and key changed"
		if intent.(*SetProjectSettings).Meter != "" {
			label = "Project title, tempo, key, and meter changed"
		}
		ctx.SetLabel(label)
		return nil
	})
}
func projectSettingsSource(ctx *Context, settings *SetProjectSettings) ([]byte, error) {
	source := ctx.Source
	if ctx.Options.ParseProject != nil {
		score, ds, err := ctx.ParseProject()
		if err != nil {
			return nil, err
		}
		if score == nil || hasErrors(ds) {
			return nil, fmt.Errorf("score must validate before editing")
		}
	}
	if settings == nil || strings.TrimSpace(settings.Title) == "" || utf8.RuneCountInString(settings.Title) > 256 || settings.TempoMilli < 20000 || settings.TempoMilli > 300000 {
		return nil, fmt.Errorf("choose a title of 1–256 characters and tempo 20–300 BPM")
	}
	rootIndex := -1
	for i, root := range projectRoots {
		if settings.Root == root {
			rootIndex = i
		}
	}
	validScale := false
	for _, scale := range projectScales {
		validScale = validScale || settings.Scale == scale
	}
	if rootIndex < 0 || !validScale {
		return nil, fmt.Errorf("choose a supported key and scale")
	}
	var meter notation.Meter
	if settings.Meter != "" {
		fraction, groups, _ := strings.Cut(strings.TrimSpace(settings.Meter), " ")
		var code, message string
		if meter, code, message = notation.ParseMeter(fraction, strings.TrimSpace(groups)); code != "" {
			return nil, &ReasonError{Reason: "invalid_meter", Field: "meter", Message: message}
		}
	}
	score, ds := ctx.Parse()
	if score == nil || hasErrors(ds) {
		return nil, fmt.Errorf("score must validate before changing its settings")
	}
	semantic, err := ctx.CurrentPlan()
	if err != nil {
		return nil, fmt.Errorf("score must compile before changing its settings")
	}
	root, walker, err := notation.ParseTree(source)
	if err != nil {
		return nil, err
	}
	missing := map[string]string{
		"title_decl": "title " + strconv.Quote(settings.Title),
		"tempo_decl": "tempo " + strconv.FormatFloat(float64(settings.TempoMilli)/1000, 'f', -1, 64),
		"key_decl":   "key " + settings.Root + " " + settings.Scale,
	}
	if settings.Meter != "" {
		missing["meter_decl"] = "meter " + meter.String()
	}
	var edits []Span
	at := len(source)
	for i := 0; i < root.NamedChildCount(); i++ {
		node := root.NamedChild(i)
		kind := walker.Type(node)
		if kind != "integer" && kind != "comment" && at == len(source) {
			at = int(node.StartByte())
		}
		if _, ok := missing[kind]; !ok {
			continue
		}
		delete(missing, kind)
		switch kind {
		case "title_decl":
			if settings.Title != score.Title {
				for j := 0; j < node.NamedChildCount(); j++ {
					if value := node.NamedChild(j); walker.Type(value) == "string" {
						edits = append(edits, Span{int(value.StartByte()), int(value.EndByte()), strconv.Quote(settings.Title), ctx.Options.Path})
					}
				}
			}
		case "tempo_decl":
			if int64(settings.TempoMilli) != score.TempoMilli {
				for j := 0; j < node.NamedChildCount(); j++ {
					if value := node.NamedChild(j); walker.Type(value) == "number" {
						edits = append(edits, Span{int(value.StartByte()), int(value.EndByte()), strconv.FormatFloat(float64(settings.TempoMilli)/1000, 'f', -1, 64), ctx.Options.Path})
					}
				}
			}
		case "meter_decl":
			if score.Meter == nil || score.Meter.String() != meter.String() {
				edits = append(edits, Span{int(node.StartByte()), int(node.EndByte()), "meter " + meter.String(), ctx.Options.Path})
			}
		case "key_decl":
			if uint8(rootIndex) != semantic.KeyRoot {
				value := walker.Field(node, "root")
				edits = append(edits, Span{int(value.StartByte()), int(value.EndByte()), settings.Root, ctx.Options.Path})
			}
			if settings.Scale != score.Scale {
				value := walker.Field(node, "scale")
				edits = append(edits, Span{int(value.StartByte()), int(value.EndByte()), settings.Scale, ctx.Options.Path})
			}
		}
	}
	newline := "\n"
	if bytes.Contains(source, []byte("\r\n")) {
		newline = "\r\n"
	}
	var declarations []string
	for _, kind := range []string{"title_decl", "tempo_decl", "key_decl", "meter_decl"} {
		if text, ok := missing[kind]; ok {
			declarations = append(declarations, text)
		}
	}
	if len(declarations) > 0 {
		edits = append(edits, Span{at, at, strings.Join(declarations, newline) + newline, ctx.Options.Path})
	}
	return PatchSpans(source, ctx.Options.Path, edits)
}
