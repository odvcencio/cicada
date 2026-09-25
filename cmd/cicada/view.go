package main

import (
	"bytes"
	_ "embed"
	"fmt"
	"html/template"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"m31labs.dev/cicada/language"
	"m31labs.dev/cicada/project"
)

//go:embed view.html
var scoreViewTemplate string

var drumLaneOrder = []string{"bd", "sd", "ch", "oh", "cp", "rs", "lt", "mt", "ht", "cb", "cy"}
var pitchNames = []string{"C", "C♯", "D", "D♯", "E", "F", "F♯", "G", "G♯", "A", "A♯", "B"}

type viewCell struct {
	Index                      int
	Note                       string
	Active, Accent, Slide, Tie bool
	Ratchet, Probability       string
	Velocity, FillPercent      int
	Label                      string
}

type viewLane struct {
	ID    string
	Name  string
	Cells []viewCell
}

type viewPitchCell struct {
	Index      int
	Number     int
	On, Accent bool
	Label      string
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
	Drums                  bool
	Cells                  []viewCell
	Lanes                  []viewLane
	PitchRows              []viewPitchRow
	Numbers                []viewNumber
}

type viewTrack struct {
	ID, Kind, Gain string
	Muted          bool
}

type viewSongEntry struct {
	Scene string
	Bars  uint16
}

type viewScene struct {
	ID string
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
	Song                          []viewSongEntry
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
				card.Cells = append(card.Cells, makeViewCell(index, step, false))
			}
			card.PitchRows = pitchRows(pattern.Data, card.Steps)
		}
		view.Patterns = append(view.Patterns, card)
	}
	for _, entry := range p.Song {
		view.Song = append(view.Song, viewSongEntry{Scene: entry.Scene, Bars: entry.Bars})
	}
	for _, scene := range p.Scenes {
		view.Scenes = append(view.Scenes, viewScene{ID: scene.ID})
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
			lowest = min(lowest, int(step.Note))
			highest = max(highest, int(step.Note))
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
			if index >= len(steps) || steps[index] == nil || steps[index].Tie || int(steps[index].Note) != note {
				continue
			}
			row.Cells[index].On = true
			row.Cells[index].Accent = steps[index].Accent
			row.Cells[index].Label = fmt.Sprintf("Step %d, %s", index+1, row.Name)
		}
		rows = append(rows, row)
	}
	return rows
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
