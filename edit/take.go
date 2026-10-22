package edit

import (
	"bytes"
	"errors"
	"fmt"
	"strconv"

	"m31labs.dev/cicada/notation"
)

type TakeAsset struct {
	Name     string `json:"name"`
	Path     string `json:"path"`
	SHA256   string `json:"sha256"`
	Frames   int64  `json:"frames"`
	RateHz   int    `json:"ratehz"`
	Channels int    `json:"channels"`
}

// SelectTake retains assets and clips and updates only the selected binding.
type SelectTake struct {
	ID         string    `json:"id"`
	Track      string    `json:"track"`
	Scene      string    `json:"scene"`
	Rate       int       `json:"rate"`
	Channels   int       `json:"channels"`
	Asset      TakeAsset `json:"asset"`
	StartFrame int64     `json:"startframe"`
}

func (SelectTake) Kind() string { return "selecttake" }
func init() {
	Register("selecttake", func() Intent { return &SelectTake{} })
	Handle("selecttake", func(ctx *Context, intent Intent) error {
		var score *notation.Score
		var diagnostics []notation.Diagnostic
		var err error
		if ctx.Options.ParseProject != nil || len(ctx.Options.Sources) > 0 {
			score, diagnostics, err = ctx.ParseProject()
		} else {
			score, diagnostics = notation.ParseEdition(ctx.Source, 2)
		}
		if err != nil {
			return err
		}
		if err := takeScoreError(score, diagnostics); err != nil {
			return err
		}
		t := *intent.(*SelectTake)
		score, before, path, err := sceneSource(ctx, t.Scene)
		if err != nil {
			return err
		}
		found := false
		for _, track := range score.Tracks {
			found = found || track.Name == t.Track && track.Kind == "audio"
		}
		if !found {
			return errors.New("take target must be an existing audio track")
		}
		// A take's assets stay beside the entry declarations; only the scene
		// assignment belongs in its owning source.
		updated, err := takeDeclarationsSource(ctx.Source, t, score)
		if err != nil {
			return err
		}
		ctx.Source, ctx.plan = updated, nil
		binding, err := sceneBindingSource(before, path, t.Scene, t.Track, t.ID+"-clip")
		if err != nil {
			return err
		}
		if path == ctx.Options.Path {
			// Declarations were appended after taking the scene snapshot.
			binding, err = sceneBindingSource(updated, path, t.Scene, t.Track, t.ID+"-clip")
			if err != nil {
				return err
			}
		}
		ctx.Source = stageProjectSource(ctx, path, before, binding)
		ctx.SetLabel("Select audio take")
		return nil
	})
}

// SelectTakeSource is the pure text half used by the take journal wrapper.
func SelectTakeSource(source []byte, t SelectTake) ([]byte, error) {
	score, ds := notation.ParseEdition(source, 2)
	if err := takeScoreError(score, ds); err != nil {
		return nil, err
	}
	return selectTakeSource(source, "", t, score)
}

// SelectTakeSourceInProject edits source using its validated project snapshot.
// The caller must load score with these exact source bytes and validate the
// resulting project before publishing the edited file.
func SelectTakeSourceInProject(source []byte, t SelectTake, score *notation.Score) ([]byte, error) {
	if err := takeScoreError(score, nil); err != nil {
		return nil, err
	}
	return selectTakeSource(source, "", t, score)
}

func takeScoreError(score *notation.Score, ds []notation.Diagnostic) error {
	if score == nil {
		return errors.New("score cannot be parsed")
	}
	for _, d := range ds {
		if d.Severity == "error" {
			return errors.New(d.Message)
		}
	}
	if score.Version != 2 {
		return errors.New("audio takes require an edition-2 score")
	}
	return nil
}

func selectTakeSource(source []byte, targetFile string, t SelectTake, score *notation.Score) ([]byte, error) {
	score, ds := notation.ResolvePresets(score)
	for _, d := range ds {
		if d.Severity == "error" {
			return nil, errors.New(d.Message)
		}
	}
	found := false
	for _, track := range score.Tracks {
		if track.Name == t.Track && track.Kind == "audio" {
			found = true
		}
	}
	if !found {
		return nil, errors.New("take target must be an existing audio track")
	}
	root, w, err := notation.ParseTree(source)
	if err != nil {
		return nil, err
	}
	var updated []byte
	for i := 0; i < root.NamedChildCount(); i++ {
		node := root.NamedChild(i)
		if w.Type(node) != "scene_decl" || w.Text(w.Field(node, "name")) != t.Scene {
			continue
		}
		for j := 0; j < node.NamedChildCount(); j++ {
			binding := node.NamedChild(j)
			if w.Type(binding) != "scene_assignment" || w.Text(w.Field(binding, "target")) != t.Track || notation.SceneAssignmentIsSetting(t.Track, w.Text(w.Field(binding, "value"))) {
				continue
			}
			value := w.Field(binding, "value")
			if value == nil {
				return nil, errors.New("scene binding is missing value")
			}
			updated, err = ReplaceSpan(source, targetFile, Span{int(value.StartByte()), int(value.EndByte()), t.ID + "-clip", targetFile})
			if err != nil {
				return nil, err
			}
			break
		}
		if updated == nil {
			at := int(node.EndByte()) - 1
			updated, err = ReplaceSpan(source, targetFile, Span{at, at, "\n  " + t.Track + " = " + t.ID + "-clip\n", targetFile})
			if err != nil {
				return nil, err
			}
		}
		break
	}
	if updated == nil {
		return nil, errors.New("take scene no longer exists")
	}
	return takeDeclarationsSource(updated, t, score)
}

func takeDeclarationsSource(source []byte, t SelectTake, score *notation.Score) ([]byte, error) {
	hasAsset, hasClip := false, false
	for _, a := range score.Assets {
		if a.Name == t.ID {
			if a.SHA256 != t.Asset.SHA256 || a.Path != t.Asset.Path || a.Frames != t.Asset.Frames || a.RateHz != t.Rate || a.Channels != t.Channels {
				return nil, errors.New("take asset name collision")
			}
			hasAsset = true
		}
	}
	for _, c := range score.Clips {
		if c.Name == t.ID+"-clip" {
			if c.Asset != t.ID {
				return nil, errors.New("take clip name collision")
			}
			hasClip = true
		}
	}
	var text bytes.Buffer
	if !hasAsset {
		a := t.Asset
		fmt.Fprintf(&text, "\nasset %s %s {\n  sha256 = %s\n  format = wav\n  frames = %d\n  rate = %dHz\n  channels = %d\n  source = recorded\n}\n", a.Name, strconv.Quote(a.Path), strconv.Quote(a.SHA256), a.Frames, a.RateHz, a.Channels)
	}
	if !hasClip {
		start := t.StartFrame
		if start >= t.Asset.Frames {
			return nil, errors.New("take contains only preroll; retained for recovery")
		}
		fmt.Fprintf(&text, "\nclip %s-clip %s {\n  start = %dframes\n  end = %dframes\n}\n", t.ID, t.ID, start, t.Asset.Frames)
	}
	return append(bytes.Clone(source), text.Bytes()...), nil
}
