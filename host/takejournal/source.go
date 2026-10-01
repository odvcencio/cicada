package takejournal

import (
	"bytes"
	"errors"
	"fmt"
	"strconv"

	"m31labs.dev/cicada/notation"
)

// SelectSource retains every asset and clip declaration and changes only the
// selected scene binding. Timing before frame zero becomes clip preroll trim;
// full raw PCM and placement metadata remain in the journal and asset.
func SelectSource(source []byte, t Take) ([]byte, error) {
	score, ds := notation.ParseEdition(source, 2)
	if score == nil {
		return nil, errors.New("score cannot be parsed")
	}
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
			if w.Type(binding) != "scene_assignment" || w.Text(w.Field(binding, "target")) != t.Track {
				continue
			}
			value := w.Field(binding, "value")
			if value == nil {
				return nil, errors.New("scene binding is missing value")
			}
			updated = replace(source, int(value.StartByte()), int(value.EndByte()), []byte(t.ID+"-clip"))
			break
		}
		if updated == nil {
			at := int(node.EndByte()) - 1
			updated = replace(source, at, at, []byte("\n  "+t.Track+" = "+t.ID+"-clip\n"))
		}
		break
	}
	if updated == nil {
		return nil, errors.New("take scene no longer exists")
	}
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
		start := int64(0)
		if t.FirstBlock != nil && t.FirstBlock.Placement.EngineFrame < 0 {
			start = int64(t.FirstBlock.RawFrame) - t.FirstBlock.Placement.EngineFrame
		}
		if start >= t.Asset.Frames {
			return nil, errors.New("take contains only preroll; retained for recovery")
		}
		fmt.Fprintf(&text, "\nclip %s-clip %s {\n  start = %dframes\n  end = %dframes\n}\n", t.ID, t.ID, start, t.Asset.Frames)
	}
	return append(updated, text.Bytes()...), nil
}
func replace(source []byte, start, end int, text []byte) []byte {
	result := make([]byte, 0, len(source)+len(text))
	result = append(result, source[:start]...)
	result = append(result, text...)
	return append(result, source[end:]...)
}
