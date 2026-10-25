package ui

import (
	"fmt"
	neturl "net/url"
	"os/exec"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

const (
	doubleClick = 400 * time.Millisecond
	wheelLines  = 3
)

// zone is a screen rectangle that reacts to the mouse; handlers get
// coordinates relative to its top-left corner.
type zone struct {
	x, y, w, h int
	click      func(x, y int, double bool) tea.Cmd
	wheel      func(dir int) tea.Cmd
}

type lastClick struct {
	x, y int
	at   time.Time
}

func (m *Model) zoneAt(x, y int) *zone {
	for i := len(m.zones) - 1; i >= 0; i-- {
		z := &m.zones[i]
		if x >= z.x && x < z.x+z.w && y >= z.y && y < z.y+z.h {
			return z
		}
	}
	return nil
}

func (m *Model) onClick(ms tea.Mouse) tea.Cmd {
	if ms.Button != tea.MouseLeft || m.deleting != nil {
		return nil
	}
	double := ms.X == m.lastClick.x && ms.Y == m.lastClick.y && time.Since(m.lastClick.at) < doubleClick
	m.lastClick = lastClick{ms.X, ms.Y, time.Now()}
	if double {
		m.lastClick.at = time.Time{}
	}
	z := m.zoneAt(ms.X, ms.Y)
	if z == nil || z.click == nil {
		return nil
	}
	m.notice, m.status = "", ""
	return z.click(ms.X-z.x, ms.Y-z.y, double)
}

func (m *Model) onWheel(ms tea.Mouse) tea.Cmd {
	dir := 0
	switch ms.Button {
	case tea.MouseWheelUp:
		dir = -1
	case tea.MouseWheelDown:
		dir = 1
	}
	z := m.zoneAt(ms.X, ms.Y)
	if dir == 0 || z == nil || z.wheel == nil {
		return nil
	}
	return z.wheel(dir)
}

func (m *Model) sidebarZone(width, height int) {
	m.zones = append(m.zones, zone{
		w: width, h: height,
		click: func(_, y int, _ bool) tea.Cmd {
			if m.switcher.open || m.picker.open || m.relogin != nil {
				return nil
			}
			rows := m.rows()
			if y >= len(m.sidebarDrawn) || m.sidebarDrawn[y] >= len(rows) {
				return nil
			}
			i := m.sidebarDrawn[y]
			if m.focus == focusSidebar {
				m.cursor = i
			}
			return m.activateRow(rows[i])
		},
		wheel: func(dir int) tea.Cmd {
			m.sidebarFree = true
			m.sidebarOffset = max(m.sidebarOffset+dir*wheelLines, 0)
			return nil
		},
	})
}

// mainZones registers the message pane (or the overlay replacing it), the
// suggestion popup and the message box.
func (m *Model) mainZones(x, bodyY, bodyH, inputY int) {
	w := m.mainWidth()
	body := zone{x: x, y: bodyY, w: w, h: bodyH}
	switch {
	case m.relogin != nil:
		return
	case m.threads != nil && m.thread == nil && !m.switcher.open:
		body.click, body.wheel = m.threadsListClick, m.threadsListWheel
	case m.search != nil && m.thread == nil && !m.switcher.open && !m.picker.open:
		body.click = m.listClick(func(i int) tea.Cmd {
			if i >= len(m.search.lineItem) || m.search.lineItem[i] < 0 {
				return nil
			}
			return m.openSearchResult(m.search.lineItem[i])
		})
		body.wheel = func(dir int) tea.Cmd {
			m.search.sel = max(min(m.search.sel+dir, len(m.search.posts)-1), 0)
			return nil
		}
		m.zones = append(m.zones, body)
		return
	case m.picker.open:
		body.click = m.listClick(func(i int) tea.Cmd {
			m.picker.sel = m.picker.start + i
			return m.pickerKey(tea.KeyPressMsg{Code: tea.KeyEnter})
		})
		body.wheel = func(dir int) tea.Cmd {
			m.picker.sel = max(m.picker.sel+dir, 0)
			return nil
		}
		m.zones = append(m.zones, body)
		return
	case m.switcher.open:
		body.click = m.listClick(func(i int) tea.Cmd {
			m.switcher.sel = m.switcher.start + i
			return m.switcherKey(tea.KeyPressMsg{Code: tea.KeyEnter})
		})
		body.wheel = func(dir int) tea.Cmd {
			m.switcher.sel = max(m.switcher.sel+dir, 0)
			return nil
		}
		m.zones = append(m.zones, body)
		return
	default:
		body.click, body.wheel = m.messagesClick, m.messagesWheel
	}
	m.zones = append(m.zones, body)

	if m.completion.active() {
		m.zones = append(m.zones, zone{
			x: x, y: bodyY + bodyH + 1, w: w, h: len(m.completion.items),
			click: func(_, y int, _ bool) tea.Cmd {
				m.completion.sel = y
				m.acceptCompletion()
				return nil
			},
		})
	}
	m.zones = append(m.zones, zone{
		x: x, y: inputY, w: w, h: m.input.Height() + inputBox.GetBorderTopSize(),
		click: func(int, int, bool) tea.Cmd { return m.focusInput() },
	})
}

// listClick skips the prompt and rule above switcher and picker items.
func (m *Model) listClick(pick func(i int) tea.Cmd) func(int, int, bool) tea.Cmd {
	return func(_, y int, _ bool) tea.Cmd {
		if y < 2 {
			return nil
		}
		return pick(y - 2)
	}
}

func (m *Model) focusInput() tea.Cmd {
	switch m.focus {
	case focusInput:
		return nil
	case focusMessages:
		return m.leaveMessages()
	}
	m.focus = focusInput
	m.refreshMessages()
	return m.input.Focus()
}

func (m *Model) messagesClick(x, y int, double bool) tea.Cmd {
	line := m.vp.YOffset() + y
	if line >= len(m.msgLines) {
		return nil
	}
	if url := linkAt(m.msgLines[line], x); url != "" {
		return openURL(url)
	}
	for _, s := range m.spans {
		if line < s.start || line >= s.end {
			continue
		}
		for _, h := range s.reactions {
			if h.line == line && x >= h.x0 && x < h.x1 {
				return m.toggleReaction(m.current, s.id, h.emoji)
			}
		}
		i := m.indexOf(s.id)
		if i < 0 {
			return nil
		}
		if double {
			return m.openThread(m.visiblePosts()[i])
		}
		m.selected = s.id
		if m.focus != focusMessages {
			m.focus = focusMessages
			m.input.Blur()
		}
		m.refreshMessages()
		return m.loadReactionUsers()
	}
	return nil
}

func (m *Model) messagesWheel(dir int) tea.Cmd {
	if dir < 0 {
		if m.vp.AtTop() {
			return m.loadOlderFromTop()
		}
		m.vp.ScrollUp(wheelLines)
		return nil
	}
	m.vp.ScrollDown(wheelLines)
	return nil
}

func (m *Model) threadsListClick(_, y int, _ bool) tea.Cmd {
	t := m.threads
	if y >= len(t.lineItem) || t.lineItem[y] < 0 {
		return nil
	}
	t.sel = t.lineItem[y]
	return m.threadsKey(tea.KeyPressMsg{Code: tea.KeyEnter})
}

func (m *Model) threadsListWheel(dir int) tea.Cmd {
	t := m.threads
	t.sel = max(min(t.sel+dir, len(t.items)-1), 0)
	return nil
}

// linkAt returns the OSC 8 hyperlink target under column x of a rendered line.
func linkAt(line string, x int) string {
	var url string
	col, state := 0, byte(0)
	for len(line) > 0 {
		seq, width, n, newState := ansi.DecodeSequence(line, state, nil)
		state = newState
		line = line[n:]
		if strings.HasPrefix(seq, "\x1b]8;") {
			url = oscLinkTarget(seq)
			continue
		}
		if width == 0 {
			continue
		}
		if x < col+width {
			return url
		}
		col += width
	}
	return ""
}

// oscLinkTarget extracts the URI from "ESC ] 8 ; params ; URI" ended by BEL or ST.
func oscLinkTarget(seq string) string {
	body := strings.TrimSuffix(strings.TrimSuffix(strings.TrimPrefix(seq, "\x1b]8;"), "\a"), "\x1b\\")
	_, uri, _ := strings.Cut(body, ";")
	return uri
}

func openURL(url string) tea.Cmd {
	if !openableURL(url) {
		return func() tea.Msg {
			return noticeMsg{err: fmt.Errorf("not opening %q: only http, https and mailto links open", url)}
		}
	}
	return func() tea.Msg {
		cmd := exec.Command("xdg-open", url)
		if err := cmd.Start(); err != nil {
			return noticeMsg{err: fmt.Errorf("xdg-open: %w", err)}
		}
		go func() { _ = cmd.Wait() }()
		return noticeMsg{text: "Opened " + url}
	}
}

func openableURL(raw string) bool {
	u, err := neturl.Parse(raw)
	if err != nil {
		return false
	}
	switch strings.ToLower(u.Scheme) {
	case "http", "https":
		return u.Host != ""
	case "mailto":
		return true
	}
	return false
}
