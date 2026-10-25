package ui

import (
	"fmt"
	"testing"

	"github.com/kurund/termost/internal/mm"
)

func TestSidebarWindowPinsServers(t *testing.T) {
	m := &Model{sessions: []*mm.Session{{}, {}}}

	got := fmt.Sprint(m.sidebarWindow(40, 10, 30))
	if want := "[0 1 2 24 25 26 27 28 29 30]"; got != want {
		t.Errorf("focus deep in the list = %s, want %s", got, want)
	}
	got = fmt.Sprint(m.sidebarWindow(40, 10, 1))
	if want := "[0 1 2 24 25 26 27 28 29 30]"; got != want {
		t.Errorf("focus on a server row moved the scroll: %s, want %s", got, want)
	}
	got = fmt.Sprint(m.sidebarWindow(40, 10, 5))
	if want := "[0 1 2 5 6 7 8 9 10 11]"; got != want {
		t.Errorf("scrolling back up = %s, want %s", got, want)
	}

	m.sidebarFree, m.sidebarOffset = true, 0
	got = fmt.Sprint(m.sidebarWindow(40, 10, 30))
	if want := "[0 1 2 3 4 5 6 7 8 9]"; got != want {
		t.Errorf("wheel scrolled to the top = %s, want %s", got, want)
	}

	m = &Model{sessions: []*mm.Session{{}, {}}}
	if got := fmt.Sprint(m.sidebarWindow(5, 10, 4)); got != "[0 1 2 3 4]" {
		t.Errorf("short list = %s", got)
	}
	if got := fmt.Sprint(m.sidebarWindow(40, 5, 30)); got != "[26 27 28 29 30]" {
		t.Errorf("too short to pin = %s", got)
	}
}
