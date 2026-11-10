//go:build js && wasm

package main

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"syscall/js"
	"time"

	"m31labs.dev/gosx/signal"
)

// The /studio shell's edit queue. It reuses the panel workspace's queue, run
// loop and tree diff, with three differences: it captures only piano-roll
// cells and the Undo and Redo forms, it paints them pending at once, and it
// patches the update regions an edit response carries instead of the whole
// page. Every other form keeps GoSX's soft navigation, which refreshes every
// region, so the page stays correct while the queue grows to more forms.

const shellIdleRefresh = 250 * time.Millisecond

func bindShellEdits(ctx context.Context, root js.Value, props workspaceProps) func() {
	patch := js.Undefined()
	if gosx := js.Global().Get("__gosx"); workspacePresent(gosx) && workspacePresent(gosx.Get("host")) && workspacePresent(gosx.Get("host").Get("patch")) {
		patch = gosx.Get("host").Get("patch").Get("applyJSON")
	}
	if patch.Type() != js.TypeFunction || !workspacePresent(domCall(root, "querySelector", "[data-update-region]")) {
		// Without the patch receiver every form keeps GoSX's soft navigation.
		return func() {}
	}
	ctx, cancel := context.WithCancel(ctx)
	u := &workspaceUI{root: root, patch: patch, props: props, ctx: ctx, cancel: cancel, jobs: make(chan workspaceJob, 128), revision: signal.New(props.Revision), sourceRevision: props.SourceRevision, status: signal.New(""), drafts: map[string]workspaceDraft{}, editors: map[string]workspaceEditorBaseline{}, shell: true}
	u.seedEditorBaseline()
	watch := signal.Watch(func() {
		if node := domCall(root, "querySelector", "[data-workspace-status]"); workspacePresent(node) {
			node.Set("textContent", u.status.Get())
		}
		if node := domCall(root, "querySelector", "[data-workspace-retry]"); workspacePresent(node) {
			node.Set("hidden", !u.blocked.Load())
		}
	})
	document := js.Global().Get("document")
	u.listen(document, "submit", u.submit)
	u.listen(document, "click", func(event js.Value) {
		if target := event.Get("target"); event.Get("button").Int() == 0 && u.owned(target) && workspacePresent(domCall(target, "closest", "[data-workspace-retry]")) {
			workspaceStop(event)
			// "Refresh saved state": read every region again, without digests,
			// keeping unsaved drafts, then let edits run.
			u.clearQueue()
			u.restoreAll()
			u.status.Set("Refreshing saved state…")
			u.jobs <- workspaceJob{method: http.MethodGet, form: js.Undefined(), refresh: true, regions: u.shellRegions(workspaceJob{}, regionsAll)}
		}
	})
	// A stale region refreshes when its tab, pane, section or dialog opens.
	reveal := js.FuncOf(func(js.Value, []js.Value) any {
		if !u.blocked.Load() && !u.closed.Load() {
			if !u.busy.Load() {
				u.reloadIfNeeded()
			}
			u.refreshStale(regionsVisibleStale)
		}
		return nil
	})
	// Watch the tab panels, sections, panes and dialogs that hold regions, not
	// their contents: engines inside regions change these attributes too.
	observer := js.Global().Get("MutationObserver").New(reveal)
	options := map[string]any{"attributes": true, "attributeFilter": []any{"hidden", "open", "data-phone-region"}}
	wrappers := domCall(root, "querySelectorAll", "[data-update-region]")
	var watched []js.Value
	for i := 0; i < wrappers.Length(); i++ {
	ancestors:
		for node := wrappers.Index(i).Get("parentElement"); workspacePresent(node) && u.owned(node); node = node.Get("parentElement") {
			for _, seen := range watched {
				if seen.Equal(node) {
					continue ancestors
				}
			}
			watched = append(watched, node)
			observer.Call("observe", node, options)
		}
	}
	domSet(root, "data-shell-edits", "ready")
	go u.run()
	return func() {
		if u.closed.Swap(true) {
			return
		}
		u.cancel()
		if u.idle != nil {
			u.idle.Stop()
		}
		observer.Call("disconnect")
		reveal.Release()
		for _, event := range u.events {
			event.target.Call("removeEventListener", event.name, event.callback, true)
			event.callback.Release()
		}
		watch.Dispose()
		u.clearQueue()
		u.restoreAll()
		domDrop(root, "data-shell-edits")
	}
}

// shellCaptures reports whether the queue owns a submission: a piano-roll
// cell, Undo or Redo, inside an update region and without a submitter that
// redirects it.
func shellCaptures(form, submitter js.Value) bool {
	if !workspacePresent(domCall(form, "closest", "[data-update-region]")) || !strings.EqualFold(workspaceAttr(form, "method"), "post") ||
		workspacePresent(submitter) && (workspaceAttr(submitter, "formaction") != "" || workspaceAttr(submitter, "formmethod") != "") {
		return false
	}
	if id := workspaceAttr(form, "id"); id == "undo-form" || id == "redo-form" {
		return true
	}
	command := workspaceFieldValue(form, "action")
	return workspacePresent(submitter) && domCall(submitter, "matches", ".roll-cell").Bool() && (command == "pitch" || command == "toggle")
}

// shellPaint paints a captured edit before any request: a piano-roll cell
// flips and keeps its confirmed state for a restore; Undo and Redo predict
// nothing and only carry the pending marker that queue sets. A blocked queue
// refuses the edit instead.
func (u *workspaceUI) shellPaint(job workspaceJob) bool {
	if u.blocked.Load() {
		u.status.Set("Refresh saved state before editing again. Your drafts are retained.")
		return false
	}
	if u.cancelRead != nil {
		// An edit outranks a background read; its own response refreshes the
		// visible regions.
		u.cancelRead()
	}
	if u.idle != nil {
		u.idle.Stop()
	}
	cell := job.pendingCell
	if !workspacePresent(cell) || !domCall(cell, "matches", ".roll-cell").Bool() {
		return true
	}
	if !domHas(cell, "data-pending-paint") {
		domSet(cell, "data-confirmed-active", workspaceAttr(cell, "data-active"))
		domSet(cell, "data-confirmed-text", workspaceString(cell, "textContent"))
		domSet(cell, "data-pending-paint", "")
	}
	state, text := "true", "●"
	if workspaceAttr(cell, "data-active") == "true" {
		state, text = "false", ""
	}
	if workspaceAttr(domCall(cell, "closest", ".piano-roll"), "data-pattern-kind") == "drums" {
		text = map[string]string{"true": "×", "false": "·"}[state]
	}
	domSet(cell, "data-active", state)
	domSet(cell, "aria-pressed", state)
	cell.Set("textContent", text)
	return true
}

// settle ends a job's pending marker. Once a cell has no pending edit left, a
// restore puts its confirmed state back; otherwise the region patch that
// follows brings the server's state. Pages without predictions (the panel
// workspace) only lose the marker.
func (u *workspaceUI) settle(job workspaceJob, restore bool) {
	workspacePending(job, -1)
	if cell := job.pendingCell; workspacePresent(cell) && !domHas(cell, "data-workspace-pending") {
		shellForget(cell, restore)
	}
}

func shellForget(cell js.Value, restore bool) {
	if !domHas(cell, "data-pending-paint") {
		return
	}
	if restore {
		active := workspaceAttr(cell, "data-confirmed-active")
		domSet(cell, "data-active", active)
		domSet(cell, "aria-pressed", active)
		cell.Set("textContent", workspaceAttr(cell, "data-confirmed-text"))
	}
	for _, name := range []string{"data-pending-paint", "data-confirmed-active", "data-confirmed-text"} {
		domDrop(cell, name)
	}
}

// restoreAll clears every pending marker and prediction left on the page.
func (u *workspaceUI) restoreAll() {
	nodes := domCall(u.root, "querySelectorAll", "[data-workspace-pending], [data-pending-paint]")
	for i := 0; i < nodes.Length(); i++ {
		domDrop(nodes.Index(i), "data-workspace-pending")
		shellForget(nodes.Index(i), true)
	}
}

// shellHeld reports a region the queue must not patch: the Code lens while
// its editor holds text the user has not saved. Its fileRevision stays on the
// revision the draft was typed on, so a later save takes the merge path.
func shellHeld(wrapper js.Value) bool {
	editor := domCall(wrapper, "querySelector", "#score textarea")
	if !workspacePresent(editor) {
		return false
	}
	value, baseline := workspaceString(editor, "value"), workspaceString(editor, "defaultValue")
	return workspacePresent(domCall(editor, "closest", "[data-code-unsaved]")) || value != baseline && value != workspaceEditorNormalized(baseline)
}

// shellVisible reports a region the user can see: no hidden ancestor, no
// closed <details> around it, and a laid-out parent (the phone layout and the
// collapsed panes hide whole areas with CSS). The wrapper itself has no box.
func shellVisible(wrapper js.Value) bool {
	parent := wrapper.Get("parentElement")
	for node := parent; workspacePresent(node); node = node.Get("parentElement") {
		if node.Get("hidden").Bool() || strings.EqualFold(workspaceString(node, "tagName"), "details") && !node.Get("open").Bool() {
			return false
		}
	}
	return workspacePresent(parent) && parent.Call("getClientRects").Get("length").Int() > 0
}

// Region lists name regions in patch order: the region the job acted in
// first, then shellPatchOrder. Held regions are never named.
const (
	regionsVisible      = iota // an edit: every visible region, with the digest it shows
	regionsAll                 // "Refresh saved state": every region, without digests, so each comes back as HTML
	regionsStale               // the idle read: every stale region, with its digest
	regionsVisibleStale        // a reveal: the visible stale regions, with their digests
)

// shellRegions writes a region list, "id:digest,…".
func (u *workspaceUI) shellRegions(job workspaceJob, mode int) string {
	var out strings.Builder
	first := ""
	if workspacePresent(job.form) {
		first = workspaceAttr(domCall(job.form, "closest", "[data-update-region]"), "data-update-region")
	}
	for i, id := range append([]string{first}, shellPatchOrder...) {
		if id == "" || i > 0 && id == first {
			continue
		}
		wrapper := domRegion(id)
		if !workspacePresent(wrapper) || shellHeld(wrapper) ||
			// Background reads skip the Code lens (it is read when it shows,
			// Phase 3 owns its updates) and regions that need a page reload.
			mode >= regionsStale && (!domHas(wrapper, "data-region-stale") || domHas(wrapper, "data-region-reload") || mode == regionsStale && id == "code") ||
			(mode == regionsVisible || mode == regionsVisibleStale) && !shellVisible(wrapper) {
			continue
		}
		if out.Len() > 0 {
			out.WriteByte(',')
		}
		out.WriteString(id + ":")
		if mode != regionsAll {
			out.WriteString(workspaceAttr(wrapper, "data-region-digest"))
		}
	}
	return out.String()
}

// refreshStale queues a cancellable read of the stale regions: only the
// visible ones when a region opens, all of them when the queue is idle.
func (u *workspaceUI) refreshStale(mode int) {
	if u.reading || u.closed.Load() {
		// One read at a time: it covers what is stale when it runs, and an
		// edit cancels it.
		return
	}
	regions := u.shellRegions(workspaceJob{}, mode)
	if regions == "" {
		return
	}
	ctx, cancel := context.WithCancel(u.ctx)
	u.cancelRead, u.reading = cancel, true
	select {
	case u.jobs <- workspaceJob{method: http.MethodGet, form: js.Undefined(), regions: regions, ctx: ctx}:
	default:
		u.reading = false
		cancel()
	}
}

// scheduleIdle refreshes stale regions once the queue has been idle for
// 250 ms. The wait doubles after a failed read, up to 8 s.
func (u *workspaceUI) scheduleIdle() {
	if u.idle != nil {
		u.idle.Stop()
	}
	u.idleDelay = max(u.idleDelay, shellIdleRefresh)
	u.idle = time.AfterFunc(u.idleDelay, func() {
		if !u.blocked.Load() && !u.busy.Load() && len(u.jobs) == 0 {
			u.refreshStale(regionsStale)
		}
	})
}

// shellEnginesKept reports whether a region's new HTML keeps every engine
// mount the page has there, and adds none. GoSX mounts engines only during a
// page bootstrap, so a patch must not add, remove or replace one.
func shellEnginesKept(wrapper js.Value, markup string) bool {
	mounts := domCall(wrapper, "querySelectorAll", "[data-gosx-engine]")
	for i := 0; i < mounts.Length(); i++ {
		// data-engine-props hashes the props the engine mounted with, without
		// its revision fields. A change means only a remount brings them.
		props := workspaceAttr(mounts.Index(i), "data-engine-props")
		if !strings.Contains(markup, `id="`+workspaceAttr(mounts.Index(i), "id")+`"`) || props != "" && !strings.Contains(markup, `data-engine-props="`+props+`"`) {
			return false
		}
	}
	return strings.Count(markup, "data-gosx-engine=") == mounts.Length()
}

// applyRegions applies an edit or read response under rule R1. The region the
// server lists first is patched first; the regions that need no HTML advance
// before the rest are patched, one per task, so input keeps flowing. Every
// region left on an older revision is marked stale.
func (u *workspaceUI) applyRegions(projection workspaceProjection, job workspaceJob) (err error) {
	defer func() {
		if recover() != nil {
			err = fmt.Errorf("The workspace view could not be updated.")
		}
	}()
	if projection.Revision == "" {
		return fmt.Errorf("The server returned no saved revision.")
	}
	document := js.Global().Get("document")
	confirm := func(wrapper js.Value, digest string) {
		if digest != "" {
			domSet(wrapper, "data-region-digest", digest)
		}
		domSet(wrapper, "data-region-revision", projection.Revision)
		domSet(wrapper, "data-region-source-revision", projection.SourceRevision)
		domDrop(wrapper, "data-region-stale")
	}
	// A region whose engines changed can only be shown by a page load. When
	// one is visible, nothing is unsaved and no edit waits, reload before
	// applying anything: the page must not look saved, and accept the next
	// edit, while a reload is on its way.
	for _, update := range projection.Regions {
		if wrapper := domRegion(update.ID); workspacePresent(wrapper) && !shellHeld(wrapper) && !shellEnginesKept(wrapper, update.HTML) && shellVisible(wrapper) && len(u.jobs) == 0 && !hasUnsavedWork() {
			u.reload()
			return nil
		}
	}
	scrolls := u.captureScrolls()
	x, y := js.Global().Get("scrollX").Float(), js.Global().Get("scrollY").Float()
	for i, update := range projection.Regions {
		if i == 1 {
			// The first region is the one the user is looking at. Confirm the
			// regions that need no HTML now, then yield before each other one.
			u.advanceRegions(projection, job, confirm)
		}
		wrapper := domRegion(update.ID)
		if !workspacePresent(wrapper) {
			continue
		}
		if i > 0 {
			// Yield to the browser so input and paint run between regions.
			time.Sleep(time.Millisecond)
			if u.closed.Load() {
				return nil
			}
		}
		// Checked after the yield: a keystroke handled there can make the
		// Code editor dirty, and a held region is never patched.
		if ruleR1(update.ID, shellHeld(wrapper), true, false, false) != regionPatch {
			continue
		}
		if !shellEnginesKept(wrapper, update.HTML) {
			// Not patched, so not confirmed fresh: it keeps its revision
			// fields (rule R1). Background reads skip it; the page reloads
			// once it shows, nothing is unsaved and no edit waits.
			domSet(wrapper, "data-region-reload", "")
			continue
		}
		// A draft typed into the Code editor keeps the file revision it was
		// typed on, so a later save takes the merge path instead of
		// overwriting the newer file.
		files := domCall(wrapper, "querySelectorAll", "input[type=hidden]:is([name=fileRevision],[name=diskRevision])")
		kept := make([]string, files.Length())
		for j := range kept {
			kept[j] = workspaceString(files.Index(j), "value")
		}
		if _, _, patchErr := u.applyTree(domCall(wrapper, "querySelector", ".update-region__body"), "update-"+update.ID, update.HTML, job, nil); patchErr != nil {
			continue
		}
		if shellHeld(wrapper) {
			if files = domCall(wrapper, "querySelectorAll", "input[type=hidden]:is([name=fileRevision],[name=diskRevision])"); files.Length() == len(kept) {
				for j, value := range kept {
					files.Index(j).Set("value", value)
					files.Index(j).Set("defaultValue", value)
				}
			}
			continue
		}
		confirm(wrapper, update.Digest)
	}
	if len(projection.Regions) < 2 {
		u.advanceRegions(projection, job, confirm)
	}
	wrappers := domCall(u.root, "querySelectorAll", "[data-update-region]")
	for i := 0; i < wrappers.Length(); i++ {
		// The pair names the saved state: a save to another project file
		// moves only the source revision, and forms carry both.
		if wrapper := wrappers.Index(i); workspaceAttr(wrapper, "data-region-revision") != projection.Revision || workspaceAttr(wrapper, "data-region-source-revision") != projection.SourceRevision {
			domSet(wrapper, "data-region-stale", "")
		}
	}
	for _, scroll := range scrolls {
		if scroll.node.Get("isConnected").Bool() {
			scroll.node.Set("scrollTop", scroll.top)
			scroll.node.Set("scrollLeft", scroll.left)
		}
	}
	if js.Global().Get("scrollX").Float() != x || js.Global().Get("scrollY").Float() != y {
		js.Global().Call("scrollTo", x, y)
	}
	if projection.Title != "" {
		document.Set("title", projection.Title)
	}
	if location, safe := workspaceURL(projection.Location); safe && job.method == http.MethodPost {
		// The response names the selection; the address keeps its fragment.
		js.Global().Get("history").Call("replaceState", js.Global().Get("history").Get("state"), "", location.RequestURI()+js.Global().Get("location").Get("hash").String())
	}
	workspaceUpdated.run()
	document.Call("dispatchEvent", js.Global().Get("Event").New("cicada:workspace-updated"))
	u.reloadIfNeeded()
	return nil
}

// reloadIfNeeded loads the page again when a region that needs it shows and
// no edit waits. Unsaved work keeps the page; the refresh notice asks instead.
func (u *workspaceUI) reloadIfNeeded() {
	if u.blocked.Load() || len(u.jobs) > 0 {
		return
	}
	wrappers := domCall(u.root, "querySelectorAll", "[data-region-reload]")
	for i := 0; i < wrappers.Length(); i++ {
		if shellVisible(wrappers.Index(i)) {
			if hasUnsavedWork() {
				refreshShell()
			} else {
				u.reload()
			}
			return
		}
	}
}

// reload renders the page again the way an edit did before update regions:
// GoSX's same-URL revalidation reconciles the page and remounts the engines
// whose props changed, without a document load. The queue stays blocked
// until the new page binds a fresh one.
func (u *workspaceUI) reload() {
	u.blocked.Store(true)
	u.status.Set("Updating Studio to show the saved song…")
	if gosx := js.Global().Get("__gosx"); workspacePresent(gosx) && workspacePresent(gosx.Get("navigation")) && gosx.Get("navigation").Get("revalidate").Type() == js.TypeFunction {
		navigation := gosx.Get("navigation")
		navigation.Call("revalidate")
		return
	}
	js.Global().Get("location").Call("reload")
}

// refreshThenSubmit reads a stale region before a form in it is sent. If the
// server reports the region unchanged, the user saw current content and the
// form goes out once, with the fresh revision fields. If the content changed,
// the refreshed form stays for the user to check and submit again. Nothing
// was sent before the read, so this never repeats a write.
func (u *workspaceUI) refreshThenSubmit(region, form, submitter js.Value) {
	id, digest := workspaceAttr(region, "data-update-region"), workspaceAttr(region, "data-region-digest")
	select {
	case u.jobs <- workspaceJob{method: http.MethodGet, form: js.Undefined(), regions: id + ":" + digest, then: func(applied bool) {
		switch {
		case !applied || !form.Get("isConnected").Bool():
			u.status.Set("This panel could not be refreshed, so nothing was saved. Submit it again.")
		case workspaceAttr(domRegion(id), "data-region-digest") != digest:
			u.status.Set("This panel changed since it was shown. Check it and submit again.")
		case workspacePresent(submitter) && submitter.Get("isConnected").Bool():
			form.Call("requestSubmit", submitter)
		default:
			form.Call("requestSubmit")
		}
	}}:
	default:
	}
}

// dropHistory removes queued Undo and Redo after a refused edit: they were
// meant for the edit that did not happen and would undo an earlier one.
func (u *workspaceUI) dropHistory() {
	var keep []workspaceJob
	dropped := false
	for len(u.jobs) > 0 {
		job := <-u.jobs
		if job.target == "/__actions/undo" || job.target == "/__actions/redo" {
			u.settle(job, true)
			dropped = true
			continue
		}
		keep = append(keep, job)
	}
	for _, job := range keep {
		u.jobs <- job
	}
	if dropped {
		u.status.Set(u.status.Get() + " The Undo or Redo waiting behind it was not sent.")
	}
}

// engineVersion is the revision an engine writes with: its update region's,
// so rule R1 covers engines too (a stale region's engine is refused). Outside
// the shell it is the fallback.
func engineVersion(node js.Value, fallback workspaceVersion) workspaceVersion {
	if !workspacePresent(node) || node.Get("closest").Type() != js.TypeFunction {
		return fallback
	}
	if region := domCall(node, "closest", "[data-update-region]"); workspacePresent(region) && workspaceAttr(region, "data-region-revision") != "" {
		return workspaceVersion{Revision: workspaceAttr(region, "data-region-revision"), SourceRevision: workspaceAttr(region, "data-region-source-revision"), Seq: fallback.Seq}
	}
	return fallback
}

// advanceRegions confirms what R1 lets through without HTML: regions the
// server reported unchanged, and on a write the revision-only regions. Their
// hidden revision fields take the response's values; a field the server
// rendered empty stays empty.
func (u *workspaceUI) advanceRegions(projection workspaceProjection, job workspaceJob, confirm func(js.Value, string)) {
	for i, id := range append([]string{"transport"}, projection.Unchanged...) {
		wrapper := domRegion(id)
		if !workspacePresent(wrapper) || ruleR1(id, shellHeld(wrapper), false, i > 0, job.method == http.MethodPost) != regionAdvance {
			continue
		}
		fields := domCall(wrapper, "querySelectorAll", "input[type=hidden]:is([name=revision],[name=source_revision],[name=fileRevision],[name=diskRevision])")
		for j := 0; j < fields.Length(); j++ {
			field, value := fields.Index(j), projection.FileRevision
			switch workspaceAttr(field, "name") {
			case "revision":
				value = projection.Revision
			case "source_revision":
				value = projection.SourceRevision
			}
			if value != "" && workspaceString(field, "value") != "" {
				field.Set("value", value)
				field.Set("defaultValue", value)
			}
		}
		confirm(wrapper, "")
	}
}

// Small DOM helpers. They stay out of line on purpose: in Go/WASM each inline
// js.Value.Call builds its own argument slice, and the UI engine has a size
// budget.

//go:noinline
func domSet(node js.Value, name, value string) { node.Call("setAttribute", name, value) }

//go:noinline
func domDrop(node js.Value, name string) { node.Call("removeAttribute", name) }

//go:noinline
func domHas(node js.Value, name string) bool { return node.Call("hasAttribute", name).Bool() }

//go:noinline
func domCall(node js.Value, method, argument string) js.Value { return node.Call(method, argument) }

//go:noinline
func domRegion(id string) js.Value {
	return js.Global().Get("document").Call("getElementById", "update-"+id)
}
