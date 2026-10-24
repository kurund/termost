package ui

import (
	"fmt"
	"sort"
	"strings"
	"unicode"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/mattermost/mattermost/server/public/model"
)

type switchItem struct {
	ref      chanRef
	name     string
	slug     string
	prefix   string
	detail   string
	unread   int64
	mentions int64
	muted    bool
	lastPost int64
}

func (it switchItem) hasUnread() bool {
	return it.mentions > 0 || (it.unread > 0 && !it.muted)
}

type switcher struct {
	open  bool
	input textinput.Model
	sel   int
	start int
}

func newSwitcher() switcher {
	in := textinput.New()
	in.SetVirtualCursor(false)
	in.Prompt = "Jump to: "
	in.Placeholder = "channel or person, Enter to open, Esc to cancel"
	return switcher{input: in}
}

// switchItems lists every channel and DM on the active server, including
// DMs hidden from the sidebar by dm_limit.
func (m *Model) switchItems() []switchItem {
	name := m.active
	st := m.servers[name]
	if st == nil {
		return nil
	}
	var items []switchItem
	add := func(id, team string) {
		ch := st.Channels[id]
		unread, mentions := st.Unread(id)
		prefix, slug := "# ", ch.Name
		switch ch.Type {
		case model.ChannelTypePrivate:
			prefix = "◆ "
		case model.ChannelTypeDirect, model.ChannelTypeGroup:
			prefix, slug = "@ ", ""
		}
		items = append(items, switchItem{
			ref:      chanRef{name, id},
			name:     st.ChannelName(id),
			slug:     slug,
			prefix:   prefix,
			detail:   team,
			unread:   unread,
			mentions: mentions,
			muted:    st.Muted(id),
			lastPost: ch.LastPostAt,
		})
	}
	for _, team := range st.Teams {
		for _, id := range st.TeamChannels(team.Id) {
			add(id, team.DisplayName)
		}
	}
	for _, id := range st.DMs() {
		add(id, "")
	}
	return items
}

type scored struct {
	switchItem
	score int
}

// filterItems ranks items for query. With no query, unread channels come
// first, then the most recently active.
func filterItems(items []switchItem, query string) []switchItem {
	var out []scored
	for _, it := range items {
		score, ok := fuzzyScore(query, it.name)
		if !ok {
			score, ok = fuzzyScore(query, it.slug)
		}
		if ok {
			out = append(out, scored{it, score})
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.score != b.score {
			return a.score > b.score
		}
		if (a.mentions > 0) != (b.mentions > 0) {
			return a.mentions > 0
		}
		if a.hasUnread() != b.hasUnread() {
			return a.hasUnread()
		}
		return a.lastPost > b.lastPost
	})
	res := make([]switchItem, len(out))
	for i, s := range out {
		res[i] = s.switchItem
	}
	return res
}

// fuzzyScore matches query as a case-insensitive subsequence of target.
// Matches at the start of words and consecutive runs score higher; shorter
// targets win ties.
func fuzzyScore(query, target string) (int, bool) {
	if query == "" {
		return 0, true
	}
	q := []rune(strings.ToLower(query))
	t := []rune(strings.ToLower(target))
	score, qi, prev := 0, 0, -2
	for ti := 0; ti < len(t) && qi < len(q); ti++ {
		if t[ti] != q[qi] {
			continue
		}
		s := 1
		switch {
		case ti == prev+1:
			s += 8
		case ti == 0:
			s += 10
		case !unicode.IsLetter(t[ti-1]) && !unicode.IsDigit(t[ti-1]):
			s += 6
		}
		score += s
		prev = ti
		qi++
	}
	if qi < len(q) {
		return 0, false
	}
	return score*100 - len(t), true
}

func (m *Model) openSwitcher() tea.Cmd {
	m.switcher.open = true
	m.switcher.sel = 0
	m.switcher.input.Reset()
	m.input.Blur()
	return m.switcher.input.Focus()
}

func (m *Model) closeSwitcher() tea.Cmd {
	m.switcher.open = false
	m.switcher.input.Blur()
	if m.focus == focusInput {
		return m.input.Focus()
	}
	return nil
}

func (m *Model) switcherKey(msg tea.KeyPressMsg) tea.Cmd {
	items := filterItems(m.switchItems(), m.switcher.input.Value())
	switch msg.String() {
	case "esc", "ctrl+k":
		return m.closeSwitcher()
	case "up", "ctrl+p":
		m.switcher.sel = max(m.switcher.sel-1, 0)
		return nil
	case "down", "ctrl+n", "tab":
		m.switcher.sel = min(m.switcher.sel+1, max(len(items)-1, 0))
		return nil
	case "enter":
		if m.switcher.sel >= len(items) {
			return nil
		}
		ref := items[m.switcher.sel].ref
		m.focus = focusInput
		return tea.Batch(m.closeSwitcher(), m.open(ref))
	}
	var cmd tea.Cmd
	m.switcher.input, cmd = m.switcher.input.Update(msg)
	m.switcher.sel = 0
	return cmd
}

func (m *Model) renderSwitcher(width, height int) string {
	items := filterItems(m.switchItems(), m.switcher.input.Value())
	m.switcher.sel = min(m.switcher.sel, max(len(items)-1, 0))

	lines := []string{m.switcher.input.View(), faint.Render(strings.Repeat("─", width))}
	listHeight := max(height-len(lines), 1)
	start := max(m.switcher.sel-listHeight+1, 0)
	m.switcher.start = start
	for i := start; i < len(items) && i < start+listHeight; i++ {
		lines = append(lines, renderSwitchItem(items[i], i == m.switcher.sel, width))
	}
	if len(items) == 0 {
		lines = append(lines, faint.Render("  No matches"))
	}
	for len(lines) < height {
		lines = append(lines, "")
	}
	return strings.Join(lines[:height], "\n")
}

func renderSwitchItem(it switchItem, selectedRow bool, width int) string {
	label := it.prefix + it.name
	marker := "  "
	switch {
	case it.muted:
		label = mutedStyle.Render(label)
	case it.hasUnread():
		label = unreadStyle.Render(label)
		marker = unreadStyle.Render("• ")
	default:
		label = readStyle.Render(label)
	}
	if it.detail != "" {
		label += "  " + faint.Render(it.detail)
	}
	badge := ""
	switch {
	case it.mentions > 0:
		badge = mentionTag.Render(fmt.Sprintf(" %d ", it.mentions))
	case it.unread > 0 && !it.muted:
		badge = unreadStyle.Render(fmt.Sprint(it.unread))
	}
	room := width - lipgloss.Width(badge) - 3
	line := marker + padRight(truncate(label, room), room) + " " + badge
	if selectedRow {
		return selected.Render(padRight(ansi.Strip(line), width))
	}
	return line
}
