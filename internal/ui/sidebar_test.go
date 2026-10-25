package ui

import (
	"fmt"
	"testing"

	"github.com/mattermost/mattermost/server/public/model"

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

func TestStepChannelVisitsThreads(t *testing.T) {
	m := threadsTestModel(true)
	m.current = chanRef{"work", "c-t1"}

	m.stepChannel(1, false)
	if m.threads == nil || m.threads.team != "t2" {
		t.Fatalf("Alt+J from t1's last channel: threads = %+v, want t2's Threads", m.threads)
	}
	m.stepChannel(1, false)
	if m.threads != nil || m.current.channel != "c-t2" {
		t.Fatalf("Alt+J from Threads: current = %+v, threads = %+v", m.current, m.threads)
	}
	m.stepChannel(-1, false)
	if m.threads == nil || m.threads.team != "t2" {
		t.Fatalf("Alt+K back: threads = %+v", m.threads)
	}

	m.current, m.threads = chanRef{"work", "c-t1"}, nil
	m.stepChannel(-1, true)
	if m.threads == nil || m.threads.team != "t2" {
		t.Errorf("Alt+Shift+K should reach t2's Threads, which has unread replies: threads = %+v", m.threads)
	}
}

func TestSidebarScrollsToOpenThreads(t *testing.T) {
	m := threadsTestModel(true)
	var dms []*model.Channel
	for i := range 20 {
		dms = append(dms, &model.Channel{Id: fmt.Sprintf("dm%02d", i), Type: model.ChannelTypeGroup, DisplayName: fmt.Sprintf("dm%02d", i), LastPostAt: int64(i)})
	}
	m.servers["work"].AddTeam(&model.Team{Id: "t0", DisplayName: "A0"}, dms, nil)
	m.current = chanRef{"work", "dm00"}
	m.renderSidebar(30, 8)
	m.stepChannel(1, false)
	if m.threads == nil || m.threads.team != "t0" {
		t.Fatal("expected the threads list to be open")
	}

	m.renderSidebar(30, 8)
	rows := m.rows()
	want := m.threadsRowIndex(rows)
	for _, i := range m.sidebarDrawn {
		if i == want {
			return
		}
	}
	t.Errorf("Threads row %d not drawn; drew %v", want, m.sidebarDrawn)
}
