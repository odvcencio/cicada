package main

import (
	"bytes"
	_ "embed"
	"fmt"
	"html/template"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"m31labs.dev/cicada/language"
	"m31labs.dev/cicada/project"
)

//go:embed view.html
var scoreViewTemplate string

//go:embed studio-audio.js
var studioAudioScript []byte

var drumLaneOrder = []string{"bd", "sd", "ch", "oh", "cp", "rs", "lt", "mt", "ht", "cb", "cy"}
var pitchNames = []string{"C", "C♯", "D", "D♯", "E", "F", "F♯", "G", "G♯", "A", "A♯", "B"}

type viewCell struct {
	Index                             int
	Note                              string
	Active, Accent, Slide, Tie, Chord bool
	Ratchet, Probability              string
	Velocity, FillPercent             int
	Label                             string
}

type viewLane struct {
	ID    string
	Name  string
	Cells []viewCell
}

type viewPitchCell struct {
	Index             int
	Number            int
	On, Accent, Chord bool
	Label             string
}

type viewPitchRow struct {
	Name  string
	Note  int
	Cells []viewPitchCell
}

type viewNumber struct {
	Value    int
	Downbeat bool
}

type viewPattern struct {
	ID, Kind               string
	Steps                  int
	Swing, Gate, Transpose string
	Drums, HasChords       bool
	Cells                  []viewCell
	Lanes                  []viewLane
	PitchRows              []viewPitchRow
	Numbers                []viewNumber
}

type viewTrack struct {
	ID, Kind, Gain string
	Muted          bool
}

type viewMixerParam struct{ Name, Value string }
type viewMasterEffect struct {
	ID, Kind string
	Params   []viewMixerParam
}
type viewExportTarget struct{ ID, Loudness, TruePeak, Normalize string }

type viewSongEntry struct {
	Scene    string
	Bars     uint16
	Index    int
	StartBar int
	EndBar   int
}

type viewScene struct {
	ID    string
	Index int
}

type viewSceneCell struct {
	Scene, Track, Pattern, Label string
	Launchable                   bool
	TrackIndex, SlotIndex        int
}

type viewSceneRow struct {
	Track string
	Cells []viewSceneCell
}

type scoreView struct {
	Title, Tempo, Key, SourceName string
	SourceHTML                    template.HTML
	SourceText                    string
	Revision                      string
	Studio                        bool
	LineNumbers                   []int
	Tracks                        []viewTrack
	Voices                        []viewVoice
	Patterns                      []viewPattern
	Scenes                        []viewScene
	SceneRows                     []viewSceneRow
	Song                          []viewSongEntry
	MasterChain                   []viewMasterEffect
	ExportTargets                 []viewExportTarget
	MasterCompressor              []viewMixerParam
	HasMasterCompressor           bool
}

func viewCommand(args []string) error {
	if len(args) != 3 || args[1] != "-o" || !strings.HasSuffix(args[2], ".html") || args[0] == args[2] {
		return fmt.Errorf("usage: cicada view <score.cicada|project.json> -o <view.html>")
	}
	p, err := loadProject(args[0])
	if err != nil {
		return err
	}
	var source []byte
	if strings.HasSuffix(args[0], ".cicada") {
		source, err = os.ReadFile(args[0])
	} else {
		source, err = project.ToSource(p)
	}
	if err != nil {
		return err
	}
	var output bytes.Buffer
	if err := writeScoreView(&output, p, string(source), filepath.Base(args[0])); err != nil {
		return err
	}
	if err := writeNewAtomic(args[2], output.Bytes()); err != nil {
		return err
	}
	fmt.Println(args[2])
	return nil
}

func writeScoreView(w io.Writer, p *project.Project, source, sourceName string) error {
	return writeScorePage(w, p, source, sourceName, false, "")
}

func writeScorePage(w io.Writer, p *project.Project, source, sourceName string, studio bool, revision string) error {
	if p == nil {
		return fmt.Errorf("nil project")
	}
	view := scoreView{
		Title: p.Title, Tempo: strconv.FormatFloat(float64(p.TempoMilli)/1000, 'f', -1, 64),
		Key: pitchNames[p.Key.Root] + " " + p.Key.Scale, SourceName: sourceName,
		SourceText: source, Revision: revision, Studio: studio,
		Tracks: make([]viewTrack, 0, len(p.Tracks)), Patterns: make([]viewPattern, 0, len(p.Patterns)),
		Scenes: make([]viewScene, 0, len(p.Scenes)), Song: make([]viewSongEntry, 0, len(p.Song)),
		MasterCompressor: make([]viewMixerParam, 0),
	}
	if p.Master != nil {
		for _, name := range p.Master.Mixer.Inserts {
			for _, effect := range p.Effects {
				if effect.ID == name {
					stage := viewMasterEffect{ID: name, Kind: effect.Kind}
					for _, key := range sortedMasterKeys(effect.Params) {
						stage.Params = append(stage.Params, viewMixerParam{Name: key, Value: masterDisplayValue(effect.Params[key])})
					}
					view.MasterChain = append(view.MasterChain, stage)
				}
			}
		}
	}
	for _, target := range p.Exports {
		item := viewExportTarget{ID: target.ID, Normalize: "Renderer default"}
		if target.Loudness != nil {
			item.Loudness = masterDisplayValue(*target.Loudness)
		}
		if target.TruePeak != nil {
			item.TruePeak = masterDisplayValue(*target.TruePeak)
		}
		if target.Normalize != nil {
			if *target.Normalize {
				item.Normalize = "On"
			} else {
				item.Normalize = "Off"
			}
		}
		view.ExportTargets = append(view.ExportTargets, item)
	}
	for _, effect := range p.Effects {
		if effect.Kind != "comp" && effect.ID != "comp" || project.MasterHasInsert(p, effect.ID) {
			continue
		}
		view.HasMasterCompressor = true
		keys := make([]string, 0, len(effect.Params))
		for key := range effect.Params {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			value := effect.Params[key]
			formatted := value.Text
			if value.Number != nil {
				formatted = strconv.FormatFloat(*value.Number, 'f', -1, 64)
				switch value.Unit {
				case "db":
					formatted += " dB"
				case "ms":
					formatted += " ms"
				case "ratio", "unit":
				case "s":
					formatted += " s"
				}
			}
			view.MasterCompressor = append(view.MasterCompressor, viewMixerParam{Name: key, Value: formatted})
		}
	}
	spans, err := language.Highlight([]byte(source))
	if err != nil {
		return err
	}
	var highlighted bytes.Buffer
	if err := language.WriteHTMLFragment(&highlighted, []byte(source), spans, language.Night); err != nil {
		return err
	}
	// WriteHTMLFragment escapes source bytes and emits only theme-defined CSS.
	view.SourceHTML = template.HTML(highlighted.String())
	lineCount := strings.Count(source, "\n") + 1
	if strings.HasSuffix(source, "\n") {
		lineCount--
	}
	for line := 1; line <= lineCount; line++ {
		view.LineNumbers = append(view.LineNumbers, line)
	}
	for _, track := range p.Tracks {
		view.Tracks = append(view.Tracks, viewTrack{ID: track.ID, Kind: track.Kind, Gain: strconv.FormatFloat(track.Mixer.GainDB, 'f', -1, 64) + " dB", Muted: track.Mixer.Mute})
	}
	view.Voices = scoreVoices(p)
	for _, pattern := range p.Patterns {
		card := viewPattern{
			ID: pattern.ID, Kind: pattern.Kind, Steps: int(pattern.Steps), Drums: pattern.Kind == "drums",
			Swing: strconv.FormatFloat(float64(pattern.SwingPercent100)/100, 'f', -1, 64) + "%",
			Gate:  strconv.Itoa(int(pattern.GatePercent)) + "%", Transpose: strconv.Itoa(int(pattern.Transpose)) + " st",
		}
		for index := 0; index < card.Steps; index++ {
			card.Numbers = append(card.Numbers, viewNumber{Value: index + 1, Downbeat: index%4 == 0})
		}
		if card.Drums {
			for _, name := range drumLaneOrder {
				steps, ok := pattern.Lanes[name]
				if !ok {
					continue
				}
				lane := viewLane{ID: name, Name: strings.ToUpper(name), Cells: make([]viewCell, 0, card.Steps)}
				for index := 0; index < card.Steps; index++ {
					var step *project.Step
					if index < len(steps) {
						step = steps[index]
					}
					lane.Cells = append(lane.Cells, makeViewCell(index, step, true))
				}
				card.Lanes = append(card.Lanes, lane)
			}
		} else {
			card.Cells = make([]viewCell, 0, card.Steps)
			for index := 0; index < card.Steps; index++ {
				var step *project.Step
				if index < len(pattern.Data) {
					step = pattern.Data[index]
				}
				cell := makeViewCell(index, step, false)
				card.HasChords = card.HasChords || cell.Chord
				card.Cells = append(card.Cells, cell)
			}
			card.PitchRows = pitchRows(pattern.Data, card.Steps)
		}
		view.Patterns = append(view.Patterns, card)
	}
	bar := 1
	for index, entry := range p.Song {
		end := bar + int(entry.Bars) - 1
		view.Song = append(view.Song, viewSongEntry{Scene: entry.Scene, Bars: entry.Bars, Index: index, StartBar: bar, EndBar: end})
		bar = end + 1
	}
	for sceneIndex, scene := range p.Scenes {
		view.Scenes = append(view.Scenes, viewScene{ID: scene.ID, Index: sceneIndex})
	}
	for trackIndex, track := range p.Tracks {
		row := viewSceneRow{Track: track.ID, Cells: make([]viewSceneCell, 0, len(p.Scenes))}
		for _, scene := range p.Scenes {
			binding := scene.Bindings[track.ID]
			if binding == "" {
				binding = "keep"
			}
			label := binding
			if label == "off" {
				label = "stop"
			}
			slotIndex := -1
			for index, pattern := range track.Slots {
				if pattern != nil && *pattern == binding {
					slotIndex = index
					break
				}
			}
			row.Cells = append(row.Cells, viewSceneCell{Scene: scene.ID, Track: track.ID, Pattern: binding, Label: label, Launchable: binding != "keep" && binding != "off", TrackIndex: trackIndex, SlotIndex: slotIndex})
		}
		view.SceneRows = append(view.SceneRows, row)
	}
	page, err := template.New("view").Parse(scoreViewTemplate)
	if err != nil {
		return err
	}
	return page.Execute(w, view)
}

func pitchRows(steps []*project.Step, count int) []viewPitchRow {
	lowest, highest := 127, 0
	for _, step := range steps {
		if step != nil && !step.Tie {
			for _, note := range viewStepPitches(step) {
				lowest = min(lowest, note)
				highest = max(highest, note)
			}
		}
	}
	if lowest > highest {
		lowest, highest = 60, 60
	}
	lowest, highest = max(0, lowest-2), min(127, highest+2)
	rows := make([]viewPitchRow, 0, highest-lowest+1)
	for note := highest; note >= lowest; note-- {
		row := viewPitchRow{Name: midiNote(uint8(note)), Note: note, Cells: make([]viewPitchCell, count)}
		for index := 0; index < count; index++ {
			row.Cells[index].Index = index
			row.Cells[index].Number = index + 1
			if index >= len(steps) || steps[index] == nil || steps[index].Tie {
				continue
			}
			row.Cells[index].Chord = len(steps[index].Notes) > 0
			for _, pitch := range viewStepPitches(steps[index]) {
				if pitch == note {
					row.Cells[index].On = true
					row.Cells[index].Accent = steps[index].Accent
					row.Cells[index].Label = fmt.Sprintf("Step %d, %s", index+1, row.Name)
					break
				}
			}
		}
		rows = append(rows, row)
	}
	return rows
}

func viewStepPitches(step *project.Step) []int {
	if len(step.Notes) > 0 {
		return step.Notes
	}
	return []int{int(step.Note)}
}

func makeViewCell(index int, step *project.Step, drum bool) viewCell {
	cell := viewCell{Index: index, Note: "·", Probability: "—", Ratchet: "—", Label: fmt.Sprintf("Step %d, rest", index+1)}
	if step == nil {
		return cell
	}
	cell.Active, cell.Accent, cell.Slide, cell.Tie = true, step.Accent, step.Slide, step.Tie
	cell.Velocity = int(step.Velocity)
	cell.FillPercent = int(step.Velocity) * 100 / 127
	cell.Probability = strconv.Itoa(int(step.Probability)) + "%"
	if step.Ratchet > 1 {
		cell.Ratchet = "×" + strconv.Itoa(int(step.Ratchet))
	}
	if drum {
		cell.Note = "●"
		cell.Label = fmt.Sprintf("Step %d, drum hit, velocity %d, probability %d percent", index+1, step.Velocity, step.Probability)
	} else if step.Tie {
		cell.Note = "TIE"
		cell.Label = fmt.Sprintf("Step %d, tie", index+1)
	} else if len(step.Notes) > 0 {
		cell.Chord = true
		names := make([]string, len(step.Notes))
		for i, note := range step.Notes {
			names[i] = midiNote(uint8(note))
		}
		cell.Note = strings.Join(names, " ")
		cell.Label = fmt.Sprintf("Step %d, chord %s, velocity %d, probability %d percent", index+1, strings.Join(names, ", "), step.Velocity, step.Probability)
	} else {
		cell.Note = midiNote(step.Note)
		cell.Label = fmt.Sprintf("Step %d, note %s, velocity %d, probability %d percent", index+1, cell.Note, step.Velocity, step.Probability)
	}
	if step.Accent {
		cell.Label += ", accent"
	}
	if step.Slide {
		cell.Label += ", slide"
	}
	if step.Ratchet > 1 {
		cell.Label += fmt.Sprintf(", ratchet %d", step.Ratchet)
	}
	return cell
}

func midiNote(note uint8) string {
	return pitchNames[int(note)%12] + strconv.Itoa(int(note)/12-1)
}

func sortedMasterKeys(values map[string]project.Value) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
func masterDisplayValue(value project.Value) string {
	if value.Number == nil {
		return value.Text
	}
	number := strconv.FormatFloat(*value.Number, 'f', -1, 64)
	units := map[string]string{"db": "dB", "dbtp": "dBTP", "hz": "Hz", "ms": "ms", "frames": "frames", "lufs": "LUFS", "lu": "LU"}
	if unit := units[value.Unit]; unit != "" {
		return number + " " + unit
	}
	return number
}
