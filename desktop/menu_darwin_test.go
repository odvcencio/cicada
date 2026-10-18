//go:build darwin

package main

import (
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
