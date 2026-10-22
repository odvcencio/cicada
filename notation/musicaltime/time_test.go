package musicaltime

import "testing"

func TestExactMusicalTime(t *testing.T) {
	for _, c := range []struct {
		input    string
		position bool
		want     int64
	}{
		{"@2.3.4", true, 6480}, {"0ticks", true, 0}, {"7ticks", true, 7}, {"1.5bars", false, 5760}, {"1/12", false, 320}, {"9007199254740991ticks", true, 9007199254740991},
	} {
		got, err := Parse(c.input, c.position)
		if err != nil || got != c.want {
			t.Errorf("Parse(%q,%v)=%d,%v; want %d", c.input, c.position, got, err, c.want)
		}
	}
	for _, c := range []struct {
		input    string
		position bool
	}{
		{"@0.1.1", true}, {"@1.5.1", true}, {"@1.1.5", true}, {"@1.1.1", false}, {"1/7", false}, {"1/8", true}, {"-1ticks", true}, {"0ticks", false}, {"0.5ticks", true}, {"9007199254740992ticks", true}, {"100000000000000000000000000000bars", true},
	} {
		if _, err := Parse(c.input, c.position); err == nil {
			t.Errorf("accepted %q", c.input)
		}
	}
}
