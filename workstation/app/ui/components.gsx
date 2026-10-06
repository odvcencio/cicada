package ui

type ShellProps struct {
	Title string
	Filename string
	Tempo string
	Key string
	Message string
	HasMessage bool
}

component Shell(props: ShellProps) {
	return <div class="studio">
		<a class="skip" href="#workspace">Skip to workspace</a>
		<header class="masthead">
			<a class="brand" href="/">CICADA <span>STUDIO</span></a>
			<div class="score-title"><h1>{props.Title}</h1><span class="muted">{props.Filename}</span></div>
			<div class="score-meta"><span>{props.Tempo} BPM</span><span>{props.Key}</span><span class="engine-label">TYMBAL</span></div>
		</header>
		<If cond={props.HasMessage}><p class="notice" role="status">{props.Message}</p></If>
		<div class="notices" data-gosx-toast-host role="status" aria-live="polite"></div>
		{children}
		<footer>Score first. Sound follows.</footer>
	</div>
}

type PanelProps struct { ID string; Title string; Description string }

component Panel(props: PanelProps) {
	return <section class="panel" id={props.ID} aria-label={props.Title}>
		<div class="panel-heading"><h2>{props.Title}</h2><p class="muted">{props.Description}</p></div>
		{children}
	</section>
}

type FormProps struct { Action string; CSRF string; Revision string; ReturnTo string; Class string }

component Form(props: FormProps) {
	return <form method="post" action={props.Action} data-gosx-managed class={props.Class}>
		<input type="hidden" name="csrf_token" value={props.CSRF} />
		<input type="hidden" name="revision" value={props.Revision} />
		<input type="hidden" name="__gosx_return_to" value={props.ReturnTo} />
		{children}
	</form>
}

type StepProps struct {
	Label string
	Text string
	Active bool
	Index int
	Pattern string
	Lane string
}

component Step(props: StepProps) {
	return <button class="step" type="submit" name="step" value={props.Index}
		aria-label={props.Label} aria-pressed={props.Active} data-active={props.Active}
		data-pattern={props.Pattern} data-lane={props.Lane}>{props.Text}</button>
}

type TransportProps struct { Playing bool; Bar int64; Step int64; Backend string; Error string }

component Position(props: TransportProps) {
	return <div class="position" data-gosx-live-src="/api/transport" data-gosx-live-interval="1s">
		<span class="position-label">BAR <b data-gosx-live-bind="bar">{props.Bar}</b></span>
		<span class="position-label">STEP <b data-gosx-live-bind="step">{props.Step}</b></span>
		<span class="muted" data-gosx-live-bind="activeBackend">{props.Backend}</span>
		<span class="error" data-gosx-live-bind="error">{props.Error}</span>
	</div>
}
