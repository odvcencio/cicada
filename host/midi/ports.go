package midi

import (
	"bufio"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

var localPortLine = regexp.MustCompile(`^port\s+([a-z_][a-z0-9_-]{0,63})\s*=\s*("(?:[^"\\]|\\.)*")\s*(?://.*|#.*)?$`)

// ReadLocalPorts parses cicada.local. It holds machine-specific names only;
// it is deliberately separate from a score and never enters an export bundle.
func ReadLocalPorts(r io.Reader) (map[string]string, error) {
	ports := map[string]string{}
	scanner := bufio.NewScanner(io.LimitReader(r, 65537))
	scanner.Buffer(make([]byte, 1024), 65537)
	bytes := 0
	for line := 1; scanner.Scan(); line++ {
		text := strings.TrimSpace(scanner.Text())
		bytes += len(scanner.Bytes()) + 1
		if bytes > 65536 {
			return nil, fmt.Errorf("cicada.local exceeds 64 KiB")
		}
		if text == "" || strings.HasPrefix(text, "//") || strings.HasPrefix(text, "#") {
			continue
		}
		parts := localPortLine.FindStringSubmatch(text)
		if parts == nil {
			return nil, fmt.Errorf("cicada.local line %d: expected port name = \"device\"", line)
		}
		device, err := strconv.Unquote(parts[2])
		if err != nil || !utf8.ValidString(device) || len(device) == 0 || len(device) > 512 || strings.IndexFunc(device, unicode.IsControl) >= 0 {
			return nil, fmt.Errorf("cicada.local line %d: invalid device name", line)
		}
		if _, exists := ports[parts[1]]; exists {
			return nil, fmt.Errorf("cicada.local line %d: duplicate port %s", line, parts[1])
		}
		ports[parts[1]] = device
		if len(ports) > 32 {
			return nil, fmt.Errorf("cicada.local exceeds 32 ports")
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("cicada.local: %w", err)
	}
	return ports, nil
}

// SetLocalPort updates a binding without discarding comments or other ports.
// A malformed existing file is left untouched and must be repaired first.
func SetLocalPort(data []byte, port, device string) ([]byte, error) {
	if _, err := ReadLocalPorts(strings.NewReader(string(data))); err != nil {
		return nil, err
	}
	line := "port " + port + " = " + strconv.Quote(device)
	if _, err := ReadLocalPorts(strings.NewReader(line + "\n")); err != nil {
		return nil, err
	}
	lines := strings.SplitAfter(string(data), "\n")
	found := false
	for i, existing := range lines {
		trimmed := strings.TrimSpace(existing)
		match := localPortLine.FindStringSubmatchIndex(trimmed)
		if match == nil || trimmed[match[2]:match[3]] != port {
			continue
		}
		offset := strings.Index(existing, trimmed)
		lines[i] = existing[:offset+match[4]] + strconv.Quote(device) + existing[offset+match[5]:]
		found = true
		break
	}
	updated := strings.Join(lines, "")
	if !found {
		if updated != "" && !strings.HasSuffix(updated, "\n") {
			updated += "\n"
		}
		updated += line + "\n"
	}
	if _, err := ReadLocalPorts(strings.NewReader(updated)); err != nil {
		return nil, err
	}
	return []byte(updated), nil
}
