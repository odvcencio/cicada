package language

import (
	"bufio"
	"bytes"
	"fmt"
	"html"
	"io"
	"strings"
)

// run is a stretch of source drawn in one style.
type run struct {
	start, end int
	style      Style
}

// runs flattens nested spans into runs of one style. Each span is drawn over
// the spans that enclose it, so an accented note keeps its bold while its
// pitch takes a color of its own.
func runs(src []byte, spans []Span, theme Theme) []run {
	type open struct {
		end   int
		style Style
	}
	base := Style{Foreground: theme.Foreground}
	var stack []open
	var out []run
	next := 0
	for pos := 0; pos < len(src); {
		for len(stack) > 0 && stack[len(stack)-1].end <= pos {
			stack = stack[:len(stack)-1]
		}
		for next < len(spans) && spans[next].Start <= pos {
			span := spans[next]
			next++
			if span.End <= pos || span.End > len(src) {
				continue
			}
			under := base
			if len(stack) > 0 {
				under = stack[len(stack)-1].style
			}
			style := theme.Style(span.Capture, string(src[span.Start:span.End])).over(under)
			stack = append(stack, open{end: span.End, style: style})
		}
		end, style := len(src), base
		if len(stack) > 0 {
			end, style = min(end, stack[len(stack)-1].end), stack[len(stack)-1].style
		}
		if next < len(spans) && spans[next].Start < end {
			end = spans[next].Start
		}
		if n := len(out); n > 0 && out[n-1].style == style {
			out[n-1].end = end
		} else {
			out = append(out, run{start: pos, end: end, style: style})
		}
		pos = end
	}
	return out
}

// WriteANSI draws src with 24-bit ANSI color for a dark terminal.
func WriteANSI(w io.Writer, src []byte, spans []Span, theme Theme) error {
	out := bufio.NewWriter(w)
	for _, r := range runs(src, spans, theme) {
		sgr := r.style.sgr()
		for i, line := range bytes.Split(src[r.start:r.end], []byte("\n")) {
			if i > 0 {
				out.WriteByte('\n')
			}
			if len(line) == 0 {
				continue
			}
			if sgr == "" || (len(bytes.TrimSpace(line)) == 0 && !r.style.Background.Set && !r.style.Underline) {
				out.Write(line)
				continue
			}
			out.WriteString(sgr)
			out.Write(line)
			out.WriteString("\x1b[0m")
		}
	}
	return out.Flush()
}

func (s Style) sgr() string {
	var codes []string
	if s.Bold {
		codes = append(codes, "1")
	}
	if s.Faint {
		codes = append(codes, "2")
	}
	if s.Italic {
		codes = append(codes, "3")
	}
	if s.Underline {
		codes = append(codes, "4")
	}
	if c := s.Foreground; c.Set {
		codes = append(codes, fmt.Sprintf("38;2;%d;%d;%d", c.R, c.G, c.B))
	}
	if c := s.Background; c.Set {
		codes = append(codes, fmt.Sprintf("48;2;%d;%d;%d", c.R, c.G, c.B))
	}
	if len(codes) == 0 {
		return ""
	}
	return "\x1b[" + strings.Join(codes, ";") + "m"
}

// WriteHTML writes src as a standalone HTML page drawn in the theme.
func WriteHTML(w io.Writer, src []byte, spans []Span, theme Theme, title string) error {
	out := bufio.NewWriter(w)
	fmt.Fprintf(out, `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>%s</title>
<style>
:root { color-scheme: dark; }
body { margin: 0; background: %s; color: %s; }
pre { margin: 0; padding: 24px 16px; overflow-x: auto; font: 14px/1.55 ui-monospace, "SF Mono", Menlo, Consolas, monospace; tab-size: 2; }
</style>
</head>
<body>
<pre><code>`, html.EscapeString(title), theme.Background.css(), theme.Foreground.css())
	for _, r := range runs(src, spans, theme) {
		text := html.EscapeString(string(src[r.start:r.end]))
		if css := r.style.css(theme.Foreground); css != "" {
			fmt.Fprintf(out, `<span style="%s">%s</span>`, css, text)
		} else {
			out.WriteString(text)
		}
	}
	out.WriteString("</code></pre>\n</body>\n</html>\n")
	return out.Flush()
}

func (c Color) css() string {
	return fmt.Sprintf("#%02x%02x%02x", c.R, c.G, c.B)
}

func (s Style) css(foreground Color) string {
	var rules []string
	if s.Foreground.Set && s.Foreground != foreground {
		rules = append(rules, "color:"+s.Foreground.css())
	}
	if s.Background.Set {
		rules = append(rules, "background:"+s.Background.css())
	}
	if s.Bold {
		rules = append(rules, "font-weight:700")
	}
	if s.Italic {
		rules = append(rules, "font-style:italic")
	}
	if s.Underline {
		rules = append(rules, "text-decoration:underline")
	}
	if s.Faint {
		rules = append(rules, "opacity:.6")
	}
	return strings.Join(rules, ";")
}
