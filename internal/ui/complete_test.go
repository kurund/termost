package ui

import (
	"strings"
	"testing"
)

func TestTokenAt(t *testing.T) {
	cases := []struct {
		in      string
		trigger byte
		query   string
		ok      bool
	}{
		{"@", '@', "", true},
		{"hi @al", '@', "al", true},
		{"hi @first.last", '@', "first.last", true},
		{"see ~de", '~', "de", true},
		{"line one\n@bo", '@', "bo", true},
		{"mail a@b", 0, "", false},
		{"@alice ", 0, "", false},
		{"plain text", 0, "", false},
		{":th", ':', "th", true},
		{"nice :thumbs", ':', "thumbs", true},
		{"ok :+1", ':', "+1", true},
		{"at 12:30", 0, "", false},
		{"note:ab", 0, "", false},
		{"so :t", 0, "", false},
		{"done :smile:", 0, "", false},
		{"https://example", 0, "", false},
	}
	for _, tc := range cases {
		trigger, query, ok := tokenAt(tc.in)
		if trigger != tc.trigger || query != tc.query || ok != tc.ok {
			t.Errorf("tokenAt(%q) = %q %q %v, want %q %q %v", tc.in, trigger, query, ok, tc.trigger, tc.query, tc.ok)
		}
	}
}

func TestSpecialMentions(t *testing.T) {
	if got := specialMentions(""); len(got) != 0 {
		t.Errorf("empty query should not suggest specials: %v", got)
	}
	if got := specialMentions("ch"); len(got) != 1 || got[0].insert != "channel" {
		t.Errorf("ch = %v", got)
	}
	if got := specialMentions("A"); len(got) != 1 || got[0].insert != "all" {
		t.Errorf("A = %v", got)
	}
}

func TestEmojiCompletions(t *testing.T) {
	items := emojiCompletions("Thumbsu")
	if len(items) == 0 || items[0].insert != "thumbsup" || items[0].detail != "👍" {
		t.Fatalf("Thumbsu = %v", items)
	}
	for _, it := range emojiCompletions("thumbs") {
		if strings.Contains(it.insert, "skin_tone") {
			t.Errorf("skin tone variant %q shown without asking", it.insert)
		}
	}
	if got := emojiCompletions("thumbsup_dark_skin"); len(got) == 0 || got[0].insert != "thumbsup_dark_skin_tone" {
		t.Errorf("thumbsup_dark_skin = %v", got)
	}
	if got := emojiCompletions("zzzqqq"); len(got) != 0 {
		t.Errorf("zzzqqq = %v", got)
	}
}
