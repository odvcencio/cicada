//go:build js && wasm

package main

import (
	"encoding/json"
	"fmt"
	"m31labs.dev/cicada/musictime"
	"math"
	"strconv"
	"strings"
	"syscall/js"

	enginewasm "m31labs.dev/gosx/engine/wasm"
)

type curveEditorProps struct {
	Path, Unit, Scale string
	Min, Max          float64
	End               int64
	Points            []curveEditorPoint
}
type curveEditor struct {
	root, svg       js.Value
	props           curveEditorProps
	points          []curveEditorPoint
	selected        int
	oldAt           string
	bindings        []vocalBinding
	drag            *curveEditorPoint
	pointer         int
	closed, writing bool
}

func mountAutomationCurve(host enginewasm.Context) (enginewasm.Handle, error) {
	var p curveEditorProps
	if err := host.DecodeProps(&p); err != nil {
		return nil, err
	}
	u := &curveEditor{root: host.Mount(), props: p, selected: -1}
	u.svg = u.root.Call("querySelector", "[data-automation-curve]")
	if !workspacePresent(u.svg) || p.End <= 0 || p.Max <= p.Min {
		return nil, fmt.Errorf("automation curve is unavailable")
	}
	u.reset(p)
	u.listen(u.root, "dblclick", func(e js.Value) {
		if !u.busy() && workspacePresent(e.Get("target").Call("closest", "[data-automation-curve]")) {
			e.Call("preventDefault")
			u.newPoint(u.tick(e), u.value(e))
		}
	})
	u.listen(u.root, "pointerdown", u.down)
	u.listen(u.root, "pointermove", u.move)
	u.listen(u.root, "pointerup", u.up)
	u.listen(u.root, "pointercancel", func(e js.Value) { u.cancelDrag() })
	u.listen(u.root, "keydown", u.key)
	u.listen(u.root, "click", func(e js.Value) {
		if workspacePresent(e.Get("target").Call("closest", "[data-automation-new]")) && !u.busy() {
			at := int64(0)
			for _, p := range u.points {
				if p.Tick == at {
					at += 240
				} else if p.Tick > at {
					break
				}
			}
			if at > u.props.End {
				u.root.Call("querySelector", "[data-automation-curve-status]").Set("textContent", "No unused grid position remains. Select an existing point to edit it.")
				return
			}
			u.newPoint(at, u.props.Min)
			workspaceField(u.root, "at").Call("focus")
		}
	})
	u.listen(u.root, "input", func(e js.Value) {
		if !u.writing && !u.busy() {
			u.fromFields()
		}
	})
	u.listen(u.root, "change", func(e js.Value) {
		if !u.writing && !u.busy() {
			u.fromFields()
		}
	})
	u.listen(js.Global().Get("document"), "cicada:workspace-updated", func(e js.Value) {
		if !u.root.Get("isConnected").Bool() {
			return
		}
		data := u.root.Call("querySelector", "[data-automation-curve-props]")
		var p curveEditorProps
		if workspacePresent(data) && json.Unmarshal([]byte(workspaceAttr(data, "data-automation-curve-props")), &p) == nil && p.End > 0 {
			u.svg = u.root.Call("querySelector", "[data-automation-curve]")
			if sameCurveProps(p, u.props) {
				u.draw()
				workspaceField(u.root, "oldAt").Set("value", u.oldAt)
				u.root.Call("querySelector", "[value=automation-point-remove]").Set("disabled", u.oldAt == "")
			} else {
				u.reset(p)
			}
			u.root.Call("setAttribute", "data-automation-ready", "true")
		}
	})
	u.root.Call("setAttribute", "data-automation-ready", "true")
	// The draft is the editor's points. Gestures, arrow keys and field edits
	// all change them without a form edit event, so compare the points
	// themselves with what the server rendered. A confirmed save re-renders the
	// props and resets both; a discarded drag restores the point.
	releaseUnsaved := holdUnsavedWork(fmt.Sprintf("automation-curve:%s:%p", p.Path, u), func() bool {
		return !u.closed && curvePointsDiffer(u.points, u.props.Points)
	})
	return enginewasm.HandleFunc(func() {
		releaseUnsaved()
		u.closed = true
		for _, b := range u.bindings {
			b.node.Call("removeEventListener", b.event, b.fn)
			b.fn.Release()
		}
	}), nil
}
func sameCurveProps(a, b curveEditorProps) bool {
	if a.Path != b.Path || a.Unit != b.Unit || a.Scale != b.Scale || a.Min != b.Min || a.Max != b.Max || a.End != b.End || len(a.Points) != len(b.Points) {
		return false
	}
	for i := range a.Points {
		if a.Points[i] != b.Points[i] {
			return false
		}
	}
	return true
}
func (u *curveEditor) listen(node js.Value, event string, fn func(js.Value)) {
	f := js.FuncOf(func(_ js.Value, args []js.Value) any {
		if !u.closed && len(args) > 0 {
			fn(args[0])
		}
		return nil
	})
	node.Call("addEventListener", event, f)
	u.bindings = append(u.bindings, vocalBinding{node, event, f})
}
func (u *curveEditor) busy() bool {
	root := js.Global().Get("document").Call("getElementById", "cicada-workspace")
	return workspaceAttr(root, "aria-busy") == "true" || nativeCapture.Get().State.Active != ""
}
func (u *curveEditor) reset(p curveEditorProps) {
	u.drag = nil
	u.props = p
	u.points = append([]curveEditorPoint(nil), p.Points...)
	for i := range u.points {
		u.points[i].OriginalAt = vocalPos(u.points[i].Tick)
	}
	u.selected = -1
	u.oldAt = ""
	if len(u.points) > 0 {
		u.selected = 0
		u.oldAt = u.points[0].OriginalAt
	}
	u.draw()
	u.toFields()
}
func (u *curveEditor) control(v float64) float64 {
	if u.props.Scale == "log" && u.props.Min > 0 {
		return math.Log(max(u.props.Min, v))
	}
	return v
}
func (u *curveEditor) inverse(v float64) float64 {
	if u.props.Scale == "log" && u.props.Min > 0 {
		return math.Exp(v)
	}
	return v
}
func (u *curveEditor) x(t int64) float64 { return 16 + 968*float64(t)/float64(u.props.End) }
func (u *curveEditor) y(v float64) float64 {
	return 224 - 208*(u.control(v)-u.control(u.props.Min))/(u.control(u.props.Max)-u.control(u.props.Min))
}
func (u *curveEditor) tick(e js.Value) int64 {
	b := u.svg.Call("getBoundingClientRect")
	x := (e.Get("clientX").Float() - b.Get("left").Float()) / max(1., b.Get("width").Float()) * 1000
	return max(0, min(u.props.End, int64(math.Round((x-16)/968*float64(u.props.End)/240))*240))
}
func (u *curveEditor) value(e js.Value) float64 {
	b := u.svg.Call("getBoundingClientRect")
	y := (e.Get("clientY").Float() - b.Get("top").Float()) / max(1., b.Get("height").Float()) * 240
	fraction := max(0., min(1., (224-y)/208))
	v := u.inverse(u.control(u.props.Min) + fraction*(u.control(u.props.Max)-u.control(u.props.Min)))
	return math.Round(v*10000) / 10000
}
func (u *curveEditor) draw() {
	line := u.root.Call("querySelector", "[data-automation-line]")
	if line.Get("namespaceURI").String() != "http://www.w3.org/2000/svg" {
		fixed := js.Global().Get("document").Call("createElementNS", "http://www.w3.org/2000/svg", "path")
		fixed.Call("setAttribute", "data-automation-line", "")
		line.Get("parentNode").Call("replaceChild", fixed, line)
		line = fixed
	}
	var path strings.Builder
	for i, p := range u.points {
		if i == 0 {
			fmt.Fprintf(&path, "M%.3f %.3f", u.x(p.Tick), u.y(p.Value))
			continue
		}
		a := u.points[i-1]
		if p.Shape == "step" {
			fmt.Fprintf(&path, "L%.3f %.3fL%.3f %.3f", u.x(p.Tick), u.y(a.Value), u.x(p.Tick), u.y(p.Value))
			continue
		}
		for n := 1; n <= 32; n++ {
			t := float64(n) / 32
			switch p.Shape {
			case "smooth":
				t = t * t * (3 - 2*t)
			case "curve":
				t = math.Pow(t, math.Exp2(p.Curve))
			}
			v := u.inverse(u.control(a.Value) + (u.control(p.Value)-u.control(a.Value))*t)
			tick := a.Tick + (p.Tick-a.Tick)*int64(n)/32
			fmt.Fprintf(&path, "L%.3f %.3f", u.x(tick), u.y(v))
		}
	}
	if len(u.points) > 0 {
		p := u.points[len(u.points)-1]
		fmt.Fprintf(&path, "L%.3f %.3f", u.x(u.props.End), u.y(p.Value))
	}
	line.Call("setAttribute", "d", path.String())
	group := u.root.Call("querySelector", "[data-automation-points]")
	if group.Get("namespaceURI").String() != "http://www.w3.org/2000/svg" {
		fixed := js.Global().Get("document").Call("createElementNS", "http://www.w3.org/2000/svg", "g")
		fixed.Call("setAttribute", "data-automation-points", "")
		group.Get("parentNode").Call("replaceChild", fixed, group)
		group = fixed
	}
	group.Call("replaceChildren")
	for i, p := range u.points {
		node := js.Global().Get("document").Call("createElementNS", "http://www.w3.org/2000/svg", "circle")
		for k, v := range map[string]string{"cx": fmt.Sprint(u.x(p.Tick)), "cy": fmt.Sprint(u.y(p.Value)), "r": "7", "tabindex": "0", "role": "button", "data-curve-point": strconv.Itoa(i), "data-selected": fmt.Sprint(i == u.selected), "aria-label": fmt.Sprintf("Point %s, %g%s", vocalPos(p.Tick), p.Value, u.props.Unit)} {
			node.Call("setAttribute", k, v)
		}
		group.Call("appendChild", node)
	}
}
func (u *curveEditor) toFields() {
	u.root.Call("querySelector", "[value=automation-point-remove]").Set("disabled", u.oldAt == "")
	if u.selected < 0 || u.selected >= len(u.points) {
		return
	}
	u.writing = true
	defer func() { u.writing = false }()
	p := u.points[u.selected]
	for name, value := range map[string]string{"oldAt": u.oldAt, "at": vocalPos(p.Tick), "value": strconv.FormatFloat(p.Value, 'f', -1, 64) + u.props.Unit, "shape": p.Shape, "curve": strconv.FormatFloat(p.Curve, 'f', -1, 64)} {
		workspaceField(u.root, name).Set("value", value)
	}
	workspaceField(u.root, "curve").Set("disabled", p.Shape != "curve")
	u.root.Call("querySelector", "[data-automation-curve-status]").Set("textContent", fmt.Sprintf("%s · %g%s · %s. Save point to keep this edit.", vocalPos(p.Tick), p.Value, u.props.Unit, p.Shape))
}
func (u *curveEditor) fromFields() {
	if u.selected < 0 {
		return
	}
	at, err := musictime.MusicalTicks(workspaceFieldValue(u.root, "at"), true)
	value := strings.TrimSuffix(workspaceFieldValue(u.root, "value"), u.props.Unit)
	v, e := strconv.ParseFloat(value, 64)
	if err != nil || e != nil || math.IsNaN(v) || math.IsInf(v, 0) || v < u.props.Min || v > u.props.Max {
		return
	}
	p := &u.points[u.selected]
	if at < 0 || at > u.props.End {
		return
	}
	p.Tick, p.Value = at, v
	p.Shape = workspaceFieldValue(u.root, "shape")
	p.Curve = 0
	if p.Shape == "curve" {
		p.Curve, _ = strconv.ParseFloat(workspaceFieldValue(u.root, "curve"), 64)
	}
	workspaceField(u.root, "curve").Set("disabled", p.Shape != "curve")
	u.draw()
}
func (u *curveEditor) newPoint(t int64, v float64) {
	for i, p := range u.points {
		if p.Tick == t {
			u.selected = i
			u.oldAt = p.OriginalAt
			u.toFields()
			u.draw()
			return
		}
	}
	i := 0
	for i < len(u.points) && u.points[i].Tick < t {
		i++
	}
	p := curveEditorPoint{Tick: t, Value: v, Shape: "linear"}
	u.points = append(u.points, curveEditorPoint{})
	copy(u.points[i+1:], u.points[i:])
	u.points[i] = p
	u.selected = i
	u.oldAt = ""
	u.toFields()
	u.draw()
}
func (u *curveEditor) down(e js.Value) {
	if u.busy() || e.Get("button").Int() != 0 {
		return
	}
	target := e.Get("target").Call("closest", "[data-curve-point]")
	if !workspacePresent(target) {
		return
	}
	i, _ := strconv.Atoi(workspaceAttr(target, "data-curve-point"))
	if i < 0 || i >= len(u.points) {
		return
	}
	e.Call("preventDefault")
	u.selected = i
	u.oldAt = u.points[i].OriginalAt
	before := u.points[i]
	u.drag = &before
	u.pointer = e.Get("pointerId").Int()
	u.svg.Call("setPointerCapture", u.pointer)
	u.toFields()
	u.draw()
}
func (u *curveEditor) move(e js.Value) {
	if u.drag == nil || e.Get("pointerId").Int() != u.pointer {
		return
	}
	e.Call("preventDefault")
	p := &u.points[u.selected]
	lo, hi := int64(0), u.props.End
	if u.selected > 0 {
		lo = u.points[u.selected-1].Tick + 240
	}
	if u.selected+1 < len(u.points) {
		hi = u.points[u.selected+1].Tick - 240
	}
	p.Tick = max(lo, min(hi, u.tick(e)))
	p.Value = u.value(e)
	u.toFields()
	u.draw()
}
func (u *curveEditor) up(e js.Value) {
	if u.drag == nil {
		return
	}
	u.move(e)
	u.drag = nil
	if u.svg.Call("hasPointerCapture", u.pointer).Bool() {
		u.svg.Call("releasePointerCapture", u.pointer)
	}
}
func (u *curveEditor) cancelDrag() {
	if u.drag != nil {
		u.points[u.selected] = *u.drag
		u.drag = nil
		u.toFields()
		u.draw()
	}
}
func (u *curveEditor) key(e js.Value) {
	key := e.Get("key").String()
	if key == "Escape" && u.drag != nil {
		e.Call("preventDefault")
		u.cancelDrag()
		return
	}
	node := e.Get("target").Call("closest", "[data-curve-point]")
	if !workspacePresent(node) || u.busy() {
		return
	}
	i, _ := strconv.Atoi(workspaceAttr(node, "data-curve-point"))
	if i < 0 || i >= len(u.points) {
		return
	}
	if key != "ArrowLeft" && key != "ArrowRight" && key != "ArrowUp" && key != "ArrowDown" && key != "Enter" {
		return
	}
	e.Call("preventDefault")
	u.selected = i
	u.oldAt = u.points[i].OriginalAt
	p := &u.points[i]
	step := int64(240)
	amount := .005
	if e.Get("shiftKey").Bool() {
		step *= 4
		amount *= 10
	}
	lo, hi := int64(0), u.props.End
	if i > 0 {
		lo = u.points[i-1].Tick + 240
	}
	if i+1 < len(u.points) {
		hi = u.points[i+1].Tick - 240
	}
	switch key {
	case "ArrowLeft":
		p.Tick = max(lo, p.Tick-step)
	case "ArrowRight":
		p.Tick = min(hi, p.Tick+step)
	case "ArrowUp":
		p.Value = u.inverse(min(u.control(u.props.Max), u.control(p.Value)+amount*(u.control(u.props.Max)-u.control(u.props.Min))))
	case "ArrowDown":
		p.Value = u.inverse(max(u.control(u.props.Min), u.control(p.Value)-amount*(u.control(u.props.Max)-u.control(u.props.Min))))
	}
	u.toFields()
	u.draw()
	if key != "Enter" {
		focus := u.root.Call("querySelector", `[data-curve-point="`+strconv.Itoa(i)+`"]`)
		focus.Call("focus")
	} else {
		workspaceField(u.root, "value").Call("focus")
	}
}
