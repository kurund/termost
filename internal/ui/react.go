package ui

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/mattermost/mattermost/server/public/model"

	"github.com/kurund/termost/internal/emoji"
)

var (
	reactShortcut = regexp.MustCompile(`^\+:([a-z0-9_+\-]+):\s*$`)
	commonEmoji   = []string{"+1", "heart", "joy", "tada", "eyes", "white_check_mark", "raised_hands", "pray", "rocket", "100", "thinking_face", "-1"}
)

type picker struct {
	open   bool
	input  textinput.Model
	sel    int
	ref    chanRef
	postID string
	start  int
}

func newPicker() picker {
	in := textinput.New()
	in.SetVirtualCursor(false)
	in.Prompt = "React with: "
	in.Placeholder = "emoji name, Enter to toggle, Esc to cancel"
	return picker{input: in}
}

func (m *Model) openPicker(post *model.Post) tea.Cmd {
	m.picker.open = true
	m.picker.sel = 0
	m.picker.ref = m.current
	m.picker.postID = post.Id
	m.picker.input.Reset()
	m.picker.input.SetWidth(m.mainWidth() - lipgloss.Width(m.picker.input.Prompt) - 1)
	return m.picker.input.Focus()
}

func filterEmoji(query string) []string {
	if query == "" {
		return commonEmoji
	}
	type scoredName struct {
		name  string
		score int
	}
	best := map[string]scoredName{}
	tones := strings.Contains(query, "skin") || strings.Contains(query, "tone")
	for _, n := range emoji.Names() {
		if !tones && strings.Contains(n, "skin_tone") {
			continue
		}
		s, ok := fuzzyScore(query, n)
		if !ok {
			continue
		}
		glyph, _ := emoji.Lookup(n)
		if cur, seen := best[glyph]; !seen || s > cur.score || (s == cur.score && len(n) < len(cur.name)) {
			best[glyph] = scoredName{n, s}
		}
	}
	out := make([]scoredName, 0, len(best))
	for _, s := range best {
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].score != out[j].score {
			return out[i].score > out[j].score
		}
		return out[i].name < out[j].name
	})
	names := make([]string, len(out))
	for i, s := range out {
		names[i] = s.name
	}
	return names
}

func (m *Model) pickerKey(msg tea.KeyPressMsg) tea.Cmd {
	p := &m.picker
	items := filterEmoji(p.input.Value())
	switch msg.String() {
	case "esc":
		p.open = false
		return nil
	case "up", "ctrl+p":
		p.sel = max(p.sel-1, 0)
		return nil
	case "down", "ctrl+n", "tab":
		p.sel = min(p.sel+1, max(len(items)-1, 0))
		return nil
	case "enter":
		p.open = false
		if p.sel >= len(items) {
			return nil
		}
		return m.toggleReaction(p.ref, p.postID, items[p.sel])
	}
	var cmd tea.Cmd
	p.input, cmd = p.input.Update(msg)
	p.sel = 0
	return cmd
}

func (m *Model) findPost(id string) *model.Post {
	for _, p := range m.visiblePosts() {
		if p.Id == id {
			return p
		}
	}
	return nil
}

// loadReactionUsers fetches the names of anyone who reacted to the selected
// post and is not known yet.
func (m *Model) loadReactionUsers() tea.Cmd {
	st, sess := m.servers[m.current.server], m.byName[m.current.server]
	post := m.findPost(m.selected)
	if st == nil || post == nil || post.Metadata == nil {
		return nil
	}
	if m.requestedUsers == nil {
		m.requestedUsers = map[string]bool{}
	}
	var ids []string
	for _, r := range post.Metadata.Reactions {
		key := st.Name + "/" + r.UserId
		if _, known := st.Users[r.UserId]; !known && !m.requestedUsers[key] {
			m.requestedUsers[key] = true
			ids = append(ids, r.UserId)
		}
	}
	if len(ids) == 0 {
		return nil
	}
	server := st.Name
	return func() tea.Msg {
		users, err := sess.Users(m.ctx, ids)
		if err != nil {
			return nil
		}
		return usersMsg{server: server, users: users}
	}
}

func (m *Model) toggleReaction(ref chanRef, postID, name string) tea.Cmd {
	st, sess := m.servers[ref.server], m.byName[ref.server]
	remove := false
	if post := m.findPost(postID); post != nil {
		remove = st.HasReacted(post, name)
	}
	return func() tea.Msg {
		if err := sess.ToggleReaction(m.ctx, postID, name, remove); err != nil {
			return errMsg{fmt.Errorf("reaction :%s:: %w", name, err)}
		}
		return nil
	}
}

// reactToLatest handles the web app's "+:emoji:" shortcut, which reacts to
// the newest post in the channel or open thread.
func (m *Model) reactToLatest(text string) (tea.Cmd, bool) {
	match := reactShortcut.FindStringSubmatch(text)
	if match == nil {
		return nil, false
	}
	posts := m.visiblePosts()
	if len(posts) == 0 {
		return nil, true
	}
	return m.toggleReaction(m.current, posts[len(posts)-1].Id, match[1]), true
}

func (m *Model) renderPicker(width, height int) string {
	p := &m.picker
	items := filterEmoji(p.input.Value())
	p.sel = min(p.sel, max(len(items)-1, 0))
	lines := []string{p.input.View(), faint.Render(strings.Repeat("─", width))}
	listHeight := max(height-len(lines), 1)
	start := max(p.sel-listHeight+1, 0)
	p.start = start
	for i := start; i < len(items) && i < start+listHeight; i++ {
		glyph, _ := emoji.Lookup(items[i])
		line := "  " + glyph + "  :" + items[i] + ":"
		if i == p.sel {
			line = selected.Render(padRight(ansi.Strip(line), width))
		}
		lines = append(lines, line)
	}
	if len(items) == 0 {
		lines = append(lines, faint.Render("  No matches"))
	}
	for len(lines) < height {
		lines = append(lines, "")
	}
	return strings.Join(lines[:height], "\n")
}
