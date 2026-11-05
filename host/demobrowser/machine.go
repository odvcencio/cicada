package demobrowser

import (
	"fmt"

	"m31labs.dev/cicada/edit"
	"m31labs.dev/cicada/patternmachine"
	"m31labs.dev/cicada/project"
)

// MachineIdea is an unsaved candidate and the exact steps shown in its preview.
type MachineIdea struct {
	Source                   []byte
	Name, Track, Scene, Kind string
	Pattern                  patternmachine.Pattern
	Lanes                    []string
}

type machineCompiler struct{}

func (machineCompiler) Compile(source []byte, _ map[string][]byte) (*edit.Plan, error) {
	p, err := Parse(source)
	if err != nil {
		return nil, err
	}
	return project.EditPlan(p, source), nil
}

// GenerateMachine stages one new pattern and an explicit scene assignment in
// memory. It never modifies the demo session or its last-good audio.
func GenerateMachine(source []byte, trackID, sceneID string, recipe patternmachine.Recipe) (*MachineIdea, error) {
	p, err := Parse(source)
	if err != nil {
		return nil, err
	}
	if p.Arrange != nil {
		return nil, fmt.Errorf("Pattern Machine currently needs a scene-based score")
	}
	foundScene := false
	for _, scene := range p.Scenes {
		foundScene = foundScene || scene.ID == sceneID
	}
	if !foundScene {
		return nil, fmt.Errorf("choose a scene for this pattern")
	}
	var track *project.Track
	for i := range p.Tracks {
		if p.Tracks[i].ID == trackID {
			track = &p.Tracks[i]
			break
		}
	}
	if track == nil || track.Kind == "audio" {
		return nil, fmt.Errorf("choose an instrument or drum track")
	}
	kind := track.Kind
	if kind != "acid" && kind != "drums" {
		kind = "notes"
	}
	var lanes []string
	if kind == "drums" {
		lanes = []string{"bd", "sd", "ch", "oh", "rs"}
	}
	for _, kit := range p.Kits {
		if kit.ID == track.Kind {
			kind = "drums"
			lanes = nil
			for lane := range kit.Lanes {
				lanes = append(lanes, lane)
			}
			sortMachineLanes(lanes)
		}
	}
	recipe.Root, recipe.Scale = int(p.Key.Root), p.Key.Scale
	pattern, err := patternmachine.Generate(recipe, kind, lanes)
	if err != nil {
		return nil, err
	}
	occupied := map[string]bool{}
	for _, x := range p.Patterns {
		occupied[x.ID] = true
	}
	for _, x := range p.Clips {
		occupied[x.Name] = true
	}
	name := "machine-idea"
	for i := 2; occupied[name]; i++ {
		name = fmt.Sprintf("machine-idea-%d", i)
	}
	intent := &edit.CreatePattern{Name: name, Track: trackID, Scene: sceneID, Steps: recipe.Bars * 16, Starter: "machine", Machine: &recipe}
	result, err := edit.Apply(source, edit.Envelope{Version: 1, Revision: edit.Revision(source), Intents: []edit.Intent{intent}}, edit.Options{Compiler: machineCompiler{}})
	if err != nil {
		return nil, err
	}
	if _, _, _, err := Prepare(result.Source, 48000); err != nil {
		return nil, err
	}
	return &MachineIdea{Source: result.Source, Name: name, Track: trackID, Scene: sceneID, Kind: kind, Pattern: pattern, Lanes: lanes}, nil
}

func sortMachineLanes(lanes []string) {
	// Stable ordering for named kits and consistent preview/score output.
	for i := 1; i < len(lanes); i++ {
		for j := i; j > 0 && lanes[j] < lanes[j-1]; j-- {
			lanes[j], lanes[j-1] = lanes[j-1], lanes[j]
		}
	}
}
