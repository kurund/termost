// Package emoji maps Mattermost emoji names to Unicode.
package emoji

import (
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/mattermost/mattermost/server/public/model"
)

var (
	shortcodeRe = regexp.MustCompile(`:([a-z0-9_+\-]+):`)

	once   sync.Once
	byName map[string]string
)

// Lookup returns the Unicode emoji for a system emoji name such as "+1" or
// "white_check_mark". Custom emoji are not known and return false.
func Lookup(name string) (string, bool) {
	once.Do(build)
	s, ok := byName[name]
	return s, ok
}

// Replace turns :name: shortcodes in text into Unicode, leaving unknown ones.
func Replace(text string) string {
	if !strings.Contains(text, ":") {
		return text
	}
	return shortcodeRe.ReplaceAllStringFunc(text, func(code string) string {
		if s, ok := Lookup(code[1 : len(code)-1]); ok {
			return s
		}
		return code
	})
}

func build() {
	byName = make(map[string]string, len(model.SystemEmojis))
	for name, hex := range model.SystemEmojis {
		var b strings.Builder
		for _, part := range strings.Split(hex, "-") {
			cp, err := strconv.ParseUint(part, 16, 32)
			if err != nil {
				b.Reset()
				break
			}
			b.WriteRune(rune(cp))
		}
		if b.Len() > 0 {
			byName[name] = b.String()
		}
	}
}

var (
	namesOnce sync.Once
	names     []string
)

// Names lists every system emoji name, aliases included, sorted.
func Names() []string {
	namesOnce.Do(func() {
		once.Do(build)
		for name := range byName {
			names = append(names, name)
		}
		sort.Strings(names)
	})
	return names
}
