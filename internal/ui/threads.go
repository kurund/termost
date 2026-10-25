package ui

import (
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/mattermost/mattermost/server/public/model"

	"github.com/kurund/termost/internal/emoji"
	"github.com/kurund/termost/internal/store"
)

type threadsList struct {
	server  string
	team    string
	items   []*model.ThreadResponse
	sel     int
	loading bool
	back    chanRef
	// lineItem maps each line on screen to an index in items, or -1.
	lineItem []int
}

type threadsMsg struct {
	server, team string
	items        []*model.ThreadResponse
	err          error
}

type threadTotalsMsg struct {
	server, team string
	totals       store.ThreadTotals
}

func (m *Model) openThreadsList(server, team string) tea.Cmd {
	back := m.current
	if m.threads != nil {
		back = m.threads.back
	}
	m.thread = nil
	m.threads = &threadsList{server: server, team: team, loading: true, back: back}
	m.active = server
	m.focus = focusMessages
	m.input.Blur()
	return m.loadThreadsList(server, team)
}

// toggleThreads opens the Threads list for the current team, or closes it.
func (m *Model) toggleThreads() tea.Cmd {
	if m.threads != nil {
		return m.closeThreadsList()
	}
	st := m.servers[m.active]
	if st == nil || !st.CRT {
		m.notice = "Threads need Collapsed Reply Threads, which is off on this server"
		return nil
	}
	channel := ""
	if m.current.server == m.active {
		channel = m.current.channel
	}
	team := st.ThreadTeam(channel)
	if team == "" {
		return nil
	}
	return m.openThreadsList(m.active, team)
}

func (m *Model) closeThreadsList() tea.Cmd {
	back := m.threads.back
	m.threads = nil
	if back.channel != "" {
		return m.open(back)
	}
	m.focus = focusInput
	m.refreshMessages()
	return m.input.Focus()
}

func (m *Model) loadThreadsList(server, team string) tea.Cmd {
	sess := m.byName[server]
	return func() tea.Msg {
		items, err := sess.FollowedThreads(m.ctx, team)
		return threadsMsg{server: server, team: team, items: items, err: err}
	}
}

func (m *Model) loadThreadTotals(server, team string) tea.Cmd {
	sess := m.byName[server]
	return func() tea.Msg {
		totals, err := sess.ThreadTotals(m.ctx, team)
		if err != nil {
			return nil
		}
		return threadTotalsMsg{server: server, team: team, totals: totals}
	}
}

func (m *Model) onThreadsList(msg threadsMsg) {
	t := m.threads
	if t == nil || t.server != msg.server || t.team != msg.team {
		return
	}
	t.loading = false
	if msg.err != nil {
		m.setError(fmt.Errorf("loading threads: %w", msg.err))
		return
	}
	t.items = msg.items
	t.sel = min(t.sel, max(len(t.items)-1, 0))
	users := map[string]*model.User{}
	for _, it := range t.items {
		for _, u := range it.Participants {
			users[u.Id] = u
		}
	}
	m.servers[msg.server].AddUsers(users)
}

// onThreadEvent refreshes counts and the open list after the server reports
// a change to a followed thread.
func (m *Model) onThreadEvent(server string, ev *model.WebSocketEvent) tea.Cmd {
	st := m.servers[server]
	if st == nil || !st.CRT {
		return nil
	}
	teams := []string{}
	if b := ev.GetBroadcast(); b != nil && b.TeamId != "" {
		teams = append(teams, b.TeamId)
	} else {
		for _, t := range st.Teams {
			teams = append(teams, t.Id)
		}
	}
	var cmds []tea.Cmd
	for _, team := range teams {
		cmds = append(cmds, m.loadThreadTotals(server, team))
		if m.threads != nil && m.threads.server == server && m.threads.team == team {
			cmds = append(cmds, m.loadThreadsList(server, team))
		}
	}
	return tea.Batch(cmds...)
}

func (m *Model) markThreadRead(ref chanRef, rootID string) tea.Cmd {
	st := m.servers[ref.server]
	if st == nil || !st.CRT {
		return nil
	}
	sess, team := m.byName[ref.server], st.ThreadTeam(ref.channel)
	return func() tea.Msg {
		if err := sess.MarkThreadRead(m.ctx, team, rootID); err != nil {
			return errMsg{fmt.Errorf("marking thread read: %w", err)}
		}
		return nil
	}
}

func (m *Model) threadsKey(msg tea.KeyPressMsg) tea.Cmd {
	t := m.threads
	switch msg.String() {
	case "j", "down":
		t.sel = min(t.sel+1, max(len(t.items)-1, 0))
	case "k", "up":
		t.sel = max(t.sel-1, 0)
	case "g", "home":
		t.sel = 0
	case "G", "end":
		t.sel = max(len(t.items)-1, 0)
	case "enter", "r":
		if t.sel >= len(t.items) || t.items[t.sel].Post == nil {
			return nil
		}
		root := t.items[t.sel].Post
		m.current = chanRef{t.server, root.ChannelId}
		m.lastChannel[t.server] = root.ChannelId
		return m.openThread(root)
	case "esc", "i":
		return m.closeThreadsList()
	}
	return nil
}

func (m *Model) renderThreadsList(width, height int) string {
	t := m.threads
	st := m.servers[t.server]
	var lines []string
	var lineItem []int
	switch {
	case t.loading && t.items == nil:
		lines = append(lines, faint.Render(" Loading threads…"))
		lineItem = append(lineItem, -1)
	case len(t.items) == 0:
		lines = append(lines, faint.Render(" You are not following any threads in this team."))
		lineItem = append(lineItem, -1)
	}

	selStart, selEnd := 0, 0
	for i, it := range t.items {
		if it.Post == nil {
			continue
		}
		unread := it.UnreadReplies > 0 || it.UnreadMentions > 0
		where := "#" + st.ChannelName(it.Post.ChannelId)
		if ch := st.Channels[it.Post.ChannelId]; ch != nil && ch.IsGroupOrDirect() {
			where = "@" + st.ChannelName(it.Post.ChannelId)
		}
		head := where + "  " + st.Username(it.Post.UserId)
		meta := fmt.Sprintf("  %d %s · %s", it.ReplyCount, plural(it.ReplyCount, "reply", "replies"), ago(it.LastReplyAt))

		marker := "  "
		if unread {
			head = unreadStyle.Render(head)
			marker = unreadStyle.Render("• ")
		} else {
			head = bold.Render(head)
		}
		badge := ""
		switch {
		case it.UnreadMentions > 0:
			badge = mentionTag.Render(fmt.Sprintf(" %d ", it.UnreadMentions))
		case it.UnreadReplies > 0:
			badge = unreadStyle.Render(fmt.Sprintf("%d new", it.UnreadReplies))
		}
		room := width - lipgloss.Width(badge) - 1
		first := padRight(truncate(marker+head+faint.Render(meta), room), room) + " " + badge
		excerpt := strings.Join(strings.Fields(emoji.Replace(it.Post.Message)), " ")
		second := "    " + faint.Render(truncate(excerpt, width-4))

		if i == t.sel {
			selStart = len(lines)
			first = selected.Render(padRight(ansi.Strip(first), width))
		}
		lines = append(lines, first, second, "")
		lineItem = append(lineItem, i, i, -1)
		if i == t.sel {
			selEnd = len(lines)
		}
	}

	start := 0
	if selEnd > height {
		start = selEnd - height
	}
	start = min(start, selStart)
	lines = lines[min(start, len(lines)):]
	t.lineItem = lineItem[min(start, len(lineItem)):]
	for len(lines) < height {
		lines = append(lines, "")
	}
	return strings.Join(lines[:height], "\n")
}

func ago(ms int64) string {
	if ms == 0 {
		return "no replies yet"
	}
	d := time.Since(time.UnixMilli(ms))
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	case d < 7*24*time.Hour:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	}
	return time.UnixMilli(ms).Local().Format("2 Jan")
}
