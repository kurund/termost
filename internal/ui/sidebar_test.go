package ui

import (
	"fmt"
	"testing"

	"github.com/mattermost/mattermost/server/public/model"

	"github.com/kurund/termost/internal/mm"
	"github.com/kurund/termost/internal/store"
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

func TestRowsHideTeamRowForSingleTeam(t *testing.T) {
	kinds := func(m *Model) map[rowKind]int {
		out := map[rowKind]int{}
		for _, r := range m.rows() {
			out[r.kind]++
		}
		return out
	}

	m := threadsTestModel(true)
	if got := kinds(m)[rowTeam]; got != 2 {
		t.Errorf("two teams: %d team rows, want 2", got)
	}

	st := store.NewServer("work", &model.User{Id: "me", Username: "me"})
	ch := &model.Channel{Id: "c1", TeamId: "t1", Name: "town-square", DisplayName: "Town Square", Type: model.ChannelTypeOpen}
	st.AddTeam(&model.Team{Id: "t1", DisplayName: "Acme"}, []*model.Channel{ch}, nil)
	st.CRT = true
	m.servers["work"] = st
	m.collapsed[st.Name+"/t1"] = true

	if got := kinds(m)[rowTeam]; got != 0 {
		t.Errorf("single team: %d team rows, want 0", got)
	}
	for _, r := range m.rows() {
		switch {
		case r.kind == rowThreads && r.depth != 0, r.kind == rowCategory && r.depth != 0:
			t.Errorf("single team: %v row at depth %d, want 0", r.kind, r.depth)
		case r.kind == rowChannel && r.channel == "c1" && r.depth != 1:
			t.Errorf("single team: channel at depth %d, want 1", r.depth)
		}
	}
	if m.rowIndex(chanRef{"work", "c1"}) < 0 {
		t.Error("a team collapsed before it became the only one still hides its channels")
	}
}
