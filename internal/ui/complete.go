package ui

import (
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
	"github.com/kurund/termost/internal/emoji"
	"github.com/mattermost/mattermost/server/public/model"
)

const (
	maxCompletions = 6
	userSearchSize = 20
)

var (
	tokenRe = regexp.MustCompile(`(?:^|\s)([@~])([A-Za-z0-9._-]*)$`)
	emojiRe = regexp.MustCompile(`(?:^|\s):([A-Za-z0-9_+-]{2,})$`)
)

type compItem struct {
	insert string
	detail string
	faint  bool
}

type completion struct {
	trigger   byte
	query     string
	items     []compItem
	sel       int
	dismissed string
}

func (c *completion) active() bool {
	return c.trigger != 0 && len(c.items) > 0
}

type usersFoundMsg struct {
	ref   chanRef
	query string
	in    []*model.User
	out   []*model.User
}

// tokenAt finds a @user, ~channel or :emoji token ending at the cursor.
func tokenAt(beforeCursor string) (trigger byte, query string, ok bool) {
	if m := emojiRe.FindStringSubmatch(beforeCursor); m != nil {
		return ':', m[1], true
	}
	m := tokenRe.FindStringSubmatch(beforeCursor)
	if m == nil {
		return 0, "", false
	}
	return m[1][0], m[2], true
}

func (m *Model) textBeforeCursor() string {
	lines := strings.Split(m.input.Value(), "\n")
	row := min(m.input.Line(), len(lines)-1)
	runes := []rune(lines[row])
	return string(runes[:min(m.input.Column(), len(runes))])
}

func (m *Model) cursorAtEnd() bool {
	lines := strings.Split(m.input.Value(), "\n")
	return m.input.Line() == len(lines)-1 && m.input.Column() >= utf8.RuneCountInString(lines[len(lines)-1])
}

// updateCompletion recomputes suggestions after the input changed.
func (m *Model) updateCompletion() tea.Cmd {
	c := &m.completion
	before := m.textBeforeCursor()
	trigger, query, ok := tokenAt(before)
	if !ok || before == c.dismissed {
		m.setCompletion(0, "", nil)
		return nil
	}
	c.dismissed = ""

	st := m.servers[m.current.server]
	if st == nil {
		return nil
	}
	switch trigger {
	case '~':
		m.setCompletion(trigger, query, channelCompletions(m, query))
		return nil
	case ':':
		m.setCompletion(trigger, query, emojiCompletions(query))
		return nil
	}

	var items []compItem
	if c.trigger == '@' {
		for _, it := range c.items {
			if strings.HasPrefix(strings.ToLower(it.insert), strings.ToLower(query)) && !isSpecial(it.insert) {
				items = append(items, it)
			}
		}
	}
	m.setCompletion(trigger, query, append(items, specialMentions(query)...))
	return m.searchUsers(m.current, query)
}

func (m *Model) setCompletion(trigger byte, query string, items []compItem) {
	c := &m.completion
	wasActive := c.active()
	before := len(c.items)
	c.trigger, c.query = trigger, query
	if len(items) > maxCompletions {
		items = items[:maxCompletions]
	}
	c.items = items
	c.sel = min(c.sel, max(len(items)-1, 0))
	if wasActive != c.active() || before != len(c.items) {
		m.layout()
	}
}

func (m *Model) searchUsers(ref chanRef, query string) tea.Cmd {
	sess := m.byName[ref.server]
	st := m.servers[ref.server]
	ch := st.Channels[ref.channel]
	if ch == nil {
		return nil
	}
	return func() tea.Msg {
		var res *model.UserAutocomplete
		var err error
		if ch.TeamId == "" {
			res, _, err = sess.Client.AutocompleteUsers(m.ctx, query, userSearchSize, "")
		} else {
			res, _, err = sess.Client.AutocompleteUsersInChannel(m.ctx, ch.TeamId, ch.Id, query, userSearchSize, "")
		}
		if err != nil || res == nil {
			return nil
		}
		return usersFoundMsg{ref: ref, query: query, in: res.Users, out: res.OutOfChannel}
	}
}

func (m *Model) onUsersFound(msg usersFoundMsg) {
	c := &m.completion
	if msg.ref != m.current || c.trigger != '@' || c.query != msg.query {
		return
	}
	var items []compItem
	me := m.servers[msg.ref.server].Me.Id
	add := func(users []*model.User, outOfChannel bool) {
		for _, u := range users {
			if u.Id != me && u.DeleteAt == 0 {
				items = append(items, compItem{insert: u.Username, detail: strings.TrimSpace(u.GetFullName()), faint: outOfChannel})
			}
		}
	}
	add(msg.in, false)
	add(msg.out, true)
	items = append(items, specialMentions(msg.query)...)
	m.setCompletion('@', msg.query, items)
}

func specialMentions(query string) []compItem {
	var items []compItem
	for _, s := range []struct{ name, detail string }{
		{"here", "notify everyone online in this channel"},
		{"channel", "notify everyone in this channel"},
		{"all", "notify everyone in this channel"},
	} {
		if query != "" && strings.HasPrefix(s.name, strings.ToLower(query)) {
			items = append(items, compItem{insert: s.name, detail: s.detail})
		}
	}
	return items
}

func isSpecial(name string) bool {
	return name == "here" || name == "channel" || name == "all"
}

func channelCompletions(m *Model, query string) []compItem {
	st := m.servers[m.current.server]
	type cand struct {
		item  compItem
		score int
	}
	var cands []cand
	for _, team := range st.Teams {
		for _, id := range st.TeamChannels(team.Id) {
			ch := st.Channels[id]
			score, ok := fuzzyScore(query, ch.Name)
			if s2, ok2 := fuzzyScore(query, ch.DisplayName); ok2 && (!ok || s2 > score) {
				score, ok = s2, true
			}
			if ok {
				cands = append(cands, cand{compItem{insert: ch.Name, detail: ch.DisplayName + " · " + team.DisplayName}, score})
			}
		}
	}
	sort.SliceStable(cands, func(i, j int) bool { return cands[i].score > cands[j].score })
	items := make([]compItem, len(cands))
	for i, c := range cands {
		items[i] = c.item
	}
	return items
}

func emojiCompletions(query string) []compItem {
	names := filterEmoji(strings.ToLower(query))
	items := make([]compItem, 0, min(len(names), maxCompletions))
	for _, name := range names[:min(len(names), maxCompletions)] {
		glyph, _ := emoji.Lookup(name)
		items = append(items, compItem{insert: name, detail: glyph})
	}
	return items
}

func (c *completion) label(it compItem) string {
	if c.trigger == ':' {
		return it.detail + "  :" + it.insert + ":"
	}
	return string(c.trigger) + it.insert
}

// completionKey handles keys while suggestions are shown; it reports whether
// the key was consumed.
func (m *Model) completionKey(msg tea.KeyPressMsg) (tea.Cmd, bool) {
	c := &m.completion
	switch msg.String() {
	case "up", "ctrl+p":
		c.sel = max(c.sel-1, 0)
	case "down", "ctrl+n":
		c.sel = min(c.sel+1, len(c.items)-1)
	case "tab", "enter":
		m.acceptCompletion()
	case "esc":
		c.dismissed = m.textBeforeCursor()
		m.setCompletion(0, "", nil)
	default:
		return nil, false
	}
	return nil, true
}

func (m *Model) acceptCompletion() {
	c := &m.completion
	item := c.items[c.sel]
	typed := utf8.RuneCountInString(c.query) + 1
	for range typed {
		m.input, _ = m.input.Update(tea.KeyPressMsg{Code: tea.KeyBackspace})
	}
	text := string(c.trigger) + item.insert
	if c.trigger == ':' {
		text += ":"
	}
	m.input.InsertString(text + " ")
	m.setCompletion(0, "", nil)
}

func (m *Model) completionHeight() int {
	if !m.completion.active() {
		return 0
	}
	return len(m.completion.items) + 1
}

func (m *Model) renderCompletion(width int) string {
	c := &m.completion
	lines := []string{faint.Render(strings.Repeat("─", width))}
	for i, it := range c.items {
		label := c.label(it)
		detail := it.detail
		if c.trigger == ':' {
			detail = ""
		}
		line := label
		if it.faint {
			line = faint.Render(label)
		}
		if detail != "" {
			line += "  " + faint.Render(detail)
		}
		line = truncate(" "+line, width)
		if i == c.sel {
			line = selected.Render(padRight(truncate(strings.TrimRight(" "+label+"  "+detail, " "), width), width))
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}
