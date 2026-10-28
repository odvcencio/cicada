package project

// encoding/json reports syntax-error offsets that differ between Go versions:
// Decoder.Token and Unmarshal disagree with each other, and Go 1.27 moves some
// offsets that Go 1.26 reports. Diagnostics must point at the same byte on
// every toolchain, so they find the error here instead of reading it from the
// standard library. The scanner accepts the same text as json.Valid; it is only
// consulted after the decoder has already rejected the document.

// jsonScanDepth bounds the scanner's recursion. Project JSON is limited to
// maxJSONDepth long before a deeper syntax error could be reported.
const jsonScanDepth = 1 << 12

// jsonSyntaxOffset returns the zero-based offset of the first byte that keeps
// data from being a JSON text, or len(data) when the text ends too soon. It
// reports false when data is a complete JSON text or nests too deeply to scan.
func jsonSyntaxOffset(data []byte) (offset int, found bool) {
	s := jsonScanner{data: data}
	if s.value(0) {
		s.space()
		if s.pos == len(data) {
			return 0, false
		}
	}
	return s.pos, !s.deep
}

type jsonScanner struct {
	data []byte
	// pos is the offset of the next unread byte. After a failed scan it is the
	// offset of the byte that cannot continue the text, or len(data).
	pos  int
	deep bool // nesting beyond jsonScanDepth stopped the scan
}

func (s *jsonScanner) at(b byte) bool { return s.pos < len(s.data) && s.data[s.pos] == b }

func (s *jsonScanner) space() {
	for s.pos < len(s.data) {
		switch s.data[s.pos] {
		case ' ', '\t', '\n', '\r':
			s.pos++
		default:
			return
		}
	}
}

// value scans one value, including the whitespace before it.
func (s *jsonScanner) value(depth int) bool {
	if depth > jsonScanDepth {
		s.deep = true
		return false
	}
	s.space()
	if s.pos >= len(s.data) {
		return false
	}
	switch c := s.data[s.pos]; {
	case c == '{':
		return s.object(depth)
	case c == '[':
		return s.array(depth)
	case c == '"':
		return s.str()
	case c == 't':
		return s.keyword("true")
	case c == 'f':
		return s.keyword("false")
	case c == 'n':
		return s.keyword("null")
	case c == '-' || '0' <= c && c <= '9':
		return s.number()
	}
	return false
}

func (s *jsonScanner) object(depth int) bool {
	s.pos++ // {
	s.space()
	if s.at('}') {
		s.pos++
		return true
	}
	for {
		s.space()
		if !s.at('"') || !s.str() {
			return false
		}
		s.space()
		if !s.at(':') {
			return false
		}
		s.pos++
		if !s.value(depth + 1) {
			return false
		}
		if more, ok := s.next('}'); !ok || !more {
			return ok
		}
	}
}

func (s *jsonScanner) array(depth int) bool {
	s.pos++ // [
	s.space()
	if s.at(']') {
		s.pos++
		return true
	}
	for {
		if !s.value(depth + 1) {
			return false
		}
		if more, ok := s.next(']'); !ok || !more {
			return ok
		}
	}
}

// next consumes the comma or closing bracket after a member or element. more
// reports that another member or element follows; ok is false when the next
// byte is neither.
func (s *jsonScanner) next(closer byte) (more, ok bool) {
	s.space()
	switch {
	case s.at(','):
		s.pos++
		return true, true
	case s.at(closer):
		s.pos++
		return false, true
	}
	return false, false
}

func (s *jsonScanner) keyword(word string) bool {
	for i := 0; i < len(word); i++ {
		if !s.at(word[i]) {
			return false
		}
		s.pos++
	}
	return true
}

func (s *jsonScanner) str() bool {
	s.pos++ // "
	for s.pos < len(s.data) {
		switch c := s.data[s.pos]; {
		case c == '"':
			s.pos++
			return true
		case c < 0x20:
			return false
		case c == '\\':
			s.pos++
			if s.at('u') {
				s.pos++
				for range 4 {
					if s.pos >= len(s.data) || !isJSONHex(s.data[s.pos]) {
						return false
					}
					s.pos++
				}
				continue
			}
			if s.pos >= len(s.data) || !isJSONEscape(s.data[s.pos]) {
				return false
			}
			s.pos++
		default:
			s.pos++
		}
	}
	return false
}

func (s *jsonScanner) number() bool {
	if s.at('-') {
		s.pos++
	}
	if s.at('0') {
		s.pos++
	} else if !s.digits() {
		return false
	}
	if s.at('.') {
		s.pos++
		if !s.digits() {
			return false
		}
	}
	if s.at('e') || s.at('E') {
		s.pos++
		if s.at('+') || s.at('-') {
			s.pos++
		}
		if !s.digits() {
			return false
		}
	}
	return true
}

// digits consumes one or more decimal digits.
func (s *jsonScanner) digits() bool {
	start := s.pos
	for s.pos < len(s.data) && '0' <= s.data[s.pos] && s.data[s.pos] <= '9' {
		s.pos++
	}
	return s.pos > start
}

func isJSONHex(c byte) bool {
	return '0' <= c && c <= '9' || 'a' <= c && c <= 'f' || 'A' <= c && c <= 'F'
}

func isJSONEscape(c byte) bool {
	switch c {
	case '"', '\\', '/', 'b', 'f', 'n', 'r', 't':
		return true
	}
	return false
}
