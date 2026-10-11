package edit

import (
	"bytes"
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"

	"m31labs.dev/cicada/notation"
)

type SetProjectSettings struct {
	Title      string `json:"title"`
	TempoMilli int    `json:"tempomilli"`
	Root       string `json:"root"`
	Scale      string `json:"scale"`
}

func (SetProjectSettings) Kind() string { return "setprojectsettings" }

func init() {
	Register("setprojectsettings", func() Intent { return &SetProjectSettings{} })
	Handle("setprojectsettings", func(ctx *Context, intent Intent) error {
		updated, err := projectSettingsSource(ctx, intent.(*SetProjectSettings))
		if err != nil {
			return err
		}
		ctx.Source, ctx.plan = updated, nil
		ctx.SetLabel("Project title, tempo, and key changed")
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
	for i, root := range sourcePitchNames {
		if settings.Root == root {
			rootIndex = i
		}
	}
	validScale := false
	for _, scale := range []string{"minor", "major", "dorian", "phrygian", "harmonic", "mixo", "pent", "blues"} {
		validScale = validScale || settings.Scale == scale
	}
	if rootIndex < 0 || !validScale {
		return nil, fmt.Errorf("choose a supported key and scale")
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
	for _, kind := range []string{"title_decl", "tempo_decl", "key_decl"} {
		if text, ok := missing[kind]; ok {
			declarations = append(declarations, text)
		}
	}
	if len(declarations) > 0 {
		edits = append(edits, Span{at, at, strings.Join(declarations, newline) + newline, ctx.Options.Path})
	}
	return PatchSpans(source, ctx.Options.Path, edits)
}
