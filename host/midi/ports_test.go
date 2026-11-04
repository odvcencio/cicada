package midi

import (
	"strings"
	"testing"
)

func TestReadLocalPorts(t *testing.T) {
	ports, err := ReadLocalPorts(strings.NewReader("// machine settings\nport keys = \"KeyStep 37\"\nport pads = \"Launchpad\\\" Mini\" # desk\n"))
	if err != nil || ports["keys"] != "KeyStep 37" || ports["pads"] != "Launchpad\" Mini" {
		t.Fatalf("ports=%v err=%v", ports, err)
	}
	for _, bad := range []string{"port keys = \"one\"\nport keys = \"two\"", "port keys = \"\"", "port keys = \"bad\\nname\"", "not a mapping", strings.Repeat(" ", 65537)} {
		if _, err := ReadLocalPorts(strings.NewReader(bad)); err == nil {
			t.Fatalf("accepted %q", bad[:min(40, len(bad))])
		}
	}
}

func TestSetLocalPortPreservesOtherContent(t *testing.T) {
	original := []byte("# setup\r\n  port keys = \"Old\" # keyboard\r\nport pads = \"Pads\"\n")
	updated, err := SetLocalPort(original, "keys", `New "Keys"`)
	if err != nil {
		t.Fatal(err)
	}
	if string(updated) != "# setup\r\n  port keys = \"New \\\"Keys\\\"\" # keyboard\r\nport pads = \"Pads\"\n" {
		t.Fatalf("updated %q", updated)
	}
	added, err := SetLocalPort([]byte("# no newline"), "keys", "Keyboard")
	if err != nil || string(added) != "# no newline\nport keys = \"Keyboard\"\n" {
		t.Fatalf("added %q: %v", added, err)
	}
	for _, tc := range []struct{ content, port, device string }{
		{"bad input\n", "keys", "Keyboard"},
		{"", "invalid port", "Keyboard"},
		{"", "keys", ""},
		{"", "keys", "bad\nname"},
	} {
		if _, err := SetLocalPort([]byte(tc.content), tc.port, tc.device); err == nil {
			t.Errorf("accepted %+v", tc)
		}
	}
}
