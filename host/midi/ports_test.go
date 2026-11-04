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
