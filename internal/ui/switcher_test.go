package ui

import "testing"

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
