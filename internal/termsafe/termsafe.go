// Package termsafe removes terminal control characters from untrusted text.
package termsafe

import "strings"

// Strip drops C0 controls except newline and tab, DEL and C1 controls, so
// server-supplied text cannot start escape sequences in the terminal.
func Strip(s string) string {
	if !strings.ContainsFunc(s, isControl) {
		return s
	}
	return strings.Map(func(r rune) rune {
		if isControl(r) {
			return -1
		}
		return r
	}, s)
}

// Line is Strip that also drops newlines and tabs, for single-line fields.
func Line(s string) string {
	return strings.Map(func(r rune) rune {
		if r == '\n' || r == '\t' || isControl(r) {
			return -1
		}
		return r
	}, s)
}

func isControl(r rune) bool {
	switch {
	case r == '\n' || r == '\t':
		return false
	case r < 0x20, r == 0x7f:
		return true
	case r >= 0x80 && r <= 0x9f:
		return true
	}
	return false
}
