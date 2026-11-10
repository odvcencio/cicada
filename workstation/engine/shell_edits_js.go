//go:build js && wasm

package main

import (
	"context"
	"fmt"
	"strconv"
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
	u := &workspaceUI{root: root, patch: patch, props: props, ctx: ctx, cancel: cancel, jobs: make(chan workspaceJob, 128), revision: signal.New(props.Revision), sourceRevision: props.SourceRevision, status: signal.New(""), drafts: map[string]workspaceDraft{}, editors: map[string]workspaceEditorBaseline{}, shell: true, own: workspaceVersion{Revision: props.Revision, SourceRevision: props.SourceRevision}}
	u.seedEditorBaseline()
	// A page reload ends the old queue. A notice it left for the user shows
	// here, and markers it left on regions this render shows fresh are gone.
	storage := js.Global().Get("sessionStorage")
	if notice := storage.Call("getItem", shellNotice); notice.Type() == js.TypeString {
		u.status.Set(notice.String())
		storage.Call("removeItem", shellNotice)
	}
	wrappers := domCall(root, "querySelectorAll", "[data-update-region]")
	for i := 0; i < wrappers.Length(); i++ {
		if wrapper := wrappers.Index(i); workspaceAttr(wrapper, "data-region-revision") == props.Revision && workspaceAttr(wrapper, "data-region-source-revision") == props.SourceRevision {
			for _, name := range []string{"data-region-stale", "data-region-reload", "data-region-foreign"} {
				domDrop(wrapper, name)
			}
		}
	}
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
			u.jobs <- workspaceJob{method: methodGet, form: js.Undefined(), refresh: true, regions: u.shellRegions(workspaceJob{}, regionsAll)}
		}
	})
	// A stale region refreshes when its tab, pane, section or dialog opens. A
	// submit held in a region that closed (Escape, another tab) is not sent.
	reveal := js.FuncOf(func(_ js.Value, args []js.Value) any {
		// Only a real change counts: rewriting an attribute with the value it
		// had (a tab list resyncing its panels) must not read again, or a
		// failing read would repeat for as long as the page lives.
		changed := false
		for i := 0; len(args) > 0 && i < args[0].Length() && !changed; i++ {
			record := args[0].Index(i)
			changed = !record.Get("oldValue").Equal(domCall(record.Get("target"), "getAttribute", record.Get("attributeName").String()))
		}
		if !changed {
			return nil
		}
		for _, h := range append([]*shellHold(nil), u.holds...) {
			if !shellVisible(domRegion(h.region)) {
				u.releaseHold(h, shellClosed)
			}
		}
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
	options := map[string]any{"attributes": true, "attributeOldValue": true, "attributeFilter": []any{"hidden", "open", "data-phone-region"}}
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
		// edit cancels it. A region that opens meanwhile is read after it.
		u.revealWanted = u.revealWanted || mode == regionsVisibleStale
		return
	}
	regions := u.shellRegions(workspaceJob{}, mode)
	if regions == "" {
		return
	}
	ctx, cancel := context.WithCancel(u.ctx)
	u.cancelRead, u.reading = cancel, true
	select {
	case u.jobs <- workspaceJob{method: methodGet, form: js.Undefined(), regions: regions, ctx: ctx}:
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
		domDrop(wrapper, "data-region-foreign")
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
	// A read that returns a revision this tab did not write reveals another
	// change. A region it did not render still shows the song from before
	// that change, whatever this tab saves next, until it is rendered again.
	foreign := job.method == methodGet && (projection.Revision != u.own.Revision || projection.SourceRevision != u.own.SourceRevision)
	wrappers := domCall(u.root, "querySelectorAll", "[data-update-region]")
	for i := 0; i < wrappers.Length(); i++ {
		// The pair names the saved state: a save to another project file
		// moves only the source revision, and forms carry both.
		if wrapper := wrappers.Index(i); workspaceAttr(wrapper, "data-region-revision") != projection.Revision || workspaceAttr(wrapper, "data-region-source-revision") != projection.SourceRevision {
			domSet(wrapper, "data-region-stale", "")
			if foreign {
				domSet(wrapper, "data-region-foreign", "")
			}
		}
	}
	workspaceRestoreScrolls(scrolls, x, y)
	if projection.Title != "" {
		document.Set("title", projection.Title)
	}
	if location, safe := workspaceURL(projection.Location); safe && job.method == methodPost {
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
	u.reloading = true
	u.status.Set("Updating Studio to show the saved song…")
	if gosx := js.Global().Get("__gosx"); workspacePresent(gosx) && workspacePresent(gosx.Get("navigation")) && gosx.Get("navigation").Get("revalidate").Type() == js.TypeFunction {
		navigation := gosx.Get("navigation")
		navigation.Call("revalidate")
		return
	}
	js.Global().Get("location").Call("reload")
}

// A held submit: a POST form in a stale region waits for its region's read
// (rule R1: nothing is sent from a region on an older revision).
type shellHold struct {
	form, submitter       js.Value
	region, values, token string
	// foreign: when the user clicked, the region still showed the song from
	// before another client's change, so the form never goes out by itself.
	foreign, done bool
}

// How the read that a held submit waits for ended.
const (
	holdDropped = iota // taken off the queue; whoever dropped it tells the user
	holdFailed         // the read failed or the region is still stale
	holdForeign        // the read found a revision this tab did not write
	holdOwn            // the read found the revision of this tab's own writes
)

// refreshThenSubmit holds a submit from a stale region and reads the region.
// The form goes out by itself only when the region was stale only because of
// this tab's own writes: the read returns the revision of those writes, and
// at the click the region did not still show the song from before another
// client's change. It also has to send what it showed: the same values apart
// from its revision fields, and the same submit button. Otherwise the
// refreshed form is marked and the user checks it and submits again. A form has at most one hold; a click while
// it waits does nothing, and the hold ends if its panel closes, an Undo or Redo
// is dropped, or the queue clears. Nothing was sent before the read, so this
// never repeats a write.
func (u *workspaceUI) refreshThenSubmit(region, form, submitter js.Value) {
	u.held++
	h := &shellHold{form: form, submitter: submitter, region: workspaceAttr(region, "data-update-region"), values: shellFormValues(form, submitter), token: strconv.Itoa(u.held), foreign: domHas(region, "data-region-foreign")}
	marker := submitter
	if !workspacePresent(marker) {
		marker = form
	}
	// The queue's pending marker, tagged with the hold so it can be found
	// again: a region patch may replace the button and carries both over.
	domSet(marker, "data-workspace-held", h.token)
	workspacePending(workspaceJob{pendingCell: marker}, 1)
	u.holds = append(u.holds, h)
	if u.idle != nil {
		u.idle.Stop()
	}
	select {
	case u.jobs <- workspaceJob{method: methodGet, form: js.Undefined(), regions: h.region + ":" + workspaceAttr(region, "data-region-digest"), hold: h, then: func(outcome int) { u.settleHold(h, outcome) }}:
	default:
		u.releaseHold(h, "Too many changes are waiting, so this was not sent. Submit it again.")
	}
}

// settleHold ends a hold once its read has run, and sends the form if it may.
func (u *workspaceUI) settleHold(h *shellHold, outcome int) {
	if !u.releaseHold(h, "") {
		return
	}
	if h.foreign && outcome == holdOwn {
		// Decided at the click: a later own write cannot make it current.
		outcome = holdForeign
	}
	region := domRegion(h.region)
	switch {
	case outcome == holdDropped:
	case u.reloading && outcome == holdForeign:
		// The read needs a page reload to show; nothing is sent meanwhile,
		// and the notice outlives the reload.
		js.Global().Get("sessionStorage").Call("setItem", shellNotice, shellForeign)
	case u.reloading && outcome == holdOwn:
		js.Global().Get("sessionStorage").Call("setItem", shellNotice, shellChanged)
	case outcome == holdFailed || u.blocked.Load() || !h.form.Get("isConnected").Bool() || domHas(region, "data-region-stale"):
		u.status.Set("This panel could not be refreshed, so nothing was saved. Submit it again.")
	case !shellVisible(region):
		u.status.Set(shellClosed)
	case outcome == holdForeign:
		domSet(h.form, "data-workspace-updated", "")
		u.status.Set(shellForeign)
	case shellFormValues(h.form, h.submitter) != h.values:
		domSet(h.form, "data-workspace-updated", "")
		u.status.Set(shellChanged)
	case workspacePresent(h.submitter):
		h.form.Call("requestSubmit", h.submitter)
	default:
		h.form.Call("requestSubmit")
	}
}

const (
	shellClosed  = "This was not sent: its panel closed before it showed the saved song."
	shellForeign = "Another change to the song was saved, so this was not sent. Check the updated panel and submit again."
	shellChanged = "This panel changed since it was shown. Check it and submit again."
	shellNotice  = "cicada.shell-notice"
)

// shellHolding reports whether a form has a submit waiting for a read. The
// hold lives here, not in the DOM: a region patch rewrites the form's
// attributes.
func (u *workspaceUI) shellHolding(form js.Value) bool {
	for _, h := range u.holds {
		if h.form.Equal(form) {
			return true
		}
	}
	return false
}

// releaseHold ends a hold, with a message for the user if one is given. It
// reports false when the hold had already ended.
func (u *workspaceUI) releaseHold(h *shellHold, message string) bool {
	if h.done {
		return false
	}
	h.done = true
	markers := domCall(js.Global().Get("document"), "querySelectorAll", `[data-workspace-held="`+h.token+`"]`)
	for i := 0; i < markers.Length(); i++ {
		domDrop(markers.Index(i), "data-workspace-held")
		workspacePending(workspaceJob{pendingCell: markers.Index(i)}, -1)
	}
	for i, held := range u.holds {
		if held == h {
			u.holds = append(u.holds[:i], u.holds[i+1:]...)
			break
		}
	}
	if message != "" {
		u.status.Set(message)
	}
	return true
}

// shellFormValues is what a form would send, without the fields a read
// renews (the CSRF token and the revisions), and the state of its submit
// button: its name and value, whether it is pressed, active or disabled, and
// whether it is still on the page.
//
//go:noinline
func shellFormValues(form, submitter js.Value) string {
	var out strings.Builder
	workspaceFormEntries(form, func(name, value string) {
		switch name {
		case "csrf_token", "revision", "source_revision", "fileRevision", "diskRevision":
			return
		}
		out.WriteString(name + "=" + value + "\n")
	})
	if workspacePresent(submitter) {
		out.WriteString(workspaceString(submitter, "name") + "=" + workspaceString(submitter, "value") + "\n" + workspaceAttr(submitter, "aria-pressed") + "\n" + workspaceAttr(submitter, "data-active") + "\n" + fmt.Sprint(submitter.Get("disabled").Bool(), submitter.Get("isConnected").Bool()))
	}
	return out.String()
}

// dropHistory removes queued and held Undo and Redo after a refused edit: they
// were meant for the edit that did not happen and would undo an earlier one.
func (u *workspaceUI) dropHistory() {
	if u.drain(func(job workspaceJob) bool {
		if job.hold != nil {
			id := workspaceAttr(job.hold.form, "id")
			return id == "undo-form" || id == "redo-form"
		}
		return job.target == "/__actions/undo" || job.target == "/__actions/redo"
	}) > 0 {
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
		if !workspacePresent(wrapper) || ruleR1(id, shellHeld(wrapper), false, i > 0, job.method == methodPost) != regionAdvance {
			continue
		}
		workspaceSetRevisions(wrapper, projection)
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
