package ui

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
)

func TestQueryTerms(t *testing.T) {
	got := strings.Join(queryTerms(`deploy* "build failed" from:alice in:dev -flaky after:2026-01-01`), "|")
	if got != "deploy|build|failed" {
		t.Errorf("queryTerms = %q", got)
	}
}

func TestMatchSpans(t *testing.T) {
	spans := matchSpans([]rune("Build ÉTÉ build"), []string{"build", "été"})
	if len(spans) != 3 || spans[0] != [2]int{0, 5} || spans[1] != [2]int{6, 9} || spans[2] != [2]int{10, 15} {
		t.Errorf("spans = %v", spans)
	}
	if spans := matchSpans([]rune("deployment"), []string{"deploy", "deployment"}); len(spans) != 1 || spans[0] != [2]int{0, 10} {
		t.Errorf("longest overlapping term should win: %v", spans)
	}
}

func TestExcerpt(t *testing.T) {
	msg := strings.Repeat("lorem ipsum ", 20) + "the build failed\non main"
	out := excerpt(msg, []string{"build"}, 40)
	plain := ansi.Strip(out)
	if ansi.StringWidth(plain) > 40 {
		t.Errorf("excerpt wider than 40: %q", plain)
	}
	if !strings.HasPrefix(plain, "…") || !strings.Contains(plain, "build") {
		t.Errorf("excerpt should move to the match: %q", plain)
	}
	if !strings.Contains(out, matchStyle.Render("build")) {
		t.Errorf("match not highlighted: %q", out)
	}
	if got := ansi.Strip(excerpt("short build note", []string{"build"}, 40)); got != "short build note" {
		t.Errorf("short message = %q", got)
	}
}

func TestSearchTime(t *testing.T) {
	now := time.Date(2026, 10, 22, 18, 0, 0, 0, time.Local)
	cases := map[time.Time]string{
		time.Date(2026, 10, 22, 9, 5, 0, 0, time.Local): "09:05",
		time.Date(2026, 3, 4, 9, 5, 0, 0, time.Local):   "Mar 4 09:05",
		time.Date(2025, 3, 4, 9, 5, 0, 0, time.Local):   "Mar 4 2025",
	}
	for at, want := range cases {
		if got := searchTime(at.UnixMilli(), now); got != want {
			t.Errorf("searchTime(%v) = %q, want %q", at, got, want)
		}
	}
}
