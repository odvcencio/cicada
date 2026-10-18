//go:build darwin

package main

import "m31labs.dev/gosx/desktop"

func platformMenu(h *host, menu desktop.Menu) desktop.Menu {
	shortcuts := map[string]string{
		"file.new": "Cmd+N", "file.open": "Cmd+O", "file.saveas": "Cmd+Shift+S",
		"file.exit": "Cmd+W", "view.reload": "Cmd+R",
	}
	for _, top := range menu.Items {
		if top.Submenu == nil {
			continue
		}
		for i := range top.Submenu.Items {
			item := &top.Submenu.Items[i]
			item.Accelerator = shortcuts[item.ID]
			if item.ID == "file.exit" {
				item.Label = "Close Window"
			}
		}
	}
	edit := desktop.MenuItem{ID: "edit", Label: "Edit", Submenu: &desktop.Menu{Items: []desktop.MenuItem{
		// The page command registry owns Cmd-Z/Shift-Z. Menu clicks choose
		// document history or typing history based on the focused editor.
		{ID: "edit.undo", Label: "Undo", OnClick: func() { h.editHistory("undo") }},
		{ID: "edit.redo", Label: "Redo", OnClick: func() { h.editHistory("redo") }},
		{Separator: true},
		{ID: "edit.cut", Label: "Cut", Role: desktop.MenuRoleCut, Accelerator: "Cmd+X"},
		{ID: "edit.copy", Label: "Copy", Role: desktop.MenuRoleCopy, Accelerator: "Cmd+C"},
		{ID: "edit.paste", Label: "Paste", Role: desktop.MenuRolePaste, Accelerator: "Cmd+V"},
		{ID: "edit.selectall", Label: "Select All", Role: desktop.MenuRoleSelectAll, Accelerator: "Cmd+A"},
	}}}
	items := make([]desktop.MenuItem, 0, len(menu.Items)+1)
	for i, item := range menu.Items {
		items = append(items, item)
		if i == 0 {
			items = append(items, edit)
		}
	}
	menu.Items = items
	return menu
}

func (h *host) editHistory(action string) {
	// action is a fixed menu constant, never page or file input.
	script := `(function(){var e=document.activeElement;var typing=e&&(e.matches('input,textarea,select')||e.isContentEditable||e.closest('[contenteditable]:not([contenteditable=false]),[role=textbox]'));if(typing){document.execCommand('` + action + `');}else if(window.__gosx&&window.__gosx.commands){window.__gosx.commands.run('edit.` + action + `');}})();`
	if err := h.app.ExecuteScript(script); err != nil {
		showError("Edit failed", err.Error())
	}
}
