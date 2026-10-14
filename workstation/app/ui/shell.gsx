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
}

component ShellFrame(props: ShellFrameProps) {
	return <div id="studio-shell" class="shell" data-phone-region={props.Region}>
		<a class="skip" href="#studio-center">Skip to workspace</a>
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
				role="tab"
				aria-selected="true"
				aria-controls={props.Panel}
				data-gosx-tab={props.ID}
				data-gosx-tab-panel={props.Panel}
				aria-current="page"
			>{children}</a>
		</If>
		<If cond={props.Selected == false}>
			<a
				id={props.ID}
				class="gsx-tabs__tab"
				href={props.Href}
				role="tab"
				aria-selected="false"
				aria-controls={props.Panel}
				data-gosx-tab={props.ID}
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

type TopBarProps struct {
	Title    string
	Filename string
	Tempo    string
	Key      string
	Backend  string
}

// The transport toolbar's children are forms and command buttons from the page.
component TopBar(props: TopBarProps) {
	return <header class="gsx-dock__top shell-top">
		<a class="brand" href="/studio">CICADA <span>STUDIO</span></a>
		<div class="score-title"><h1>{props.Title}</h1><span class="muted">{props.Filename}</span></div>
		<div class="score-meta"><span>{props.Tempo} BPM</span><span>{props.Key}</span><span class="engine-label">{props.Backend}</span></div>
		<div class="shell-transport" role="toolbar" aria-label="Transport">{children}</div>
	</header>
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
	Keys  string
}

component CommandButton(props: CommandButtonProps) {
	return <button type="button" class="shell-command" data-gosx-command={props.ID} aria-keyshortcuts={props.Keys}>{props.Label}</button>
}

type PlaceholderProps struct{ Label string }

component Placeholder(props: PlaceholderProps) {
	return <button type="button" class="shell-command" disabled aria-disabled="true">{props.Label}</button>
}

type DialogTriggerProps struct {
	Target string
	Label  string
}

component DialogTrigger(props: DialogTriggerProps) {
	return <button type="button" class="shell-command" data-gosx-disclosure-target={props.Target} aria-controls={props.Target} aria-expanded="false">{props.Label}</button>
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
	return <aside id="studio-browser" class="gsx-dock__left shell-region" aria-label="Browser">
		<details class="gsx-collapsible shell-collapsible" data-gosx-collapsible="$layout.browserOpen" open={props.Open}>
			<summary>Browser</summary>
			<div class="shell-scroll">{children}</div>
		</details>
		<SplitHandleX Signal="$layout.browserWidth" Label="Resize Browser" Min="160" Max="640" Value={props.Width} />
	</aside>
}

type BrowserSectionProps struct {
	ID    string
	Title string
	Open  bool
}

component BrowserSection(props: BrowserSectionProps) {
	return <details id={props.ID} class="shell-section" open={props.Open}>
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
				<Tab ID="view-tab-arrange" Href={props.ArrangeHref} Panel="view-panel-arrange" Selected={props.ArrangeShown}>Arrange</Tab>
				<Tab ID="view-tab-session" Href={props.SessionHref} Panel="view-panel-session" Selected={props.SessionShown}>Session</Tab>
				<Tab ID="view-tab-mixer" Href={props.MixerHref} Panel="view-panel-mixer" Selected={props.MixerShown}>Mixer</Tab>
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

component DetailDock(props: DetailDockProps) {
	return <section id="studio-detail" class="gsx-dock__bottom shell-region" aria-label="Detail dock">
		<SplitHandleY Signal="$layout.dockHeight" Label="Resize Detail dock" Min="120" Max="640" Value={props.Height} />
		<div id="detail-tabs" data-gosx-tabs data-gosx-tabs-signal="$dock" class="shell-tabs">
			<nav class="gsx-tabs__list" role="tablist" aria-orientation="horizontal" aria-label="Detail">
				<Tab ID="detail-tab-clip" Href={props.ClipHref} Panel="detail-panel-clip" Selected={props.ClipShown}>Clip</Tab>
				<Tab ID="detail-tab-device" Href={props.DeviceHref} Panel="detail-panel-device" Selected={props.DeviceShown}>Device</Tab>
				<Tab ID="detail-tab-automation" Href={props.AutomationHref} Panel="detail-panel-automation" Selected={props.AutomationShown}>Automation</Tab>
			</nav>
			<div class="shell-scroll">{children}</div>
		</div>
	</section>
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
}

component PhoneLink(props: PhoneLinkProps) {
	return <>
		<If cond={props.Current}><a href={props.Href} aria-current="page">{children}</a></If>
		<If cond={props.Current == false}><a href={props.Href}>{children}</a></If>
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
		<PhoneLink Href={props.BrowserHref} Current={props.BrowserOn}>Browser</PhoneLink>
		<PhoneLink Href={props.CenterHref} Current={props.CenterOn}>Center</PhoneLink>
		<PhoneLink Href={props.DetailHref} Current={props.DetailOn}>Detail</PhoneLink>
		<PhoneLink Href={props.CodeHref} Current={props.CodeOn}>Code</PhoneLink>
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
}

component ActionButton(props: ActionButtonProps) {
	return <button type="submit" name={props.Name} value={props.Value}>{props.Label}</button>
}

type HiddenFieldProps struct {
	Name  string
	Value string
}

component HiddenField(props: HiddenFieldProps) {
	return <input type="hidden" name={props.Name} value={props.Value} />
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
