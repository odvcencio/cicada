//go:build darwin

package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"m31labs.dev/gosx/desktop"
)

func TestMacMenusKeepHistoryShortcutsInWorkstation(t *testing.T) {
	h := &host{}
	menu := h.menu()
	if _, err := desktop.BuildMenuPlan(menu); err != nil {
		t.Fatal(err)
	}
	items := map[string]desktop.MenuItem{}
	for _, top := range menu.Items {
		if top.Submenu != nil {
			for _, item := range top.Submenu.Items {
				items[item.ID] = item
			}
		}
	}
	for _, id := range []string{"edit.undo", "edit.redo"} {
		if item := items[id]; item.OnClick == nil || item.Role != "" || item.Accelerator != "" {
			t.Errorf("%s intercepts native document shortcut: %+v", id, item)
		}
	}
	for id, chord := range map[string]string{
		"file.new": "Cmd+N", "file.open": "Cmd+O", "file.saveas": "Cmd+Shift+S",
		"file.exit": "Cmd+W", "view.reload": "Cmd+R", "edit.copy": "Cmd+C",
	} {
		if got := items[id].Accelerator; got != chord {
			t.Errorf("%s shortcut = %q, want %q", id, got, chord)
		}
	}
	if items["edit.copy"].Role != desktop.MenuRoleCopy {
		t.Error("Copy does not use the focused native editor")
	}
}

// TestMacEditorMenuWorkflow supplies the exact production menu scripts to an
// external native smoke workflow without adding a test API to the app.
func TestMacEditorMenuWorkflow(t *testing.T) {
	output := os.Getenv("CICADA_TEST_EDITOR_MENU_WORKFLOW")
	if output == "" {
		t.Skip("set CICADA_TEST_EDITOR_MENU_WORKFLOW to generate the native editor smoke workflow")
	}
	workflow, err := os.ReadFile(filepath.Join("testdata", "native-editor-workflow.js"))
	if err != nil {
		t.Fatal(err)
	}
	scripts, err := json.Marshal(map[string]string{"undo": editHistoryScript("undo"), "redo": editHistoryScript("redo")})
	if err != nil {
		t.Fatal(err)
	}
	combined := append([]byte("window.__cicadaNativeEditorMenuScripts = "+string(scripts)+";\n"), workflow...)
	if err := os.WriteFile(output, combined, 0600); err != nil {
		t.Fatal(err)
	}
}
