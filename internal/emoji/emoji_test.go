package emoji

import "testing"

func TestLookup(t *testing.T) {
	cases := map[string]string{
		"+1":               "👍",
		"thumbsup":         "👍",
		"white_check_mark": "✅",
		"flag-us":          "🇺🇸",
		"man-woman-girl":   "👨‍👩‍👧",
	}
	for name, want := range cases {
		if got, ok := Lookup(name); !ok || got != want {
			t.Errorf("Lookup(%q) = %q, %v; want %q", name, got, ok, want)
		}
	}
	if _, ok := Lookup("our_custom_parrot"); ok {
		t.Error("custom emoji should not resolve")
	}
}

func TestReplace(t *testing.T) {
	cases := map[string]string{
		"ship it :rocket: :+1:":   "ship it 🚀 👍",
		"at 10:30:00 sharp":       "at 10:30:00 sharp",
		":our_custom_parrot: yay": ":our_custom_parrot: yay",
		"no colons":               "no colons",
		"::":                      "::",
	}
	for in, want := range cases {
		if got := Replace(in); got != want {
			t.Errorf("Replace(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestNames(t *testing.T) {
	ns := Names()
	if len(ns) < 1000 {
		t.Fatalf("only %d names", len(ns))
	}
	found := map[string]bool{}
	for _, n := range ns {
		found[n] = true
	}
	if !found["+1"] || !found["thumbsup"] {
		t.Error("both +1 and thumbsup should be listed")
	}
}
