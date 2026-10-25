package ui

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/mattermost/mattermost/server/public/model"

	"github.com/kurund/termost/internal/store"
)

type rowKind int

const (
	rowServer rowKind = iota
	rowSpacer
	rowTeam
	rowThreads
	rowCategory
	rowDMHeader
	rowChannel
)

type row struct {
	kind     rowKind
	server   string
	team     string
	category *store.Category
	channel  string
	depth    int
}

func (r row) collapseKey() string {
	switch r.kind {
	case rowTeam:
		return r.server + "/" + r.team
	case rowCategory:
		return r.server + "/" + r.team + "/" + r.category.ID
	case rowDMHeader:
		return r.server + "/dms"
	}
	return r.server
}

// isCollapsed falls back to the category's collapsed state on the server
// until the user toggles it here.
func (m *Model) isCollapsed(r row) bool {
	if v, ok := m.collapsed[r.collapseKey()]; ok {
		return v
	}
	return r.kind == rowCategory && r.category.Collapsed
}

// rows lists every server, then the active server's teams, channels and DMs.
func (m *Model) rows() []row {
	var rows []row
	for _, sess := range m.sessions {
		rows = append(rows, row{kind: rowServer, server: sess.Server.Name})
	}
	rows = append(rows, row{kind: rowSpacer})

	name := m.active
	st := m.servers[name]
	if st == nil {
		return rows
	}
	for _, team := range st.Teams {
		r := row{kind: rowTeam, server: name, team: team.Id}
		rows = append(rows, r)
		if m.isCollapsed(r) {
			continue
		}
		if st.CRT {
			rows = append(rows, row{kind: rowThreads, server: name, team: team.Id, depth: 1})
		}
		for _, cat := range st.TeamCategories(team.Id) {
			if len(cat.Channels) == 0 {
				continue
			}
			c := row{kind: rowCategory, server: name, team: team.Id, category: &cat, depth: 1}
			rows = append(rows, c)
			collapsed := m.isCollapsed(c)
			for _, id := range cat.Channels {
				if !collapsed || m.keepWhenCollapsed(st, id) {
					rows = append(rows, row{kind: rowChannel, server: name, team: team.Id, channel: id, depth: 2})
				}
			}
		}
	}
	dm := row{kind: rowDMHeader, server: name}
	rows = append(rows, dm)
	keep := ""
	if m.current.server == name {
		keep = m.current.channel
	}
	collapsed := m.isCollapsed(dm)
	for _, id := range st.VisibleDMs(m.dmLimit, keep) {
		if !collapsed || m.keepWhenCollapsed(st, id) {
			rows = append(rows, row{kind: rowChannel, server: name, channel: id, depth: 1})
		}
	}
	return rows
}

// keepWhenCollapsed matches the official clients: a collapsed category still
// lists the open channel and unread ones.
func (m *Model) keepWhenCollapsed(st *store.Server, id string) bool {
	if m.current.server == st.Name && m.current.channel == id {
		return true
	}
	u, mentions := st.Unread(id)
	return mentions > 0 || (u > 0 && !st.Muted(id))
}

func (m *Model) rowIndex(ref chanRef) int {
	for i, r := range m.rows() {
		if r.kind == rowChannel && r.server == ref.server && r.channel == ref.channel {
			return i
		}
	}
	return -1
}

// pinnedRows is how many rows at the top (servers and the divider) stay in
// place while the rest scrolls; none when that would leave too little room.
func (m *Model) pinnedRows(height int) int {
	n := len(m.sessions) + 1
	if n > height-3 {
		return 0
	}
	return n
}

// sidebarWindow picks the row indices to draw, keeping the pinned rows and
// scrolling the rest so focus stays visible.
func (m *Model) sidebarWindow(total, height, focus int) []int {
	pinned := m.pinnedRows(height)
	scrollH := height - pinned
	if !m.sidebarFree && focus >= pinned {
		if focus < m.sidebarOffset {
			m.sidebarOffset = focus
		}
		if focus >= m.sidebarOffset+scrollH {
			m.sidebarOffset = focus - scrollH + 1
		}
	}
	m.sidebarOffset = max(min(m.sidebarOffset, total-scrollH), pinned)

	drawn := make([]int, 0, height)
	for i := 0; i < pinned; i++ {
		drawn = append(drawn, i)
	}
	for i := m.sidebarOffset; i < total && len(drawn) < height; i++ {
		drawn = append(drawn, i)
	}
	return drawn
}

func (m *Model) renderSidebar(outer, height int) string {
	rows := m.rows()
	focus := m.cursor
	if m.focus != focusSidebar {
		focus = max(m.rowIndex(m.current), 0)
	}
	m.sidebarDrawn = m.sidebarWindow(len(rows), height, focus)

	width := outer - 1
	lines := make([]string, 0, height)
	for _, i := range m.sidebarDrawn {
		line := m.renderRow(rows[i], width)
		isCurrent := rows[i].kind == rowChannel && rows[i].server == m.current.server && rows[i].channel == m.current.channel && m.threads == nil
		if m.threads != nil && rows[i].kind == rowThreads && rows[i].server == m.threads.server && rows[i].team == m.threads.team {
			isCurrent = true
		}
		switch {
		case m.focus == focusSidebar && i == m.cursor:
			line = selected.Render(padRight(ansi.Strip(line), width))
		case isCurrent && m.focus == focusSidebar:
			line = cursorRow.Render(padRight(ansi.Strip(line), width))
		case isCurrent:
			line = selected.Render(padRight(ansi.Strip(line), width))
		}
		lines = append(lines, line)
	}
	for len(lines) < height {
		lines = append(lines, "")
	}
	return sidebarBox.Height(height).Render(strings.Join(lines, "\n"))
}

func (m *Model) renderRow(r row, width int) string {
	arrow := "▾ "
	if m.isCollapsed(r) {
		arrow = "▸ "
	}
	st := m.servers[r.server]

	switch r.kind {
	case rowSpacer:
		return faint.Render(strings.Repeat("─", width))

	case rowServer:
		lead := fmt.Sprintf("  %d ", m.serverIndex(r.server)+1)
		if r.server == m.active {
			lead = bold.Render(fmt.Sprintf("› %d ", m.serverIndex(r.server)+1))
		}
		dot := faint.Render("○ ")
		switch {
		case m.errs[r.server] != "" || m.expired[r.server]:
			dot = errorStyle.Render("⚠ ")
		case m.online[r.server]:
			dot = online.Render("● ")
		}
		name := r.server
		if st == nil {
			return withBadge(lead+dot+faint.Render(name), 0, 0, width)
		}
		unread, mentions := st.TotalUnread()
		switch {
		case unread > 0 || mentions > 0:
			name = unreadStyle.Render(name)
		case r.server == m.active:
			name = bold.Render(name)
		}
		return withBadge(lead+dot+name, unread, mentions, width)

	case rowTeam:
		name := r.team
		for _, t := range st.Teams {
			if t.Id == r.team {
				name = t.DisplayName
			}
		}
		label := arrow + name
		unread, mentions := st.TeamUnread(r.team)
		if !m.isCollapsed(r) {
			unread, mentions = 0, 0
		}
		if unread > 0 || mentions > 0 {
			label = unreadStyle.Render(label)
		}
		return withBadge(label, unread, mentions, width)

	case rowThreads:
		totals := st.ThreadTotals(r.team)
		label := "  💬 Threads"
		if totals.Unread > 0 || totals.Mentions > 0 {
			label = unreadStyle.Render(label)
		}
		return withBadge(label, totals.Unread, totals.Mentions, width)

	case rowCategory:
		return faint.Render(" " + arrow + strings.ToUpper(r.category.Name))

	case rowDMHeader:
		return faint.Render(arrow + "Direct messages")
	}

	ch := st.Channels[r.channel]
	icon, prefix := "", "# "
	switch ch.Type {
	case model.ChannelTypePrivate:
		prefix = "◆ "
	case model.ChannelTypeDirect:
		icon, prefix = presence(st.DMStatus(r.channel)), ""
	case model.ChannelTypeGroup:
		prefix = "@ "
	}
	label := prefix + st.ChannelName(r.channel)
	unread, mentions := st.Unread(r.channel)
	indent := strings.Repeat(" ", r.depth)
	marker := "  "
	switch {
	case st.Muted(r.channel):
		label = mutedStyle.Render(label)
		unread = 0
	case unread > 0 || mentions > 0:
		label = unreadStyle.Render(label)
		marker = unreadStyle.Render("• ")
	default:
		label = readStyle.Render(label)
	}
	return withBadge(indent+marker+icon+label, unread, mentions, width)
}

func withBadge(label string, unread, mentions int64, width int) string {
	badge := ""
	switch {
	case mentions > 0:
		badge = mentionTag.Render(fmt.Sprintf(" %d ", mentions))
	case unread > 0:
		badge = unreadStyle.Render(fmt.Sprint(unread))
	}
	room := width - lipgloss.Width(badge) - 1
	label = truncate(label, room)
	return padRight(label, room+1) + badge
}

func truncate(s string, width int) string {
	return ansi.Truncate(s, width, "…")
}

func padRight(s string, width int) string {
	return s + strings.Repeat(" ", max(width-lipgloss.Width(s), 0))
}
