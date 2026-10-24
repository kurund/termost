package ui

import (
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func TestLinkAt(t *testing.T) {
	line := "👍 see " + ansi.SetHyperlink("https://a.example") + "docs" + ansi.ResetHyperlink() +
		" and \x1b[34m" + ansi.SetHyperlink("https://b.example", "id=1") + "more" + ansi.ResetHyperlink() + "\x1b[m"
	cases := map[int]string{
		0:  "",
		6:  "",
		7:  "https://a.example",
		10: "https://a.example",
		11: "",
		16: "https://b.example",
		19: "https://b.example",
		20: "",
		99: "",
	}
	for x, want := range cases {
		if got := linkAt(line, x); got != want {
			t.Errorf("linkAt(x=%d) = %q, want %q", x, got, want)
		}
	}
	if got := oscLinkTarget("\x1b]8;;https://c.example\x1b\\"); got != "https://c.example" {
		t.Errorf("ST-terminated = %q", got)
	}
}

func TestZoneAt(t *testing.T) {
	m := &Model{zones: []zone{{x: 0, y: 0, w: 10, h: 5}, {x: 2, y: 1, w: 3, h: 1}}}
	if z := m.zoneAt(3, 1); z != &m.zones[1] {
		t.Error("later zones should win where they overlap")
	}
	if z := m.zoneAt(9, 4); z != &m.zones[0] {
		t.Error("expected the outer zone")
	}
	if m.zoneAt(10, 0) != nil || m.zoneAt(0, 5) != nil {
		t.Error("zones are half-open")
	}
}
