package ui

import (
	"errors"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/mattermost/mattermost/server/public/model"

	"github.com/kurund/termost/internal/clip"
)

type copyFallbackMsg struct {
	text, what string
}

func (m *Model) copyText(text, what string) tea.Cmd {
	if text == "" {
		m.notice = "Nothing to copy"
		return nil
	}
	return func() tea.Msg {
		err := clip.Copy(m.ctx, text)
		switch {
		case errors.Is(err, clip.ErrNoTool):
			return copyFallbackMsg{text: text, what: what}
		case err != nil:
			return noticeMsg{err: err}
		}
		return noticeMsg{text: "Copied " + what}
	}
}

// onCopyFallback asks the terminal to set the clipboard (OSC 52), which also
// works over SSH but cannot report whether the terminal allowed it.
func (m *Model) onCopyFallback(msg copyFallbackMsg) tea.Cmd {
	m.notice = "Copied " + msg.what + " via the terminal (OSC 52)"
	return tea.Raw(ansi.SetSystemClipboard(msg.text))
}

// permalink matches the web app's "Copy Link": DM and group posts use the
// user's first team.
func (m *Model) permalink(post *model.Post) string {
	st, sess := m.servers[m.current.server], m.byName[m.current.server]
	if st == nil || sess == nil {
		return ""
	}
	teamID := st.ThreadTeam(post.ChannelId)
	for _, t := range st.Teams {
		if t.Id == teamID {
			return sess.Server.URL + "/" + t.Name + "/pl/" + post.Id
		}
	}
	return ""
}
