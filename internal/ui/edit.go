package ui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/mattermost/mattermost/server/public/model"
)

type editState struct {
	ref    chanRef
	postID string
	draft  string
}

type editDoneMsg struct {
	edit editState
	text string
	err  error
}

type deleteDoneMsg struct {
	err error
}

func (m *Model) ownPost(post *model.Post, action string) bool {
	st := m.servers[m.current.server]
	if post.IsSystemMessage() || st == nil || post.UserId != st.Me.Id {
		m.notice = "You can only " + action + " your own messages"
		return false
	}
	return true
}

func (m *Model) startEdit(post *model.Post) tea.Cmd {
	if !m.ownPost(post, "edit") {
		return nil
	}
	draft := m.input.Value()
	if m.editing != nil {
		draft = m.editing.draft
	}
	m.editing = &editState{ref: m.current, postID: post.Id, draft: draft}
	m.input.SetValue(post.Message)
	m.input.Prompt = "✎ "
	m.layout()
	return m.leaveMessages()
}

func (m *Model) stopEdit(restore string) {
	m.editing = nil
	m.input.Prompt = "> "
	m.input.SetValue(restore)
	m.layout()
}

func (m *Model) saveEdit() tea.Cmd {
	e := *m.editing
	text := m.input.Value()
	if strings.TrimSpace(text) == "" {
		m.notice = "A message can't be empty; select it and press d to delete it"
		return nil
	}
	m.stopEdit(e.draft)
	sess := m.byName[e.ref.server]
	return func() tea.Msg {
		return editDoneMsg{edit: e, text: text, err: sess.EditPost(m.ctx, e.postID, text)}
	}
}

func (m *Model) onEditDone(msg editDoneMsg) {
	if msg.err == nil {
		return
	}
	m.setError(fmt.Errorf("edit failed, still editing: %w", msg.err))
	if m.editing == nil && msg.edit.ref == m.current {
		m.editing = &msg.edit
		m.input.Prompt = "✎ "
		m.input.SetValue(msg.text)
		m.layout()
	}
}

func (m *Model) confirmDelete(post *model.Post) tea.Cmd {
	if !m.ownPost(post, "delete") {
		return nil
	}
	m.deleting = &editState{ref: m.current, postID: post.Id}
	return nil
}

// deleteKey answers the delete confirmation: y deletes, anything else cancels.
func (m *Model) deleteKey(msg tea.KeyPressMsg) tea.Cmd {
	d := *m.deleting
	m.deleting = nil
	if msg.String() != "y" && msg.String() != "Y" {
		m.notice = "Delete cancelled"
		return nil
	}
	sess := m.byName[d.ref.server]
	return func() tea.Msg {
		return deleteDoneMsg{err: sess.DeletePost(m.ctx, d.postID)}
	}
}
