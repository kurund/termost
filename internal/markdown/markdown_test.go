package markdown

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func plainRender(src string, width int) string {
	return ansi.Strip(Render(src, width, "kurund"))
}

func TestLayout(t *testing.T) {
	cases := []struct {
		name, src string
		width     int
		want      string
	}{
		{"single newline is a line break", "one\ntwo", 40, "one\ntwo"},
		{"paragraphs keep a blank line", "one\n\ntwo", 40, "one\n\ntwo"},
		{"wraps on words", "the quick brown fox jumps", 10, "the quick\nbrown fox\njumps"},
		{"long word is split", "abcdefghijkl", 5, "abcde\nfghij\nkl"},
		{"emphasis markers removed", "**bold** _it_ ~~gone~~ `code`", 40, "bold it gone code"},
		{"heading without hashes", "## Title", 40, "Title"},
		{"bullet list", "- a\n- b", 40, "• a\n• b"},
		{"ordered list keeps start", "3. a\n4. b", 40, "3. a\n4. b"},
		{"list item wraps with hanging indent", "- aaa bbb ccc", 7, "• aaa\n  bbb\n  ccc"},
		{"nested list", "- a\n  - b", 40, "• a\n  • b"},
		{"task list", "- [x] done\n- [ ] todo", 40, "• ☑ done\n• ☐ todo"},
		{"quote", "> hi\n> there", 40, "│ hi\n│ there"},
		{"code block untouched", "```\nif a  {\n\tb()\n}\n```", 40, "▏if a  {\n▏    b()\n▏}"},
		{"code block hard wraps", "```\nabcdefgh\n```", 5, "▏abcd\n▏efgh"},
		{"link shows destination", "[docs](https://x.io)", 40, "docs (https://x.io)"},
		{"bare url", "see https://x.io/a now", 40, "see https://x.io/a now"},
		{"link same as text", "[https://x.io](https://x.io)", 40, "https://x.io"},
		{"rule", "a\n\n---\n\nb", 40, "a\n\n" + strings.Repeat("─", 20) + "\n\nb"},
		{"table", "| a | bb |\n|---|---|\n| 1 | 2 |", 40, "a │ bb\n──┼───\n1 │ 2 "},
		{"blank line before table", "> q\n\n| a |\n|---|\n| 1 |", 40, "│ q\n\na\n─\n1"},
		{"emoji shortcodes", "ship it :rocket: `:rocket:`", 40, "ship it 🚀 :rocket:"},
		{"empty", "", 40, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := plainRender(tc.src, tc.width); got != tc.want {
				t.Errorf("got\n%q\nwant\n%q", got, tc.want)
			}
		})
	}
}

func TestWidthNeverExceeded(t *testing.T) {
	src := "Hey **team**, the build on `main` is _failing_ again. See https://example.com/a/very/long/path/that/goes/on and [docs](https://docs.example.com)\n\n| col one | col two is very wide indeed |\n|---|---|\n| x | y |\n\n```\na very long line of code that must be wrapped somewhere\n```"
	for _, width := range []int{10, 20, 37, 80} {
		for _, line := range strings.Split(Render(src, width, "me"), "\n") {
			if w := ansi.StringWidth(line); w > width {
				t.Errorf("width %d: line %q is %d wide", width, ansi.Strip(line), w)
			}
		}
	}
}

func TestMentions(t *testing.T) {
	out := Render("hi @kurund and @alice, ping @here. mail a@b.com ~dev", 80, "kurund")
	if !strings.Contains(out, mentionMe.Render("@kurund")) {
		t.Error("own mention not highlighted")
	}
	if !strings.Contains(out, mentionMe.Render("@here")) {
		t.Error("@here not highlighted")
	}
	if !strings.Contains(out, mentionUser.Render("@alice")) {
		t.Error("other user mention not bold")
	}
	if !strings.Contains(out, mentionUser.Render("~dev")) {
		t.Error("channel link not bold")
	}
	if strings.Contains(out, mentionUser.Render("@b.com")) {
		t.Error("email address treated as mention")
	}
	if got := ansi.Strip(out); got != "hi @kurund and @alice, ping @here. mail a@b.com ~dev" {
		t.Errorf("text changed: %q", got)
	}
}

func TestMentionsNotInCode(t *testing.T) {
	out := Render("`@kurund`", 80, "kurund")
	if strings.Contains(out, mentionMe.Render("@kurund")) {
		t.Error("mention highlighted inside code")
	}
}

func TestNoStyleLeaksAcrossLines(t *testing.T) {
	out := Render("**bold text that wraps over several lines here**", 10, "")
	for _, line := range strings.Split(out, "\n") {
		if strings.Count(line, "\x1b[") > 0 && !strings.HasSuffix(line, "m") {
			t.Errorf("line does not end with a reset: %q", line)
		}
	}
}

func TestCache(t *testing.T) {
	a := Render("cached **text**", 30, "me")
	if b := Render("cached **text**", 30, "me"); a != b {
		t.Error("cache returned different output")
	}
	if Render("cached **text**", 5, "me") == a {
		t.Error("cache ignored width")
	}
}

func TestHyperlinks(t *testing.T) {
	out := Render("see https://example.com/a/very/long/path and [docs](https://d.io)", 20, "")
	lines := strings.Split(out, "\n")
	linked := 0
	for _, l := range lines {
		if strings.Contains(l, "\x1b]8;") {
			linked++
			if strings.Count(l, "\x1b]8;;\x07")+strings.Count(l, "\x1b]8;;\x1b\\") == 0 {
				t.Errorf("hyperlink not closed on its line: %q", l)
			}
		}
	}
	if linked < 3 {
		t.Errorf("expected the wrapped URL and the docs link to carry hyperlinks on %d+ lines, got %d:\n%q", 3, linked, out)
	}
	if !strings.Contains(out, "https://example.com/a/very/long/path") {
		t.Error("full URL missing from hyperlink target")
	}
}

func TestImageIsLinked(t *testing.T) {
	out := Render("![graph](https://example.com/g.png)", 60, "")
	if !strings.Contains(out, "\x1b]8;;https://example.com/g.png") {
		t.Errorf("image not hyperlinked: %q", out)
	}
}

func TestRenderStripsControlSequences(t *testing.T) {
	out := Render("hi \x1b]52;c;cHduZWQ=\x07 \x1b[41mred\x1b[0m \u009b31m", 80, "me")
	for _, seq := range []string{"\x1b]52", "\x1b[41m", "\x07", "\u009b"} {
		if strings.Contains(out, seq) {
			t.Errorf("Render output contains %q: %q", seq, out)
		}
	}
}
