package main

import (
	"m31labs.dev/gosx/command"
)

// shellCommands is the one command table for the Studio shell. Every later
// milestone flips entries from Reserved to live instead of adding chords.
//
// A Reserved entry consumes its chord and shows as unavailable in the shortcuts
// dialog. Toggle chords (Tab, Shift+Tab, B) come in pairs that differ by their
// When guard, because a command's Value is fixed and the guard reads the signal.
func shellCommands() []command.Command {
	transport := func(action string, extra map[string]any) command.Action {
		value := map[string]any{"op": "transport", "action": action}
		for key, item := range extra {
			value[key] = item
		}
		return command.Action{Signal: "$command", Value: value}
	}
	cmds := []command.Command{
		{ID: "transport.toggle", Title: "Play or pause", Group: "Transport", Keys: []string{"Space"}, Action: transport("toggle", nil)},
		{ID: "transport.resume", Title: "Resume from marker", Group: "Transport", Keys: []string{"Shift+Space"}, Reserved: true},
		{ID: "transport.home", Title: "Return to start", Group: "Transport", Keys: []string{"Home"}, Action: transport("home", nil)},
		{ID: "transport.record", Title: "Record", Group: "Transport", Keys: []string{"F9"}, Action: command.Action{Signal: "$record", Value: "toggle"}},
		{ID: "transport.loop", Title: "Loop selection", Group: "Transport", Keys: []string{"Mod+L"}, Reserved: true},

		{ID: "view.session", Title: "Switch between Arrange and Session", Group: "View", Keys: []string{"Tab"}, When: map[string]any{"$view": "arrange"}, Action: command.Action{Signal: "$view", Value: "session"}},
		{ID: "view.arrange", Title: "Switch between Arrange and Session", Group: "View", Keys: []string{"Tab"}, When: map[string]any{"$view": "session"}, Action: command.Action{Signal: "$view", Value: "arrange"}},
		{ID: "dock.toggle", Title: "Switch between Clip and Device", Group: "View", Keys: []string{"Shift+Tab"}, When: map[string]any{"$dock": "clip"}, Action: command.Action{Signal: "$dock", Value: "device"}},
		{ID: "dock.clip", Title: "Switch between Clip and Device", Group: "View", Keys: []string{"Shift+Tab"}, When: map[string]any{"$dock": "device"}, Action: command.Action{Signal: "$dock", Value: "clip"}},
		{ID: "palette.open", Title: "Show keyboard shortcuts", Group: "View", Keys: []string{"Mod+K", "?"}, Action: command.Action{Open: "#palette"}},

		// TODO(gosx-s0): the edit commands below submit managed forms. They stay
		// Reserved until command.Action.Submit exists; then each becomes
		// Action{Submit: "#undo-form"} and so on, and loses Reserved.
		{ID: "edit.undo", Title: "Undo", Group: "Edit", Keys: []string{"Mod+Z"}, Reserved: true},
		{ID: "edit.redo", Title: "Redo", Group: "Edit", Keys: []string{"Mod+Shift+Z"}, Reserved: true},
		{ID: "edit.duplicate", Title: "Duplicate selection", Group: "Edit", Keys: []string{"Mod+D"}, Reserved: true},
		{ID: "edit.split", Title: "Split clip", Group: "Edit", Keys: []string{"Mod+E"}, Reserved: true},
		{ID: "edit.rename", Title: "Rename selection", Group: "Edit", Keys: []string{"Mod+R"}, Reserved: true},
		{ID: "edit.delete", Title: "Delete selection", Group: "Edit", Keys: []string{"Delete"}, Reserved: true},

		{ID: "tool.draw", Title: "Switch between Select and Draw", Group: "Tools", Keys: []string{"B"}, When: map[string]any{"$tool": "select"}, Action: command.Action{Signal: "$tool", Value: "draw"}},
		{ID: "tool.select", Title: "Switch between Select and Draw", Group: "Tools", Keys: []string{"B"}, When: map[string]any{"$tool": "draw"}, Action: command.Action{Signal: "$tool", Value: "select"}},
		{ID: "zoom.selection", Title: "Zoom to selection", Group: "Tools", Keys: []string{"Z"}, Reserved: true},
		{ID: "zoom.back", Title: "Zoom back", Group: "Tools", Keys: []string{"X"}, Reserved: true},
		{ID: "grid.narrower", Title: "Narrower grid", Group: "Tools", Keys: []string{"Mod+1"}, Reserved: true},
		{ID: "grid.wider", Title: "Wider grid", Group: "Tools", Keys: []string{"Mod+2"}, Reserved: true},
		{ID: "grid.triplet", Title: "Triplet grid", Group: "Tools", Keys: []string{"Mod+3"}, Reserved: true},
		{ID: "grid.snap", Title: "Snap to grid", Group: "Tools", Keys: []string{"Mod+4"}, Reserved: true},
		{ID: "arrange.consolidate", Title: "Consolidate", Group: "Arrange", Keys: []string{"Mod+J"}, Reserved: true},
		{ID: "arrange.group", Title: "Group", Group: "Arrange", Keys: []string{"Mod+G"}, Reserved: true},
		{ID: "notes.quantize", Title: "Quantize notes", Group: "Arrange", Keys: []string{"Mod+U"}, Reserved: true},
	}
	// Arrow keys move the selection only while a grid cell or clip is selected;
	// otherwise they keep scrolling and focus movement. The Clip view (S4) sets
	// $selectionActive.
	for _, arrow := range []struct{ name, key string }{{"left", "ArrowLeft"}, {"right", "ArrowRight"}, {"up", "ArrowUp"}, {"down", "ArrowDown"}} {
		cmds = append(cmds, command.Command{
			ID: "select." + arrow.name, Title: "Move selection " + arrow.name, Group: "Selection", Keys: []string{arrow.key},
			When:   map[string]any{"$selectionActive": true},
			Action: command.Action{Signal: "$selection", Value: map[string]any{"move": arrow.name}},
		})
	}
	// Scene launch is live from S3; until the command relay (S2) the signal has no listener.
	for scene := 1; scene <= 8; scene++ {
		cmds = append(cmds, command.Command{
			ID: "live.scene." + string(rune('0'+scene)), Title: "Launch scene " + string(rune('0'+scene)), Group: "Session", Keys: []string{"F" + string(rune('0'+scene))},
			When:   map[string]any{"$view": "session"},
			Action: command.Action{Signal: "$command", Value: map[string]any{"op": "transport", "action": "launch", "scene": scene - 1}},
		})
	}
	return cmds
}

// shellToggleMirrors names the second command of each toggle pair. The
// shortcuts dialog lists the pair once.
var shellToggleMirrors = map[string]bool{"view.arrange": true, "dock.clip": true, "tool.select": true}
