package ui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCompletePath(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"report-q1.pdf", "report-q2.pdf", "photo.png", ".hidden"} {
		if err := os.WriteFile(filepath.Join(dir, name), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Mkdir(filepath.Join(dir, "pics"), 0o700); err != nil {
		t.Fatal(err)
	}
	base := dir + "/"

	if got, c := completePath(base + "pho"); got != base+"photo.png" || c != nil {
		t.Errorf("unique = %q %v", got, c)
	}
	if got, c := completePath(base + "rep"); got != base+"report-q" || len(c) != 2 {
		t.Errorf("common prefix = %q %v", got, c)
	}
	if got, _ := completePath(base + "pi"); got != base+"pics/" {
		t.Errorf("dir = %q", got)
	}
	if _, c := completePath(base); strings.Contains(strings.Join(c, " "), ".hidden") {
		t.Errorf("hidden file offered without a leading dot: %v", c)
	}
	if got, _ := completePath(base + "zzz"); got != base+"zzz" {
		t.Errorf("no match = %q", got)
	}
}

func TestSlashRouting(t *testing.T) {
	m := &Model{}
	if _, text, handled := m.slash("//shrug"); handled || text != "/shrug" {
		t.Errorf("// escape = %q %v", text, handled)
	}
	if _, text, handled := m.slash("hello"); handled || text != "hello" {
		t.Errorf("plain = %q %v", text, handled)
	}
	for _, text := range []string{
		"how do I use /paste here?",
		"use /upload to share files",
		"see /upload /definitely/not/here.png",
	} {
		if _, rest, handled := m.slash(text); handled || rest != text {
			t.Errorf("%q should be sent as text, got %q %v", text, rest, handled)
		}
	}
}

func TestTrailingAttachment(t *testing.T) {
	if m := trailingPasteRe.FindStringSubmatch("Hi please check this /paste "); m == nil || m[1] != "Hi please check this" {
		t.Errorf("trailing /paste = %q", m)
	}
	if m := trailingPasteRe.FindStringSubmatch("line one\nline two /paste"); m == nil || m[1] != "line one\nline two" {
		t.Errorf("multi-line /paste = %q", m)
	}
	if m := trailingUploadRe.FindStringSubmatch("look at this /upload /a/Shot\\ 1.png /b/c.pdf"); m == nil || m[1] != "look at this" || m[2] != `/a/Shot\ 1.png /b/c.pdf` {
		t.Errorf("trailing /upload = %q", m)
	}
}

func TestShellWords(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{`/a/Screenshot\ from\ 2026-10-01\ 01-25-55.png`, []string{"/a/Screenshot from 2026-10-01 01-25-55.png"}},
		{`'/a/my file.png' "/b/it's \"x\".txt"`, []string{"/a/my file.png", `/b/it's "x".txt`}},
		{"/a/one.png /b/two\\ 2.png\n", []string{"/a/one.png", "/b/two 2.png"}},
		{`'/a/it'\''s.png'`, []string{"/a/it's.png"}},
		{`"/a/back\slash"`, []string{`/a/back\slash`}},
	}
	for _, tc := range cases {
		got, err := shellWords(tc.in)
		if err != nil || strings.Join(got, "|") != strings.Join(tc.want, "|") {
			t.Errorf("shellWords(%q) = %q, %v; want %q", tc.in, got, err, tc.want)
		}
	}
	if _, err := shellWords(`'/a/open`); err == nil {
		t.Error("unterminated quote should fail")
	}
}

func TestUploadPathsAndDrops(t *testing.T) {
	dir := t.TempDir()
	spaced := filepath.Join(dir, "Screenshot from 2026.png")
	odd := filepath.Join(dir, `raw\ name.txt`)
	for _, p := range []string{spaced, odd} {
		if err := os.WriteFile(p, nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	escaped := strings.ReplaceAll(spaced, " ", `\ `)

	for _, in := range []string{escaped, spaced, "'" + spaced + "'", "file://" + strings.ReplaceAll(spaced, " ", "%20")} {
		if got, err := uploadPaths(in); err != nil || len(got) != 1 || got[0] != spaced {
			t.Errorf("uploadPaths(%q) = %q, %v", in, got, err)
		}
	}
	if got, err := uploadPaths(odd); err != nil || len(got) != 1 || got[0] != odd {
		t.Errorf("an existing name with a backslash should be used as is: %q, %v", got, err)
	}

	oddEscaped := strings.NewReplacer(`\`, `\\`, " ", `\ `).Replace(odd)
	if got, ok := droppedFiles(escaped + " " + oddEscaped + " "); !ok || len(got) != 2 || got[1] != odd {
		t.Errorf("two dropped files = %q, %v", got, ok)
	}
	for _, in := range []string{"hello world", filepath.Join(dir, "missing.png"), dir, "relative.png", ""} {
		if _, ok := droppedFiles(in); ok {
			t.Errorf("droppedFiles(%q) should be false", in)
		}
	}
}
