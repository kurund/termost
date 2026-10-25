package ui

import (
	"context"
	"strings"
	"testing"

	"github.com/mattermost/mattermost/server/public/model"

	"github.com/kurund/termost/internal/config"
	"github.com/kurund/termost/internal/mm"
	"github.com/kurund/termost/internal/store"
)

func TestFuzzyScore(t *testing.T) {
	if _, ok := fuzzyScore("xyz", "town-square"); ok {
		t.Error("non-subsequence matched")
	}
	if _, ok := fuzzyScore("TSQ", "town-square"); !ok {
		t.Error("case-insensitive subsequence did not match")
	}

	prefix, _ := fuzzyScore("dev", "Dev")
	scattered, _ := fuzzyScore("dev", "dead-ventures")
	if prefix <= scattered {
		t.Errorf("prefix %d should beat scattered %d", prefix, scattered)
	}

	short, _ := fuzzyScore("dev", "dev")
	long, _ := fuzzyScore("dev", "devops")
	if short <= long {
		t.Errorf("shorter %d should beat longer %d", short, long)
	}

	wordStart, _ := fuzzyScore("sq", "town-square")
	midWord, _ := fuzzyScore("sq", "bisque")
	if wordStart <= midWord {
		t.Errorf("word start %d should beat mid-word %d", wordStart, midWord)
	}
}

func TestFilterItems(t *testing.T) {
	items := []switchItem{
		{name: "old", lastPost: 1},
		{name: "recent", lastPost: 9},
		{name: "unread", unread: 2, lastPost: 2},
		{name: "mentioned", unread: 1, mentions: 1, lastPost: 3},
		{name: "muted", unread: 5, muted: true, lastPost: 8},
		{name: "Town Square", slug: "town-square"},
	}

	got := names(filterItems(items, ""))
	want := "mentioned,unread,recent,muted,old,Town Square"
	if got != want {
		t.Errorf("empty query = %s\nwant          %s", got, want)
	}

	if got := names(filterItems(items, "town-sq")); got != "Town Square" {
		t.Errorf("slug match = %s", got)
	}
	if got := filterItems(items, "rec"); len(got) == 0 || got[0].name != "recent" {
		t.Errorf("query rec = %s, want recent first", names(got))
	}
	if got := filterItems(items, "zzz"); len(got) != 0 {
		t.Errorf("query zzz = %s, want none", names(got))
	}
}

func names(items []switchItem) string {
	var out string
	for i, it := range items {
		if i > 0 {
			out += ","
		}
		out += it.name
	}
	return out
}

func threadsTestModel(crt bool) *Model {
	st := store.NewServer("work", &model.User{Id: "me", Username: "me"})
	for _, team := range []string{"t1", "t2"} {
		ch := &model.Channel{Id: "c-" + team, TeamId: team, Name: "town-square", DisplayName: "Town Square", Type: model.ChannelTypeOpen}
		st.AddTeam(&model.Team{Id: team, DisplayName: strings.ToUpper(team)}, []*model.Channel{ch}, map[string]model.ChannelMember{ch.Id: {ChannelId: ch.Id}})
	}
	st.CRT = crt
	st.SetThreadTotals("t2", store.ThreadTotals{Unread: 3, Mentions: 1})
	m := New(context.Background(), []*mm.Session{{Server: config.Server{Name: "work"}}}, nil, config.UI{}, nil)
	m.servers["work"], m.active, m.current = st, "work", chanRef{"work", "c-t2"}
	return m
}

func TestSwitcherThreads(t *testing.T) {
	var threads []switchItem
	for _, it := range threadsTestModel(true).switchItems() {
		if it.threadsTeam != "" {
			threads = append(threads, it)
		}
	}
	if len(threads) != 2 || threads[1].threadsTeam != "t2" || threads[1].unread != 3 || threads[1].mentions != 1 {
		t.Fatalf("threads items = %+v", threads)
	}
	if got := filterItems(threads, "thr"); len(got) != 2 {
		t.Errorf("query thr matched %d threads items, want 2", len(got))
	}
	for _, it := range threadsTestModel(false).switchItems() {
		if it.threadsTeam != "" {
			t.Errorf("threads item without CRT: %+v", it)
		}
	}
}

func TestToggleThreads(t *testing.T) {
	m := threadsTestModel(true)
	m.toggleThreads()
	if m.threads == nil || m.threads.team != "t2" || m.threads.back != (chanRef{"work", "c-t2"}) {
		t.Fatalf("threads = %+v, want t2 opened from the current channel", m.threads)
	}

	m = threadsTestModel(false)
	if m.toggleThreads(); m.threads != nil || m.notice == "" {
		t.Errorf("without CRT: threads = %+v, notice = %q", m.threads, m.notice)
	}
}
