package ui

import (
	"fmt"

	tea "charm.land/bubbletea/v2"
	"github.com/mattermost/mattermost/server/public/model"
)

type threadView struct {
	ref    chanRef
	rootID string
	posts  []*model.Post
}

type threadMsg struct {
	ref    chanRef
	rootID string
	posts  []*model.Post
	users  map[string]*model.User
	err    error
}

// visiblePosts is what the message pane shows: the open thread or the channel.
func (m *Model) visiblePosts() []*model.Post {
	if m.thread != nil {
		return m.thread.posts
	}
	st := m.servers[m.current.server]
	if st == nil {
		return nil
	}
	if p := st.Posts[m.current.channel]; p != nil {
		return p.List
	}
	return nil
}

func (m *Model) selectedID() string {
	if m.focus != focusMessages {
		return ""
	}
	return m.selected
}

func (m *Model) enterMessages() tea.Cmd {
	posts := m.visiblePosts()
	if len(posts) == 0 {
		return nil
	}
	m.focus = focusMessages
	m.input.Blur()
	if m.indexOf(m.selected) < 0 {
		m.selected = posts[len(posts)-1].Id
	}
	m.refreshMessages()
	m.ensureSelectedVisible()
	return m.loadReactionUsers()
}

func (m *Model) leaveMessages() tea.Cmd {
	m.focus = focusInput
	m.refreshMessages()
	return m.input.Focus()
}

func (m *Model) indexOf(id string) int {
	for i, p := range m.visiblePosts() {
		if p.Id == id {
			return i
		}
	}
	return -1
}

func (m *Model) messagesKey(msg tea.KeyPressMsg) tea.Cmd {
	posts := m.visiblePosts()
	if len(posts) == 0 {
		return m.leaveMessages()
	}
	i := m.indexOf(m.selected)
	if i < 0 {
		i = len(posts) - 1
	}
	switch msg.String() {
	case "j", "down":
		i++
	case "k", "up":
		if i == 0 {
			return m.loadOlderFromTop()
		}
		i--
	case "g", "home":
		i = 0
	case "G", "end":
		i = len(posts) - 1
	case "enter", "r":
		return m.openThread(posts[i])
	case "+":
		return m.openPicker(posts[i])
	case "o":
		return m.openAttachments(posts[i])
	case "y":
		return m.copyText(posts[i].Message, "message")
	case "Y":
		return m.copyText(m.permalink(posts[i]), "link")
	case "e":
		return m.startEdit(posts[i])
	case "d":
		return m.confirmDelete(posts[i])
	case "esc", "i":
		return m.leaveMessages()
	default:
		return nil
	}
	m.selected = posts[max(min(i, len(posts)-1), 0)].Id
	m.refreshMessages()
	m.ensureSelectedVisible()
	return m.loadReactionUsers()
}

func (m *Model) loadOlderFromTop() tea.Cmd {
	if m.thread != nil {
		return nil
	}
	st := m.servers[m.current.server]
	p := st.Posts[m.current.channel]
	if p == nil || !p.HasMore || m.loadingOlder || len(p.List) == 0 {
		return nil
	}
	m.loadingOlder = true
	return m.loadPosts(m.current, p.List[0].Id)
}

func (m *Model) ensureSelectedVisible() {
	for _, s := range m.spans {
		if s.id != m.selected {
			continue
		}
		top, height := m.vp.YOffset(), m.vp.Height()
		switch {
		case s.start < top:
			m.vp.SetYOffset(s.start)
		case s.end > top+height:
			m.vp.SetYOffset(max(s.end-height, s.start))
		}
		return
	}
}

func (m *Model) openThread(post *model.Post) tea.Cmd {
	if post.Type != "" && post.Type != model.PostTypeDefault {
		return nil
	}
	rootID := post.Id
	if post.RootId != "" {
		rootID = post.RootId
	}
	m.channelSelected = m.selected
	m.thread = &threadView{ref: m.current, rootID: rootID}
	m.selected = post.Id
	m.input.Placeholder = "Reply in thread"
	m.focus = focusInput
	m.refreshMessages()
	return tea.Batch(m.input.Focus(), m.loadThread(m.current, rootID), m.markThreadRead(m.current, rootID))
}

func (m *Model) closeThread() tea.Cmd {
	m.thread = nil
	if m.search != nil {
		m.selected = ""
		m.input.Blur()
		m.input.Placeholder = "Message " + m.channelTitle()
		return m.search.input.Focus()
	}
	if m.threads != nil {
		m.focus = focusMessages
		m.input.Blur()
		return m.loadThreadsList(m.threads.server, m.threads.team)
	}
	m.selected = m.channelSelected
	m.input.Placeholder = "Message " + m.channelTitle()
	m.refreshMessages()
	m.ensureSelectedVisible()
	return nil
}

func (m *Model) loadThread(ref chanRef, rootID string) tea.Cmd {
	sess := m.byName[ref.server]
	return func() tea.Msg {
		posts, err := sess.Thread(m.ctx, rootID)
		if err != nil {
			return threadMsg{ref: ref, rootID: rootID, err: err}
		}
		seen := map[string]bool{}
		var ids []string
		for _, p := range posts {
			if !seen[p.UserId] {
				seen[p.UserId] = true
				ids = append(ids, p.UserId)
			}
		}
		users, err := sess.Users(m.ctx, ids)
		return threadMsg{ref: ref, rootID: rootID, posts: posts, users: users, err: err}
	}
}

func (m *Model) onThread(msg threadMsg) {
	if m.thread == nil || m.thread.rootID != msg.rootID {
		return
	}
	if msg.err != nil {
		m.setError(fmt.Errorf("loading thread: %w", msg.err))
		return
	}
	m.servers[msg.ref.server].AddUsers(msg.users)
	m.thread.posts = msg.posts
	m.refreshMessages()
	m.vp.GotoBottom()
}

// threadEvent applies a websocket post change to the open thread.
func (m *Model) threadEvent(server string, kind model.WebsocketEventType, post *model.Post) {
	t := m.thread
	if t == nil || t.ref.server != server || t.posts == nil || (post.Id != t.rootID && post.RootId != t.rootID) {
		return
	}
	for i, p := range t.posts {
		if p.Id != post.Id {
			continue
		}
		switch kind {
		case model.WebsocketEventPostEdited:
			t.posts[i] = post
		case model.WebsocketEventPostDeleted:
			t.posts = append(t.posts[:i], t.posts[i+1:]...)
		}
		return
	}
	if kind == model.WebsocketEventPosted {
		t.posts = append(t.posts, post)
	}
}
