package language

import (
	"math"
	"strconv"
	"strings"
)

// Color is a 24-bit color. The zero Color is unset.
type Color struct {
	R, G, B uint8
	Set     bool
}

func rgb(hex uint32) Color {
	return Color{R: uint8(hex >> 16), G: uint8(hex >> 8), B: uint8(hex), Set: true}
}

// Style is how a span looks. A nested span is drawn over the spans around
// it: its colors replace theirs and its attributes add to theirs.
type Style struct {
	Foreground, Background         Color
	Bold, Italic, Underline, Faint bool
}

func (s Style) over(base Style) Style {
	if s.Foreground.Set {
		base.Foreground = s.Foreground
	}
	if s.Background.Set {
		base.Background = s.Background
	}
	base.Bold = base.Bold || s.Bold
	base.Italic = base.Italic || s.Italic
	base.Underline = base.Underline || s.Underline
	base.Faint = base.Faint || s.Faint
	return base
}

// Theme maps capture names to styles. A capture without a style of its own
// falls back through its dotted prefixes as editors do: constant.pitch.degree,
// then constant.pitch, then constant. Tint may recolor a span from its text.
type Theme struct {
	Name       string
	Background Color
	Foreground Color
	Styles     map[string]Style
	Tint       func(capture, text string) (Color, bool)
}

// Style returns the style of a capture over text.
func (t Theme) Style(capture, text string) Style {
	var style Style
	for name := capture; name != ""; {
		if s, ok := t.Styles[name]; ok {
			style = s
			break
		}
		dot := strings.LastIndexByte(name, '.')
		if dot < 0 {
			break
		}
		name = name[:dot]
	}
	if t.Tint != nil {
		if c, ok := t.Tint(capture, text); ok {
			style.Foreground = c
		}
	}
	return style
}

// Night is Cicada's theme: a summer night with amber keywords, pitches on the
// circle of fifths, and drum hits that glow brighter as they play louder.
var Night = Theme{
	Name:       "cicada-night",
	Background: rgb(0x0f1411),
	Foreground: rgb(0xd7dccf),
	Tint:       musicalTint,
	Styles: map[string]Style{
		"comment": {Foreground: rgb(0x66705f), Italic: true},

		"keyword":                  {Foreground: rgb(0xe8b04b)},
		"keyword.directive":        {Foreground: rgb(0x9be15d), Bold: true},
		"keyword.directive.define": {Foreground: rgb(0xc8a2f8)},
		"keyword.import":           {Foreground: rgb(0xc8a2f8)},
		"keyword.return":           {Foreground: rgb(0xff7ab6), Bold: true},
		"keyword.modifier":         {Foreground: rgb(0xe8b04b), Italic: true},

		"type":            {Foreground: rgb(0xf2d06b)},
		"type.definition": {Foreground: rgb(0xf2d06b), Bold: true},
		"type.builtin":    {Foreground: rgb(0xf2d06b), Italic: true},

		"function":         {Foreground: rgb(0x78a9ff)},
		"function.macro":   {Foreground: rgb(0xc8a2f8)},
		"function.builtin": {Foreground: rgb(0x4fd6be)},

		"variable":           {Foreground: rgb(0xd7dccf)},
		"variable.builtin":   {Foreground: rgb(0xff9e64), Italic: true},
		"variable.parameter": {Foreground: rgb(0xffc777)},
		"variable.member":    {Foreground: rgb(0x7dcfff)},
		"property":           {Foreground: rgb(0x9fc5b5)},
		"attribute":          {Foreground: rgb(0x8fbcbb)},
		"attribute.builtin":  {Foreground: rgb(0x8fbcbb), Italic: true},
		"label":              {Foreground: rgb(0xff8f5a), Bold: true},
		"tag":                {Foreground: rgb(0xa77b77)},
		"tag.builtin":        {Foreground: rgb(0xff6b6b), Bold: true},

		"constant":            {Foreground: rgb(0xff9e64)},
		"constant.builtin":    {Foreground: rgb(0xff9e64), Italic: true},
		"boolean":             {Foreground: rgb(0xff9e64)},
		"constant.pitch":      {Foreground: rgb(0xf0c75e)},
		"constant.hit":        {Foreground: rgb(0xff7b45)},
		"constant.hit.accent": {Foreground: rgb(0xffb35c), Bold: true},

		"number":             {Foreground: rgb(0xe9a26f)},
		"number.frequency":   {Foreground: rgb(0x89ddff)},
		"number.duration":    {Foreground: rgb(0xa6d97a)},
		"number.decibel":     {Foreground: rgb(0xf5d67b)},
		"number.percent":     {Foreground: rgb(0xd4a5ff)},
		"number.fraction":    {Foreground: rgb(0xa6d97a)},
		"number.version":     {Foreground: rgb(0x9be15d), Bold: true},
		"number.seed":        {Foreground: rgb(0xd4a5ff)},
		"number.bars":        {Foreground: rgb(0xff8f5a)},
		"number.repeat":      {Foreground: rgb(0xc8a2f8)},
		"number.ratchet":     {Foreground: rgb(0xffd166), Underline: true},
		"number.probability": {Foreground: rgb(0xc9a7ff)},

		"operator":             {Foreground: rgb(0x97a8a4)},
		"operator.accent":      {Foreground: rgb(0xff4f7b), Bold: true},
		"operator.slide":       {Foreground: rgb(0x56d8ff), Italic: true},
		"operator.ratchet":     {Foreground: rgb(0xffd166), Underline: true},
		"operator.probability": {Foreground: rgb(0x9c86c9)},
		"operator.octave.up":   {Foreground: rgb(0xd6f5a8)},
		"operator.octave.down": {Foreground: rgb(0x6f9f6a)},
		"operator.repeat":      {Foreground: rgb(0xa8b3a0)},

		"punctuation":               {Foreground: rgb(0x75806f)},
		"punctuation.delimiter.bar": {Foreground: rgb(0xb8c2ae), Bold: true},
		"punctuation.special.rest":  {Foreground: rgb(0x46514a)},
		"punctuation.special.tie":   {Foreground: rgb(0x8a9585)},

		"string":           {Foreground: rgb(0xa6d97a)},
		"markup.heading":   {Foreground: rgb(0xfff3d6), Bold: true, Underline: true},
		"markup.strong":    {Bold: true},
		"markup.italic":    {Italic: true},
		"markup.underline": {Underline: true},

		ErrorCapture: {Background: rgb(0x5c1e24), Underline: true},
	},
}

// musicalTint colors notes by pitch, drum hits by velocity, and step
// probabilities by chance.
func musicalTint(capture, text string) (Color, bool) {
	switch capture {
	case "constant.pitch.degree":
		return degreeColor(text)
	case "constant.pitch.letter", "constant.pitch.root":
		return letterColor(text)
	case "constant.hit":
		return velocityColor(100), true
	case "constant.hit.accent":
		return velocityColor(127), true
	case "constant.hit.velocity":
		if len(text) == 2 && text[1] >= '1' && text[1] <= '9' {
			return velocityColor(int(text[1]-'0') * 14), true // x1-x9 play 14-126
		}
	case "number.probability":
		if percent, err := strconv.Atoi(text); err == nil && percent >= 0 && percent <= 100 {
			return blend(rgb(0x3d3650), rgb(0xd9b8ff), float64(percent)/100), true
		}
	}
	return Color{}, false
}

// degreeFifths places each scale degree on the circle of fifths: the tonic
// at zero, the dominant one step clockwise, the subdominant one step back.
var degreeFifths = [8]float64{1: 0, 2: 2, 3: 4, 4: -1, 5: 1, 6: 3, 7: 5}

// degreeColor spreads the seven degrees around the hue wheel in fifths
// order. The tonic is gold and its closest relatives sit beside it.
func degreeColor(text string) (Color, bool) {
	if text == "" || text[0] < '1' || text[0] > '7' {
		return Color{}, false
	}
	hue := 45 + degreeFifths[text[0]-'0']*360/7
	if strings.HasSuffix(text, "#") {
		hue += 18
	} else if strings.HasSuffix(text, "b") {
		hue -= 18
	}
	return hsl(hue, 0.8, 0.66), true
}

var naturalSemitone = map[byte]int{'c': 0, 'd': 2, 'e': 4, 'f': 5, 'g': 7, 'a': 9, 'b': 11}

// letterColor gives a letter pitch the hue of its pitch class on the circle
// of fifths, with C gold like a tonic, and lightens it by octave. A pitch
// without an octave plays in octave 2.
func letterColor(text string) (Color, bool) {
	if text == "" {
		return Color{}, false
	}
	semitone, ok := naturalSemitone[text[0]]
	if !ok {
		return Color{}, false
	}
	rest := text[1:]
	if strings.HasPrefix(rest, "#") {
		semitone, rest = semitone+1, rest[1:]
	} else if strings.HasPrefix(rest, "b") {
		semitone, rest = semitone-1, rest[1:]
	}
	octave := 2
	if len(rest) == 1 && rest[0] >= '0' && rest[0] <= '6' {
		octave = int(rest[0] - '0')
	}
	fifth := (semitone + 12) % 12 * 7 % 12
	return hsl(45+float64(fifth)*30, 0.8, 0.54+0.04*float64(octave)), true
}

// velocityColor runs from a dim ember at silence to a bright flame at 127.
func velocityColor(velocity int) Color {
	return blend(rgb(0x4a1f17), rgb(0xff9a4d), float64(velocity)/127)
}

func blend(a, b Color, t float64) Color {
	mix := func(x, y uint8) uint8 { return uint8(math.Round(float64(x) + (float64(y)-float64(x))*t)) }
	return Color{R: mix(a.R, b.R), G: mix(a.G, b.G), B: mix(a.B, b.B), Set: true}
}

func hsl(hue, saturation, lightness float64) Color {
	hue = math.Mod(hue, 360)
	if hue < 0 {
		hue += 360
	}
	chroma := (1 - math.Abs(2*lightness-1)) * saturation
	x := chroma * (1 - math.Abs(math.Mod(hue/60, 2)-1))
	var r, g, b float64
	switch {
	case hue < 60:
		r, g = chroma, x
	case hue < 120:
		r, g = x, chroma
	case hue < 180:
		g, b = chroma, x
	case hue < 240:
		g, b = x, chroma
	case hue < 300:
		r, b = x, chroma
	default:
		r, b = chroma, x
	}
	m := lightness - chroma/2
	channel := func(v float64) uint8 { return uint8(math.Round((v + m) * 255)) }
	return Color{R: channel(r), G: channel(g), B: channel(b), Set: true}
}
