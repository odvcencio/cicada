package ui

// The single-window Studio shell. Region components carry the grid-area classes
// of the GoSX Dock recipe, so the page composes them in any order.
//
// The recipe stand-ins below (Dock, SplitHandleX, SplitHandleY, Tabs markup,
// Tab, TabPanel, Collapsible) render the exact attributes that GoSX's P1 M3
// recipes emit, in the recipes' component names. TODO(gosx-pr-605): when those
// recipes merge, install them with `gosx ui add` and delete the stand-ins.
//
// Tabs follow the WAI-ARIA tab pattern. The server renders the roles, states
// and ids (tablist, tab, tabpanel, aria-selected, aria-controls,
// aria-labelledby) over ordinary ?tab= links, so they work without JavaScript.
// The workbench chunk adds the roving tab stop and the Left/Right/Home/End and
// Enter/Space keys; the server sets no tabindex, so without the chunk every tab
// stays in the normal Tab order.

type ShellFrameProps struct {
	Region     string
	Message    string
	HasMessage bool
	Href       string
}

component ShellFrame(props: ShellFrameProps) {
	return <div id="studio-shell" class="shell" data-phone-region={props.Region}>
		<a class="skip" href="#studio-center">Skip to workspace</a>
		<div class="studio-startup" data-studio-startup-status role="status" aria-live="polite" hidden>
			<span data-studio-startup-message>Opening Studio controls…</span>
			<button type="button" data-studio-startup-reload hidden>Reload Studio</button>
		</div>
		<p id="studio-refresh-notice" class="notice" role="status" hidden>The score changed while you have unsaved changes here. Save or copy them, then <a href={props.Href}>refresh this page</a>. Saving now is refused because the score moved on.</p>
		<If cond={props.HasMessage}><p class="notice" role="status">{props.Message}</p></If>
		<div class="notices" data-gosx-toast-host role="status" aria-live="polite"></div>
		{children}
	</div>
}

type DockProps struct {
	ID           string
	LeftSignal   string
	RightSignal  string
	BottomSignal string
	Left         string
	Right        string
	Bottom       string
}

component Dock(props: DockProps) {
	return <div
		id={props.ID}
		class="gsx-dock"
		style={"--gsx-dock-left: " + props.Left + "px; --gsx-dock-right: " + props.Right + "px; --gsx-dock-bottom: " + props.Bottom + "px"}
		data-gosx-bind-style={"--gsx-dock-left:" + props.LeftSignal + ":px,--gsx-dock-right:" + props.RightSignal + ":px,--gsx-dock-bottom:" + props.BottomSignal + ":px"}
	>
		{children}
	</div>
}

type SplitHandleProps struct {
	Signal string
	Label  string
	Min    string
	Max    string
	Value  string
}

component SplitHandleX(props: SplitHandleProps) {
	return <div
		class="gsx-split__handle"
		role="separator"
		tabindex="0"
		aria-orientation="vertical"
		aria-label={props.Label}
		aria-valuemin={props.Min}
		aria-valuemax={props.Max}
		aria-valuenow={props.Value}
		data-gosx-drag={props.Signal}
		data-gosx-drag-axis="x"
		data-gosx-drag-min={props.Min}
		data-gosx-drag-max={props.Max}
		data-gosx-drag-step="8"
		data-gosx-drag-scale="1"
	></div>
}

// SplitHandleXReverse sits on the left edge of a right-hand pane, so motion to
// the left grows the pane (the recipe documents data-gosx-drag-scale="-1").
component SplitHandleXReverse(props: SplitHandleProps) {
	return <div
		class="gsx-split__handle"
		role="separator"
		tabindex="0"
		aria-orientation="vertical"
		aria-label={props.Label}
		aria-valuemin={props.Min}
		aria-valuemax={props.Max}
		aria-valuenow={props.Value}
		data-gosx-drag={props.Signal}
		data-gosx-drag-axis="x"
		data-gosx-drag-min={props.Min}
		data-gosx-drag-max={props.Max}
		data-gosx-drag-step="8"
		data-gosx-drag-scale="-1"
	></div>
}

component SplitHandleY(props: SplitHandleProps) {
	return <div
		class="gsx-split__handle"
		role="separator"
		tabindex="0"
		aria-orientation="horizontal"
		aria-label={props.Label}
		aria-valuemin={props.Min}
		aria-valuemax={props.Max}
		aria-valuenow={props.Value}
		data-gosx-drag={props.Signal}
		data-gosx-drag-axis="y"
		data-gosx-drag-min={props.Min}
		data-gosx-drag-max={props.Max}
		data-gosx-drag-step="8"
		data-gosx-drag-scale="1"
	></div>
}

type TabProps struct {
	ID       string
	Value    string
	Href     string
	Panel    string
	Selected bool
}

component Tab(props: TabProps) {
	return <>
		<If cond={props.Selected}>
			<a
				id={props.ID}
				class="gsx-tabs__tab"
				href={props.Href}
				data-gosx-native="true"
				role="tab"
				aria-selected="true"
				aria-controls={props.Panel}
				data-gosx-tab={props.Value}
				data-gosx-tab-panel={props.Panel}
				aria-current="page"
			>{children}</a>
		</If>
		<If cond={props.Selected == false}>
			<a
				id={props.ID}
				class="gsx-tabs__tab"
				href={props.Href}
				data-gosx-native="true"
				role="tab"
				aria-selected="false"
				aria-controls={props.Panel}
				data-gosx-tab={props.Value}
				data-gosx-tab-panel={props.Panel}
			>{children}</a>
		</If>
	</>
}

type TabPanelProps struct {
	ID       string
	Tab      string
	Hidden   bool
}

component TabPanel(props: TabPanelProps) {
	return <section id={props.ID} class="gsx-tabs__panel" role="tabpanel" aria-labelledby={props.Tab} hidden={props.Hidden}>{children}</section>
}

type CollapsibleProps struct {
	Signal  string
	Summary string
	Open    bool
}

component Collapsible(props: CollapsibleProps) {
	return <details class="gsx-collapsible" data-gosx-collapsible={props.Signal} open={props.Open}>
		<summary>{props.Summary}</summary>
		{children}
	</details>
}

// TopBar's children are the score title and the transport toolbar. The page
// wraps the title in an update region, so it is a separate component.
component TopBar() {
	return <header class="gsx-dock__top shell-top">
		<a class="brand" href="/studio" aria-label="Cicada Studio"><span class="brand-mark" aria-hidden="true"></span></a>
		{children}
	</header>
}

type ScoreTitleProps struct {
	Title    string
	Filename string
}

component ScoreTitle(props: ScoreTitleProps) {
	return <div class="score-title"><h1>{props.Title}</h1><span class="score-sub">{props.Filename}</span></div>
}

// The transport toolbar's children are forms and command buttons from the page.
component TransportBar() {
	return <div class="shell-transport" role="toolbar" aria-label="Transport">{children}</div>
}

type TempoProps struct {
	Tempo string
	Key   string
}

// Tempo shows the song's tempo and key beside the position counter.
component Tempo(props: TempoProps) {
	return <div class="shell-tempo"><b>{props.Tempo}</b><span>BPM</span><small>{props.Key}</small></div>
}

// StatusBar is the workspace's bottom line: the saved state on the left and
// the audio service on the right. Its children are the page's status items.
component StatusBar() {
	return <footer class="gsx-dock__status shell-status" aria-label="Status">{children}</footer>
}

type StatusItemProps struct {
	Label string
	Value string
}

component StatusItem(props: StatusItemProps) {
	return <span class="shell-status__item"><span class="shell-status__label">{props.Label}</span>{props.Value}</span>
}

type PaneToggleProps struct {
	Show  string
	Hide  string
	Label string
	Pane  string
}

// PaneToggle opens or closes a side pane through its layout signal. Only the
// button that changes the current state shows; CSS reads the pane's state.
component PaneToggle(props: PaneToggleProps) {
	return <>
		<button type="button" class="shell-command shell-pane-toggle" data-pane={props.Pane} data-pane-action="show" data-gosx-command={props.Show} aria-pressed="false">{props.Label}</button>
		<button type="button" class="shell-command shell-pane-toggle" data-pane={props.Pane} data-pane-action="hide" data-gosx-command={props.Hide} aria-pressed="true">{props.Label}</button>
	</>
}

type ActionFormProps struct {
	ID       string
	Action   string
	CSRF     string
	Revision string
	ReturnTo string
}

// ActionForm is the managed form of ui.Form with an id, so a command can name it.
component ActionForm(props: ActionFormProps) {
	return <form id={props.ID} method="post" action={props.Action} data-gosx-managed class="action-form">
		<input type="hidden" name="csrf_token" value={props.CSRF} />
		<input type="hidden" name="revision" value={props.Revision} />
		<input type="hidden" name="__gosx_return_to" value={props.ReturnTo} />
		{children}
	</form>
}

type CommandButtonProps struct {
	ID    string
	Label string
	Glyph string
	Keys  string
}

component CommandButton(props: CommandButtonProps) {
	return <button type="button" class="shell-command transport-glyph" data-gosx-command={props.ID} aria-keyshortcuts={props.Keys} aria-label={props.Label} title={props.Label}>{props.Glyph}</button>
}

type PlaceholderProps struct {
	Label string
	Glyph string
}

component Placeholder(props: PlaceholderProps) {
	return <button type="button" class="shell-command transport-glyph" disabled aria-disabled="true" aria-label={props.Label} title={props.Label}>{props.Glyph}</button>
}

type DialogTriggerProps struct {
	Target   string
	Controls string
	Label  string
}

component DialogTrigger(props: DialogTriggerProps) {
	return <button type="button" class="shell-command" data-gosx-disclosure-target={props.Target} aria-controls={props.Controls} aria-expanded="false">{props.Label}</button>
}

type IconDialogTriggerProps struct {
	Target   string
	Controls string
	Label    string
	Glyph    string
}

// IconDialogTrigger is a DialogTrigger whose visible text is a glyph; the
// label stays its accessible name.
component IconDialogTrigger(props: IconDialogTriggerProps) {
	return <button type="button" class="shell-command transport-glyph" data-gosx-disclosure-target={props.Target} aria-controls={props.Controls} aria-expanded="false" aria-label={props.Label} title={props.Label}>{props.Glyph}</button>
}

type StatusTriggerProps struct {
	Target   string
	Controls string
	Label    string
	Text     string
}

// StatusTrigger opens a dialog from the status bar. It names the dialog, then
// the current status, so both are part of its accessible name.
component StatusTrigger(props: StatusTriggerProps) {
	return <button type="button" class="shell-status__trigger" data-gosx-disclosure-target={props.Target} aria-controls={props.Controls} aria-expanded="false"><span class="shell-status__label">{props.Label}</span> <span>{props.Text}</span></button>
}

type DialogFrameProps struct {
	ID    string
	Title string
}

component DialogFrame(props: DialogFrameProps) {
	return <>
		<div class="shell-backdrop" data-gosx-disclosure-backdrop={"#" + props.ID} hidden></div>
		<div id={props.ID} class="shell-dialog" role="dialog" aria-modal="true" aria-label={props.Title} data-gosx-disclosure hidden>
			<div class="shell-dialog__bar">
				<h2>{props.Title}</h2>
				<button type="button" data-gosx-disclosure-close={"#" + props.ID} data-gosx-disclosure-initial-focus>Close</button>
			</div>
			<div class="shell-dialog__body">{children}</div>
		</div>
	</>
}

type BrowserProps struct {
	Open  bool
	Width string
}

component Browser(props: BrowserProps) {
	return <aside id="studio-browser" class="gsx-dock__left shell-region" aria-label="Library">
		<details class="gsx-collapsible shell-collapsible" data-gosx-collapsible="$layout.browserOpen" open={props.Open}>
			<summary>Library</summary>
			<div class="shell-scroll">{children}</div>
		</details>
		<SplitHandleX Signal="$layout.browserWidth" Label="Resize Library" Min="160" Max="640" Value={props.Width} />
	</aside>
}

type BrowserSectionProps struct {
	Signal string
	ID    string
	Title string
	Open  bool
}

component BrowserSection(props: BrowserSectionProps) {
	return <details id={props.ID} class="shell-section" data-gosx-collapsible={props.Signal} open={props.Open}>
		<summary>{props.Title}</summary>
		{children}
	</details>
}

type CenterProps struct {
	View         string
	ArrangeHref  string
	SessionHref  string
	MixerHref    string
	ArrangeShown bool
	SessionShown bool
	MixerShown   bool
}

component Center(props: CenterProps) {
	return <main id="studio-center" class="gsx-dock__center shell-region" data-studio-region={props.View}>
		<div id="view-tabs" data-gosx-tabs data-gosx-tabs-signal="$view" class="shell-tabs">
			<nav class="gsx-tabs__list" role="tablist" aria-orientation="horizontal" aria-label="Views">
				<Tab ID="view-tab-arrange" Value="arrange" Href={props.ArrangeHref} Panel="view-panel-arrange" Selected={props.ArrangeShown}>Arrange</Tab>
				<Tab ID="view-tab-session" Value="session" Href={props.SessionHref} Panel="view-panel-session" Selected={props.SessionShown}>Session</Tab>
				<Tab ID="view-tab-mixer" Value="mixer" Href={props.MixerHref} Panel="view-panel-mixer" Selected={props.MixerShown}>Mixer</Tab>
			</nav>
			<div class="shell-scroll">{children}</div>
		</div>
	</main>
}

type DetailDockProps struct {
	ClipHref       string
	DeviceHref     string
	AutomationHref string
	ClipShown      bool
	DeviceShown    bool
	AutomationShown bool
	Height         string
}

// DetailDock's children are the selection header and the ShellScroll that
// holds the three tab panels. The page wraps the header in an update region.
component DetailDock(props: DetailDockProps) {
	return <section id="studio-detail" class="gsx-dock__bottom shell-region" aria-label="Detail dock">
		<SplitHandleY Signal="$layout.dockHeight" Label="Resize Detail dock" Min="120" Max="640" Value={props.Height} />
		<div id="detail-tabs" data-gosx-tabs data-gosx-tabs-signal="$dock" class="shell-tabs">
			<nav class="gsx-tabs__list" role="tablist" aria-orientation="horizontal" aria-label="Detail">
				<Tab ID="detail-tab-clip" Value="clip" Href={props.ClipHref} Panel="detail-panel-clip" Selected={props.ClipShown}>Clip</Tab>
				<Tab ID="detail-tab-device" Value="device" Href={props.DeviceHref} Panel="detail-panel-device" Selected={props.DeviceShown}>Device</Tab>
				<Tab ID="detail-tab-automation" Value="automation" Href={props.AutomationHref} Panel="detail-panel-automation" Selected={props.AutomationShown}>Automation</Tab>
			</nav>
			{children}
		</div>
	</section>
}

type DockSelectionProps struct {
	Selection       string
	SelectionDetail string
	TrackDetail     string
}

// DockSelection names what the detail dock edits: the Clip tab's pattern, and
// the Device and Automation tabs' track. CSS shows the one for the open tab.
component DockSelection(props: DockSelectionProps) {
	return <>
		<p class="shell-dock-selection" data-dock-selection="clip"><strong>{props.Selection}</strong> <span>{props.SelectionDetail}</span></p>
		<p class="shell-dock-selection" data-dock-selection="track"><span>{props.TrackDetail}</span></p>
	</>
}

component ShellScroll() {
	return <div class="shell-scroll">{children}</div>
}

type CodeLensProps struct {
	Open  bool
	Width string
}

component CodeLens(props: CodeLensProps) {
	return <aside id="studio-code" class="gsx-dock__right shell-region" aria-label="Code lens">
		<SplitHandleXReverse Signal="$layout.codeWidth" Label="Resize Code lens" Min="240" Max="720" Value={props.Width} />
		<details class="gsx-collapsible shell-collapsible" data-gosx-collapsible="$layout.codeOpen" open={props.Open}>
			<summary>Code</summary>
			<div class="shell-scroll">{children}</div>
		</details>
	</aside>
}

type PhoneLinkProps struct {
	Href    string
	Current bool
	Region  string
}

component PhoneLink(props: PhoneLinkProps) {
	return <>
		<If cond={props.Current}><a href={props.Href} data-gosx-native="true" data-shell-region={props.Region} aria-current="page">{children}</a></If>
		<If cond={props.Current == false}><a href={props.Href} data-gosx-native="true" data-shell-region={props.Region}>{children}</a></If>
	</>
}

type PhoneNavProps struct {
	CenterHref  string
	BrowserHref string
	DetailHref  string
	CodeHref    string
	BrowserOn   bool
	CenterOn    bool
	DetailOn    bool
	CodeOn      bool
}

// PhoneNav is visible only at phone width, where one region shows at a time.
component PhoneNav(props: PhoneNavProps) {
	return <nav class="shell-phone-nav" aria-label="Regions">
		<PhoneLink Href={props.CenterHref} Current={props.CenterOn} Region="center">View</PhoneLink>
		<PhoneLink Href={props.BrowserHref} Current={props.BrowserOn} Region="browser">Library</PhoneLink>
		<PhoneLink Href={props.DetailHref} Current={props.DetailOn} Region="detail">Edit</PhoneLink>
		<PhoneLink Href={props.CodeHref} Current={props.CodeOn} Region="code">Code</PhoneLink>
	</nav>
}

type ShortcutGroupProps struct{ Title string }

component ShortcutGroup(props: ShortcutGroupProps) {
	return <section class="shortcut-group"><h3>{props.Title}</h3><ul>{children}</ul></section>
}

type ShortcutRowProps struct {
	Title       string
	Keys        string
	Unavailable bool
}

component ShortcutRow(props: ShortcutRowProps) {
	return <li>
		<span class="shortcut-title">{props.Title}</span>
		<kbd>{props.Keys}</kbd>
		<If cond={props.Unavailable}><span class="muted">Unavailable</span></If>
	</li>
}

type ActionButtonProps struct {
	Name  string
	Value string
	Label string
	Text  string
	Class string
}

component ActionButton(props: ActionButtonProps) {
	return <button type="submit" name={props.Name} value={props.Value} class={props.Class} aria-label={props.Label} title={props.Label}>{props.Text}</button>
}

type HiddenFieldProps struct {
	Name  string
	Value string
}

component HiddenField(props: HiddenFieldProps) {
	return <input type="hidden" name={props.Name} value={props.Value} />
}

// EditFeedback reports the shell's edit queue: a refused or failed save and the
// explicit recovery action. CSS hides it while it is empty.
component EditFeedback() {
	return <div class="workspace-feedback">
		<span data-workspace-status role="status" aria-live="polite"></span>
		<button type="button" data-workspace-retry hidden>Refresh saved state</button>
	</div>
}

component CaptureHint() {
	return <p class="transport-capture-hint" data-transport-capture-hint hidden>Recording in progress. Stop saves your take; finish it before changing playback.</p>
}

component ShortcutNotes() {
	return <section class="shortcut-group">
		<h3>Keyboard focus</h3>
		<p class="muted">Tab and Shift+Tab always move keyboard focus. Inside a tab list, Left and Right arrows, Home and End move between tabs, and Enter or Space opens one.</p>
		<p class="muted">Single-key shortcuts (B, Z, X and ?) do not run while you type in a text field, a list or a menu.</p>
	</section>
}
