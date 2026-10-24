package ui

import (
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

type relogin struct {
	server string
	input  textinput.Model
	err    string
	busy   bool
}

type reloginDoneMsg struct {
	server string
	err    error
}

// checkExpired marks servers whose requests came back unauthorised and opens
// the password prompt for the first one.
func (m *Model) checkExpired() tea.Cmd {
	for _, sess := range m.sessions {
		if sess.TakeExpired() {
			m.expired[sess.Server.Name] = true
		}
	}
	if m.relogin != nil {
		return nil
	}
	for _, sess := range m.sessions {
		name := sess.Server.Name
		if m.expired[name] && !m.reloginDismissed[name] {
			return m.openRelogin(name)
		}
	}
	return nil
}

const (
	reloginIndent   = 2
	reloginInputRow = 3
)

func (m *Model) openRelogin(server string) tea.Cmd {
	in := textinput.New()
	in.SetVirtualCursor(false)
	in.Prompt = "Password: "
	in.EchoMode = textinput.EchoPassword
	in.SetWidth(m.mainWidth() - reloginIndent - lipgloss.Width(in.Prompt) - 1)
	m.relogin = &relogin{server: server, input: in}
	m.input.Blur()
	return m.relogin.input.Focus()
}

func (m *Model) reloginKey(msg tea.KeyPressMsg) tea.Cmd {
	r := m.relogin
	switch msg.String() {
	case "esc":
		m.reloginDismissed[r.server] = true
		m.relogin = nil
		m.status = r.server + ": session expired, press Ctrl+L to log in"
		return m.input.Focus()
	case "enter":
		if r.busy || r.input.Value() == "" {
			return nil
		}
		r.busy, r.err = true, ""
		sess, password := m.byName[r.server], r.input.Value()
		return func() tea.Msg {
			err := sess.Relogin(m.ctx, password)
			if err == nil && m.saveToken != nil {
				err = m.saveToken(sess.Server, sess.Token())
			}
			return reloginDoneMsg{server: sess.Server.Name, err: err}
		}
	}
	var cmd tea.Cmd
	r.input, cmd = r.input.Update(msg)
	return cmd
}

func (m *Model) onReloginDone(msg reloginDoneMsg) tea.Cmd {
	r := m.relogin
	if r == nil || r.server != msg.server {
		return nil
	}
	if msg.err != nil {
		r.busy, r.err = false, msg.err.Error()
		r.input.Reset()
		return nil
	}
	m.relogin = nil
	delete(m.expired, msg.server)
	delete(m.reloginDismissed, msg.server)
	m.status = ""
	return tea.Batch(m.input.Focus(), m.loadServer(m.byName[msg.server]))
}

// reopenRelogin is bound to Ctrl+L for a prompt dismissed with Esc.
func (m *Model) reopenRelogin() tea.Cmd {
	for _, sess := range m.sessions {
		name := sess.Server.Name
		if m.expired[name] {
			delete(m.reloginDismissed, name)
			return m.openRelogin(name)
		}
	}
	return nil
}

func (m *Model) renderRelogin(width, height int) string {
	r := m.relogin
	sess := m.byName[r.server]
	lines := []string{
		"",
		bold.Render("  Session expired for "+r.server) + faint.Render("  ("+sess.Server.Username+" at "+sess.Server.URL+")"),
		"",
		strings.Repeat(" ", reloginIndent) + r.input.View(),
		"",
	}
	switch {
	case r.busy:
		lines = append(lines, faint.Render("  Logging in…"))
	case r.err != "":
		lines = append(lines, errorStyle.Render("  "+r.err))
	default:
		lines = append(lines, faint.Render("  Enter to log in · Esc to dismiss"))
	}
	for i := range lines {
		lines[i] = truncate(lines[i], width)
	}
	for len(lines) < height {
		lines = append(lines, "")
	}
	return strings.Join(lines[:height], "\n")
}
