package termsafe

import "testing"

func TestStrip(t *testing.T) {
	tests := map[string]string{
		"plain text":                       "plain text",
		"keeps\nnewlines\tand tabs":        "keeps\nnewlines\tand tabs",
		"emoji 👍 and ünïcode":              "emoji 👍 and ünïcode",
		"a\x1b]52;c;cHduZWQ=\x07b":         "a]52;c;cHduZWQ=b",
		"\x1b[41mred\x1b[0m":               "[41mred[0m",
		"bell\a del\x7f cr\r":              "bell del cr",
		"c1 \u009b31m csi \u009d2;x\u009c": "c1 31m csi 2;x",
	}
	for in, want := range tests {
		if got := Strip(in); got != want {
			t.Errorf("Strip(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestLine(t *testing.T) {
	if got := Line("two\nlines\tand \x1b[1mbold"); got != "twolinesand [1mbold" {
		t.Errorf("Line = %q", got)
	}
}
