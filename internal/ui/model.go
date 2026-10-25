package ui

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"
	"unicode"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/mattermost/mattermost/server/public/model"

	"github.com/kurund/termost/internal/config"
	"github.com/kurund/termost/internal/mm"
	"github.com/kurund/termost/internal/notify"
	"github.com/kurund/termost/internal/store"
)

const pageSize = 60

type focusArea int

const (
	focusInput focusArea = iota
	focusSidebar
	focusMessages
)

type chanRef struct {
	server  string
	channel string
}

type (
	serverLoadedMsg struct {
		server string
		st     *store.Server
		err    error
	}
	postsMsg struct {
		ref     chanRef
		posts   []*model.Post
		users   map[string]*model.User
		hasMore bool
		older   bool
		err     error
	}
	usersMsg struct {
		server string
		users  map[string]*model.User
	}
	wsMsg   struct{ mm.Event }
	connMsg struct {
		server string
		err    error
	}
	errMsg        struct{ err error }
	sendFailedMsg struct {
		ref  chanRef
		text string
		err  error
	}
)

type Model struct {
	ctx      context.Context
	sessions []*mm.Session
	notifier *notify.Notifier
	dmLimit  int
	byName   map[string]*mm.Session
	servers  map[string]*store.Server
	errs     map[string]string
	online   map[string]bool
	everUp   map[string]bool

	active         string
	lastChannel    map[string]string
	collapsed      map[string]bool
	cursor         int
	sidebarOffset  int
	sidebarDrawn   []int
	current        chanRef
	loadingOlder   bool
	focus          focusArea
	termFocused    bool
	search         *searchView
	mouse          bool
	zones          []zone
	lastClick      lastClick
	sidebarFree    bool
	requestedUsers map[string]bool
	msgLines       []string
	status         string
	notice         string

	width, height int
	laidOut       [2]int
	vp            viewport.Model
	input         textarea.Model
	switcher      switcher
	completion    completion

	thread          *threadView
	threads         *threadsList
	editing         *editState
	deleting        *editState
	picker          picker
	selected        string
	channelSelected string
	spans           []postSpan

	saveToken        func(config.Server, string) error
	expired          map[string]bool
	reloginDismissed map[string]bool
	relogin          *relogin
}

func New(ctx context.Context, sessions []*mm.Session, notifier *notify.Notifier, cfg config.UI, saveToken func(config.Server, string) error) *Model {
	input := textarea.New()
	input.ShowLineNumbers = false
	input.Prompt = "> "
	input.SetHeight(3)
	input.KeyMap.InsertNewline = key.NewBinding(key.WithKeys("shift+enter", "alt+enter", "ctrl+j"))
	input.SetVirtualCursor(false)
	input.Focus()

	m := &Model{
		ctx:         ctx,
		sessions:    sessions,
		notifier:    notifier,
		dmLimit:     cfg.DMLimit,
		mouse:       cfg.Mouse,
		active:      sessions[0].Server.Name,
		lastChannel: map[string]string{},
		byName:      map[string]*mm.Session{},
		servers:     map[string]*store.Server{},
		errs:        map[string]string{},
		online:      map[string]bool{},
		everUp:      map[string]bool{},
		collapsed:   map[string]bool{},
		termFocused: true,
		vp:          viewport.New(),
		input:       input,
		switcher:    newSwitcher(),
		picker:      newPicker(),
		saveToken:   saveToken,
		expired:     map[string]bool{},

		reloginDismissed: map[string]bool{},
	}
	for _, s := range sessions {
		m.byName[s.Server.Name] = s
	}
	return m
}

const statusPollInterval = time.Minute

type statusTickMsg struct{}

type statusesMsg struct {
	server   string
	statuses []*model.Status
}

func statusTick() tea.Cmd {
	return tea.Tick(statusPollInterval, func(time.Time) tea.Msg { return statusTickMsg{} })
}

func (m *Model) pollStatuses() tea.Cmd {
	var cmds []tea.Cmd
	for _, sess := range m.sessions {
		st := m.servers[sess.Server.Name]
		if st == nil {
			continue
		}
		ids := append(st.DMUserIDs(), st.Me.Id)
		cmds = append(cmds, func() tea.Msg {
			statuses, err := sess.Statuses(m.ctx, ids)
			if err != nil {
				slog.Warn("polling statuses failed", "server", sess.Server.Name, "err", err)
				return nil
			}
			return statusesMsg{server: sess.Server.Name, statuses: statuses}
		})
	}
	return tea.Batch(cmds...)
}

func (m *Model) Init() tea.Cmd {
	cmds := []tea.Cmd{statusTick()}
	for _, s := range m.sessions {
		cmds = append(cmds, m.loadServer(s))
	}
	return tea.Batch(cmds...)
}

func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	model, cmd := m.update(msg)
	if m.width > 0 && m.laidOut != [2]int{m.mainWidth(), max(m.height-m.chromeHeight(), 1)} {
		m.layout()
	}
	return model, tea.Batch(cmd, m.checkExpired())
}

func (m *Model) update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case reloginDoneMsg:
		return m, m.onReloginDone(msg)

	case threadMsg:
		m.onThread(msg)
		return m, nil

	case copyFallbackMsg:
		return m, m.onCopyFallback(msg)

	case editDoneMsg:
		m.onEditDone(msg)
		return m, nil

	case deleteDoneMsg:
		if msg.err != nil {
			m.setError(fmt.Errorf("delete failed: %w", msg.err))
		}
		return m, nil

	case threadsMsg:
		m.onThreadsList(msg)
		return m, nil

	case threadTotalsMsg:
		if st := m.servers[msg.server]; st != nil {
			st.SetThreadTotals(msg.team, msg.totals)
		}
		return m, nil

	case noticeMsg:
		m.notice = msg.text
		if msg.err != nil {
			m.notice = ""
			m.setError(msg.err)
		}
		return m, nil

	case sendFailedMsg:
		m.setError(fmt.Errorf("send failed, message kept in the input: %w", msg.err))
		if msg.ref == m.current && m.input.Value() == "" {
			m.input.SetValue(msg.text)
		}
		return m, nil

	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.layout()
		return m, nil

	case tea.FocusMsg:
		m.termFocused = true
		return m, tea.Batch(m.markViewed(m.current), m.dismiss(m.current))

	case tea.BlurMsg:
		m.termFocused = false
		return m, nil

	case tea.KeyPressMsg:
		return m, m.handleKey(msg)

	case searchResultsMsg:
		m.onSearchResults(msg)
		return m, nil

	case tea.MouseClickMsg:
		return m, m.onClick(tea.Mouse(msg))

	case tea.MouseWheelMsg:
		return m, m.onWheel(tea.Mouse(msg))

	case serverLoadedMsg:
		return m, m.onServerLoaded(msg)

	case postsMsg:
		m.onPosts(msg)
		return m, nil

	case statusTickMsg:
		return m, tea.Batch(m.pollStatuses(), statusTick())

	case statusesMsg:
		if st := m.servers[msg.server]; st != nil {
			st.SetStatuses(msg.statuses)
		}
		return m, nil

	case categoriesMsg:
		if st := m.servers[msg.server]; st != nil {
			st.SetCategories(msg.teamID, msg.cats)
		}
		return m, nil

	case usersFoundMsg:
		m.onUsersFound(msg)
		return m, nil

	case usersMsg:
		if st := m.servers[msg.server]; st != nil {
			st.AddUsers(msg.users)
			m.refreshMessages()
		}
		return m, nil

	case wsMsg:
		return m, m.onEvent(msg.Event)

	case connMsg:
		return m, m.onConn(msg)

	case errMsg:
		m.setError(msg.err)
		return m, nil
	}

	if m.relogin != nil {
		var cmd tea.Cmd
		m.relogin.input, cmd = m.relogin.input.Update(msg)
		return m, cmd
	}
	if m.switcher.open {
		var cmd tea.Cmd
		m.switcher.input, cmd = m.switcher.input.Update(msg)
		return m, cmd
	}
	if m.picker.open {
		var cmd tea.Cmd
		m.picker.input, cmd = m.picker.input.Update(msg)
		return m, cmd
	}
	if m.search != nil && m.thread == nil {
		var cmd tea.Cmd
		m.search.input, cmd = m.search.input.Update(msg)
		return m, cmd
	}
	if m.focus == focusInput {
		if paste, ok := msg.(tea.PasteMsg); ok && m.editing == nil && m.cursorAtEnd() {
			if paths, ok := droppedFiles(paste.Content); ok {
				draft := strings.TrimRightFunc(m.input.Value(), unicode.IsSpace)
				if draft == "" {
					m.input.SetValue("/upload " + strings.TrimSpace(paste.Content))
					m.notice = "Enter uploads " + filesLabel(paths)
				} else {
					m.input.SetValue(draft + " /upload " + strings.TrimSpace(paste.Content))
					m.notice = "Enter sends the message with " + filesLabel(paths)
				}
				return m, nil
			}
		}
		var cmd tea.Cmd
		m.input, cmd = m.input.Update(msg)
		if _, paste := msg.(tea.PasteMsg); paste {
			cmd = tea.Batch(cmd, m.updateCompletion())
		}
		return m, cmd
	}
	return m, nil
}

func (m *Model) handleKey(msg tea.KeyPressMsg) tea.Cmd {
	m.sidebarFree = false
	if msg.String() != "tab" {
		m.notice = ""
	}
	m.status = ""
	switch msg.String() {
	case "ctrl+c", "ctrl+q":
		return tea.Quit
	}
	if m.relogin != nil {
		return m.reloginKey(msg)
	}
	if m.deleting != nil {
		return m.deleteKey(msg)
	}
	if m.switcher.open {
		return m.switcherKey(msg)
	}
	if m.picker.open {
		return m.pickerKey(msg)
	}
	searching := m.search != nil && m.thread == nil
	if searching {
		if cmd, ok := m.searchKey(msg); ok {
			return cmd
		}
	}
	if m.focus == focusInput && m.completion.active() {
		if cmd, ok := m.completionKey(msg); ok {
			return cmd
		}
	}
	switch msg.String() {
	case "ctrl+k":
		return m.openSwitcher()
	case "ctrl+f":
		return m.openSearch("")
	case "ctrl+l":
		return m.reopenRelogin()
	case "tab", "shift+tab":
		if msg.String() == "tab" && m.focus == focusInput && m.completeUpload() {
			return nil
		}
		return m.cycleFocus(msg.String() == "tab")
	case "alt+k":
		return m.stepChannel(-1, false)
	case "alt+j":
		return m.stepChannel(1, false)
	case "alt+K", "alt+shift+k":
		return m.stepChannel(-1, true)
	case "alt+J", "alt+shift+j":
		return m.stepChannel(1, true)
	case "alt+t":
		return m.toggleThreads()
	case "alt+1", "alt+2", "alt+3", "alt+4", "alt+5", "alt+6", "alt+7", "alt+8", "alt+9":
		i := int(msg.String()[4] - '1')
		if i < len(m.sessions) {
			return m.switchServer(m.sessions[i].Server.Name)
		}
		return nil
	case "pgup":
		return m.pageUp()
	case "pgdown":
		m.vp.PageDown()
		return nil
	}

	if searching {
		return nil
	}
	if m.threads != nil && m.thread == nil && m.focus != focusSidebar {
		return m.threadsKey(msg)
	}
	switch m.focus {
	case focusSidebar:
		return m.sidebarKey(msg)
	case focusMessages:
		return m.messagesKey(msg)
	}

	switch msg.String() {
	case "enter":
		return m.send()
	case "up":
		if m.input.Value() == "" {
			return m.enterMessages()
		}
	case "esc":
		if m.editing != nil {
			m.stopEdit(m.editing.draft)
			return nil
		}
		if m.thread != nil {
			return m.closeThread()
		}
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return tea.Batch(cmd, m.updateCompletion())
}

// cycleFocus moves between the input, the message list and the sidebar.
func (m *Model) cycleFocus(forward bool) tea.Cmd {
	order := []focusArea{focusInput, focusMessages, focusSidebar}
	i := 0
	for j, f := range order {
		if f == m.focus {
			i = j
		}
	}
	step := 1
	if !forward {
		step = len(order) - 1
	}
	next := order[(i+step)%len(order)]
	if next == focusMessages && len(m.visiblePosts()) == 0 {
		next = order[(i+2*step)%len(order)]
	}
	m.input.Blur()
	switch next {
	case focusMessages:
		return m.enterMessages()
	case focusSidebar:
		m.focus = focusSidebar
		m.cursor = max(m.rowIndex(m.current), 0)
		m.refreshMessages()
		return nil
	}
	return m.leaveMessages()
}

func (m *Model) sidebarKey(msg tea.KeyPressMsg) tea.Cmd {
	rows := m.rows()
	switch msg.String() {
	case "up", "k":
		m.cursor = max(m.cursor-1, 0)
		if rows[m.cursor].kind == rowSpacer {
			m.cursor = max(m.cursor-1, 0)
		}
	case "down", "j":
		m.cursor = min(m.cursor+1, len(rows)-1)
		if rows[m.cursor].kind == rowSpacer {
			m.cursor = min(m.cursor+1, len(rows)-1)
		}
	case "esc":
		m.focus = focusInput
		return m.input.Focus()
	case "enter", "space", "right", "l", "left", "h":
		if m.cursor >= len(rows) {
			return nil
		}
		return m.activateRow(rows[m.cursor])
	}
	return nil
}

func (m *Model) activateRow(r row) tea.Cmd {
	switch r.kind {
	case rowSpacer:
		return nil
	case rowThreads:
		return m.openThreadsList(r.server, r.team)
	case rowServer:
		cmd := m.switchServer(r.server)
		m.cursor = max(m.rowIndex(m.current), 0)
		return cmd
	}
	if r.kind != rowChannel {
		m.collapsed[r.collapseKey()] = !m.isCollapsed(r)
		return nil
	}
	m.focus = focusInput
	return tea.Batch(m.open(chanRef{r.server, r.channel}), m.input.Focus())
}

// stepChannel moves to the previous or next visible channel, optionally
// skipping channels without unread messages.
func (m *Model) stepChannel(dir int, unreadOnly bool) tea.Cmd {
	rows := m.rows()
	if len(rows) == 0 {
		return nil
	}
	start := m.rowIndex(m.current)
	if m.threads != nil {
		start = m.threadsRowIndex(rows)
	}
	for i := 1; i <= len(rows); i++ {
		r := rows[((start+dir*i)%len(rows)+len(rows))%len(rows)]
		st := m.servers[r.server]
		switch r.kind {
		case rowThreads:
			if totals := st.ThreadTotals(r.team); unreadOnly && totals.Unread == 0 && totals.Mentions == 0 {
				continue
			}
			return m.openThreadsList(r.server, r.team)
		case rowChannel:
			if u, mentions := st.Unread(r.channel); unreadOnly && mentions == 0 && (u == 0 || st.Muted(r.channel)) {
				continue
			}
			return m.open(chanRef{r.server, r.channel})
		}
	}
	return nil
}

func (m *Model) threadsRowIndex(rows []row) int {
	for i, r := range rows {
		if r.kind == rowThreads && r.server == m.threads.server && r.team == m.threads.team {
			return i
		}
	}
	return -1
}

func (m *Model) switchServer(name string) tea.Cmd {
	if name == m.active {
		return nil
	}
	m.active = name
	m.sidebarOffset = 0
	st := m.servers[name]
	if st == nil {
		m.current = chanRef{server: name}
		m.refreshMessages()
		return nil
	}
	if id := m.lastChannel[name]; id != "" {
		if _, ok := st.Channels[id]; ok {
			return m.open(chanRef{name, id})
		}
	}
	if ref, ok := defaultChannel(name, st); ok {
		return m.open(ref)
	}
	return nil
}

func (m *Model) serverIndex(name string) int {
	for i, s := range m.sessions {
		if s.Server.Name == name {
			return i
		}
	}
	return -1
}

func (m *Model) open(ref chanRef) tea.Cmd {
	m.current = ref
	m.active = ref.server
	m.lastChannel[ref.server] = ref.channel
	m.thread, m.threads, m.search, m.selected = nil, nil, nil, ""
	if m.editing != nil {
		m.stopEdit(m.editing.draft)
	}
	m.deleting = nil
	if m.focus == focusMessages {
		m.focus = focusInput
		m.input.Focus()
	}
	m.setCompletion(0, "", nil)
	m.status = ""
	m.input.Placeholder = "Message " + m.channelTitle()
	m.refreshMessages()
	m.vp.GotoBottom()

	cmds := []tea.Cmd{m.markViewed(ref), m.dismiss(ref)}
	if _, cached := m.servers[ref.server].Posts[ref.channel]; !cached {
		cmds = append(cmds, m.loadPosts(ref, ""))
	}
	return tea.Batch(cmds...)
}

func (m *Model) markViewed(ref chanRef) tea.Cmd {
	st := m.servers[ref.server]
	if st == nil || !m.termFocused {
		return nil
	}
	if u, mentions := st.Unread(ref.channel); u == 0 && mentions == 0 {
		return nil
	}
	st.MarkViewed(ref.channel)
	sess := m.byName[ref.server]
	return func() tea.Msg {
		if err := sess.View(m.ctx, ref.channel); err != nil {
			return errMsg{fmt.Errorf("%s: mark read: %w", ref.server, err)}
		}
		return nil
	}
}

func (m *Model) send() tea.Cmd {
	if m.editing != nil {
		return m.saveEdit()
	}
	text := m.input.Value()
	if strings.TrimSpace(text) == "" || m.current.channel == "" {
		return nil
	}
	if cmd, ok := m.reactToLatest(text); ok {
		m.input.Reset()
		return cmd
	}
	cmd, rest, handled := m.slash(text)
	if handled {
		m.input.Reset()
		return cmd
	}
	text = rest
	m.input.Reset()
	m.setCompletion(0, "", nil)
	ref, sess, rootID := m.current, m.byName[m.current.server], ""
	if m.thread != nil {
		rootID = m.thread.rootID
	}
	return func() tea.Msg {
		if err := sess.Send(m.ctx, ref.channel, rootID, text); err != nil {
			return sendFailedMsg{ref: ref, text: text, err: err}
		}
		return nil
	}
}

func (m *Model) pageUp() tea.Cmd {
	st := m.servers[m.current.server]
	if st == nil {
		return nil
	}
	if !m.vp.AtTop() {
		m.vp.PageUp()
		return nil
	}
	return m.loadOlderFromTop()
}

func (m *Model) loadServer(sess *mm.Session) tea.Cmd {
	return func() tea.Msg {
		st, err := sess.Load(m.ctx)
		return serverLoadedMsg{server: sess.Server.Name, st: st, err: err}
	}
}

func (m *Model) loadPosts(ref chanRef, beforeID string) tea.Cmd {
	sess, crt := m.byName[ref.server], m.servers[ref.server].CRT
	return func() tea.Msg {
		posts, hasMore, err := sess.Posts(m.ctx, ref.channel, beforeID, pageSize, crt)
		if err != nil {
			return postsMsg{ref: ref, older: beforeID != "", err: err}
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
		return postsMsg{ref: ref, posts: posts, users: users, hasMore: hasMore, older: beforeID != "", err: err}
	}
}

func (m *Model) loadUsers(server string, ids []string) tea.Cmd {
	sess := m.byName[server]
	return func() tea.Msg {
		users, err := sess.Users(m.ctx, ids)
		if err != nil {
			return errMsg{err}
		}
		return usersMsg{server: server, users: users}
	}
}

func (m *Model) onServerLoaded(msg serverLoadedMsg) tea.Cmd {
	if msg.err != nil {
		m.errs[msg.server] = msg.err.Error()
		m.setError(fmt.Errorf("%s: %w", msg.server, msg.err))
		return nil
	}
	delete(m.errs, msg.server)
	m.servers[msg.server] = msg.st

	if m.current.server == msg.server {
		if _, ok := msg.st.Channels[m.current.channel]; ok {
			return m.loadPosts(m.current, "")
		}
		m.current = chanRef{server: msg.server}
	}
	if msg.server == m.active && m.current.channel == "" {
		if ref, ok := defaultChannel(msg.server, msg.st); ok {
			return m.open(ref)
		}
	}
	return nil
}

func defaultChannel(server string, st *store.Server) (chanRef, bool) {
	for _, team := range st.Teams {
		ids := st.TeamChannels(team.Id)
		for _, id := range ids {
			if st.Channels[id].Name == model.DefaultChannelName {
				return chanRef{server, id}, true
			}
		}
		if len(ids) > 0 {
			return chanRef{server, ids[0]}, true
		}
	}
	return chanRef{}, false
}

func (m *Model) onPosts(msg postsMsg) {
	if msg.older {
		m.loadingOlder = false
	}
	if msg.err != nil {
		m.setError(fmt.Errorf("loading messages: %w", msg.err))
		return
	}
	st := m.servers[msg.ref.server]
	if st == nil {
		return
	}
	st.AddUsers(msg.users)
	if !msg.older {
		st.SetPosts(msg.ref.channel, msg.posts, msg.hasMore)
		if msg.ref == m.current {
			m.refreshMessages()
			m.vp.GotoBottom()
		}
		return
	}

	st.PrependPosts(msg.ref.channel, msg.posts, msg.hasMore)
	if msg.ref != m.current {
		return
	}
	before, offset := m.vp.TotalLineCount(), m.vp.YOffset()
	m.refreshMessages()
	m.vp.SetYOffset(offset + m.vp.TotalLineCount() - before)
}

func (m *Model) onEvent(ev mm.Event) tea.Cmd {
	st := m.servers[ev.Server]
	if st == nil {
		return nil
	}
	data := ev.GetData()

	switch ev.EventType() {
	case model.WebsocketEventPosted:
		post := decodePost(data)
		if post == nil {
			return nil
		}
		if _, known := st.Channels[post.ChannelId]; !known {
			return m.loadServer(m.byName[ev.Server])
		}
		ref := chanRef{ev.Server, post.ChannelId}
		viewing := ref == m.current && m.termFocused
		mentionsMe := mentions(data, st.Me.Id)
		st.AddPost(post, mentionsMe, viewing)
		m.threadEvent(ev.Server, model.WebsocketEventPosted, post)

		var cmds []tea.Cmd
		if notify.ShouldNotify(notify.Input{
			Me:         st.Me,
			Channel:    st.Channels[post.ChannelId],
			Member:     st.Members[post.ChannelId],
			Post:       post,
			MentionsMe: mentionsMe,
			Viewing:    viewing,
		}) {
			sender, _ := data["sender_name"].(string)
			cmds = append(cmds, m.notify(ref, post, sender))
		}
		if missing := st.MissingUsers([]*model.Post{post}); len(missing) > 0 {
			cmds = append(cmds, m.loadUsers(ev.Server, missing))
		}
		if m.thread != nil && post.RootId == m.thread.rootID && m.termFocused {
			cmds = append(cmds, m.markThreadRead(m.thread.ref, m.thread.rootID))
		}
		if ref == m.current {
			atBottom := m.vp.AtBottom()
			m.refreshMessages()
			if atBottom {
				m.vp.GotoBottom()
			}
			if viewing && post.UserId != st.Me.Id {
				cmds = append(cmds, m.viewOnServer(ref))
			}
		}
		return tea.Batch(cmds...)

	case model.WebsocketEventPostEdited:
		if post := decodePost(data); post != nil {
			st.EditPost(post)
			m.threadEvent(ev.Server, model.WebsocketEventPostEdited, post)
			m.refreshMessages()
		}

	case model.WebsocketEventPostDeleted:
		if post := decodePost(data); post != nil {
			st.DeletePost(post)
			if m.thread != nil && m.thread.ref.server == ev.Server && post.Id == m.thread.rootID {
				return m.closeThread()
			}
			if m.editing != nil && m.editing.postID == post.Id {
				m.stopEdit(m.editing.draft)
				m.notice = "The message you were editing was deleted"
			}
			m.threadEvent(ev.Server, model.WebsocketEventPostDeleted, post)
			m.refreshMessages()
		}

	case model.WebsocketEventReactionAdded, model.WebsocketEventReactionRemoved:
		raw, _ := data["reaction"].(string)
		var r model.Reaction
		if err := json.Unmarshal([]byte(raw), &r); err != nil || r.PostId == "" {
			return nil
		}
		added := ev.EventType() == model.WebsocketEventReactionAdded
		if b := ev.GetBroadcast(); b != nil {
			st.ApplyReaction(b.ChannelId, &r, added)
		}
		if m.thread != nil && m.thread.ref.server == ev.Server {
			store.ApplyReaction(m.thread.posts, &r, added)
		}
		m.refreshMessages()

	case model.WebsocketEventThreadUpdated, model.WebsocketEventThreadReadChanged, model.WebsocketEventThreadFollowChanged:
		return m.onThreadEvent(ev.Server, ev.WebSocketEvent)

	case model.WebsocketEventMultipleChannelsViewed:
		if times, ok := data["channel_times"].(map[string]any); ok {
			for id := range times {
				st.MarkViewed(id)
			}
		}

	case model.WebsocketEventStatusChange:
		userID, _ := data["user_id"].(string)
		status, _ := data["status"].(string)
		if userID != "" && status != "" {
			st.SetStatuses([]*model.Status{{UserId: userID, Status: status}})
		}

	case model.WebsocketEventPreferencesChanged, model.WebsocketEventPreferencesDeleted:
		raw, _ := data["preferences"].(string)
		var prefs model.Preferences
		if err := json.Unmarshal([]byte(raw), &prefs); err == nil {
			st.ApplyDMPreferences(prefs, ev.EventType() == model.WebsocketEventPreferencesDeleted)
		}

	case model.WebsocketEventSidebarCategoryCreated, model.WebsocketEventSidebarCategoryUpdated,
		model.WebsocketEventSidebarCategoryDeleted, model.WebsocketEventSidebarCategoryOrderUpdated:
		if b := ev.GetBroadcast(); b != nil && b.TeamId != "" {
			return m.loadCategories(ev.Server, b.TeamId)
		}

	case model.WebsocketEventDirectAdded, model.WebsocketEventGroupAdded, model.WebsocketEventChannelCreated:
		return m.loadServer(m.byName[ev.Server])

	case model.WebsocketEventUserAdded:
		if data["user_id"] == st.Me.Id {
			return m.loadServer(m.byName[ev.Server])
		}
	}
	return nil
}

func (m *Model) notify(ref chanRef, post *model.Post, sender string) tea.Cmd {
	st := m.servers[ref.server]
	if u, ok := st.Users[post.UserId]; ok {
		sender = u.Username
	}
	sender = strings.TrimPrefix(sender, "@")

	title := sender
	if ch := st.Channels[ref.channel]; ch.Type != model.ChannelTypeDirect {
		title += " in " + channelLabel(ch)
	}
	if len(m.sessions) > 1 {
		title += " · " + ref.server
	}
	body := post.Message
	if body == "" && len(post.FileIds) > 0 {
		body = "sent a file"
	}
	msg := notify.Message{Key: ref.server + "/" + ref.channel, Title: title, Body: body}

	n := m.notifier
	cmds := []tea.Cmd{func() tea.Msg {
		_ = n.Desktop(msg)
		return nil
	}}
	if seq := n.Terminal(msg); seq != "" {
		cmds = append(cmds, tea.Raw(seq))
	}
	return tea.Batch(cmds...)
}

func (m *Model) dismiss(ref chanRef) tea.Cmd {
	n := m.notifier
	key := ref.server + "/" + ref.channel
	return func() tea.Msg {
		n.Dismiss(key)
		return nil
	}
}

func channelLabel(ch *model.Channel) string {
	if ch.Type == model.ChannelTypeGroup {
		return ch.DisplayName
	}
	return "~" + ch.DisplayName
}

type categoriesMsg struct {
	server string
	teamID string
	cats   *model.OrderedSidebarCategories
}

func (m *Model) loadCategories(server, teamID string) tea.Cmd {
	sess := m.byName[server]
	return func() tea.Msg {
		cats, err := sess.Categories(m.ctx, teamID)
		if err != nil {
			return errMsg{fmt.Errorf("%s: sidebar categories: %w", server, err)}
		}
		return categoriesMsg{server: server, teamID: teamID, cats: cats}
	}
}

func (m *Model) viewOnServer(ref chanRef) tea.Cmd {
	sess := m.byName[ref.server]
	return func() tea.Msg {
		if err := sess.View(m.ctx, ref.channel); err != nil {
			return errMsg{fmt.Errorf("%s: mark read: %w", ref.server, err)}
		}
		return nil
	}
}

func (m *Model) onConn(msg connMsg) tea.Cmd {
	if msg.err != nil {
		m.online[msg.server] = false
		slog.Warn("websocket disconnected", "server", msg.server, "err", msg.err)
		return nil
	}
	m.online[msg.server] = true
	if !m.everUp[msg.server] {
		m.everUp[msg.server] = true
		return nil
	}
	slog.Info("websocket reconnected, resyncing", "server", msg.server)
	return m.loadServer(m.byName[msg.server])
}

func (m *Model) setError(err error) {
	slog.Error("ui", "err", err)
	m.status = err.Error()
}

func decodePost(data map[string]any) *model.Post {
	raw, _ := data["post"].(string)
	var post model.Post
	if err := json.Unmarshal([]byte(raw), &post); err != nil || post.Id == "" {
		return nil
	}
	return &post
}

func mentions(data map[string]any, userID string) bool {
	raw, _ := data["mentions"].(string)
	if raw == "" {
		return false
	}
	var ids []string
	if err := json.Unmarshal([]byte(raw), &ids); err != nil {
		return false
	}
	for _, id := range ids {
		if id == userID {
			return true
		}
	}
	return false
}

func (m *Model) teamName(server, teamID string) string {
	if st := m.servers[server]; st != nil {
		for _, t := range st.Teams {
			if t.Id == teamID {
				return t.DisplayName
			}
		}
	}
	return ""
}

func (m *Model) channelTitle() string {
	st := m.servers[m.current.server]
	if st == nil {
		return ""
	}
	ch, ok := st.Channels[m.current.channel]
	if !ok {
		return ""
	}
	if ch.IsGroupOrDirect() {
		return "@" + st.ChannelName(ch.Id)
	}
	return "#" + ch.DisplayName
}

// sidebarW is the sidebar's width including its border. Narrow terminals
// hide it unless it has focus, and then it takes the whole width.
func (m *Model) sidebarW() int {
	switch {
	case m.width >= narrowWidth:
		return sidebarWidth
	case m.focus == focusSidebar:
		return m.width
	}
	return 0
}

func (m *Model) mainWidth() int {
	return max(m.width-m.sidebarW(), 1)
}

func (m *Model) layout() {
	atBottom := m.vp.AtBottom()
	w, h := m.mainWidth(), max(m.height-m.chromeHeight(), 1)
	m.laidOut = [2]int{w, h}
	m.input.SetWidth(w)
	m.switcher.input.SetWidth(w - lipgloss.Width(m.switcher.input.Prompt) - 1)
	if m.search != nil {
		m.search.input.SetWidth(w - lipgloss.Width(m.search.input.Prompt) - 1)
	}
	m.vp.SetWidth(w)
	m.vp.SetHeight(h)
	m.refreshMessages()
	if atBottom {
		m.vp.GotoBottom()
	}
}

// chromeHeight is everything in the main column except the message viewport:
// header (2), input (3 + border), status bar (1) and any open suggestions.
func (m *Model) chromeHeight() int {
	return 2 + m.input.Height() + 1 + 1 + m.completionHeight()
}

func (m *Model) refreshMessages() {
	st := m.servers[m.current.server]
	m.spans = nil
	switch {
	case st == nil || m.current.channel == "":
		m.vp.SetContent(faint.Render(" Connecting…"))
		return
	case m.thread != nil && m.thread.posts == nil:
		m.vp.SetContent(faint.Render(" Loading thread…"))
		return
	case m.thread == nil && st.Posts[m.current.channel] == nil:
		m.vp.SetContent(faint.Render(" Loading…"))
		return
	}
	opts := renderOpts{selected: m.selectedID(), crt: st.CRT, thread: m.thread != nil}
	if m.thread == nil {
		opts.hasMore = st.Posts[m.current.channel].HasMore
	}
	content, spans := renderPosts(st, m.visiblePosts(), m.mainWidth()-1, opts)
	m.spans = spans
	m.msgLines = strings.Split(content, "\n")
	atBottom := m.vp.AtBottom()
	m.vp.SetContent(content)
	if atBottom {
		m.vp.GotoBottom()
	}
}

func (m *Model) View() tea.View {
	v := tea.NewView("")
	v.AltScreen = true
	v.ReportFocus = true
	v.WindowTitle = "termost"
	if m.mouse {
		v.MouseMode = tea.MouseModeCellMotion
	}
	m.zones = m.zones[:0]
	if m.width == 0 {
		return v
	}

	if m.width < minWidth || m.height < minHeight {
		msg := fmt.Sprintf("termost needs at least %d×%d\n(now %d×%d)", minWidth, minHeight, m.width, m.height)
		v.SetContent(lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, msg))
		return v
	}

	bodyHeight := m.height - 1
	if m.sidebarW() >= m.width {
		v.SetContent(lipgloss.JoinVertical(lipgloss.Left, m.renderSidebar(m.width, bodyHeight), m.statusBar()))
		m.sidebarZone(m.width, bodyHeight)
		return v
	}

	title := bold.Render(m.channelTitle())
	if m.search != nil && m.thread == nil && !m.switcher.open {
		title = m.searchTitle()
	} else if m.threads != nil && m.thread == nil && !m.switcher.open {
		title = bold.Render("Followed threads") + faint.Render("  · "+m.teamName(m.threads.server, m.threads.team)+" · Esc to go back")
	} else if m.thread != nil && !m.switcher.open {
		title = bold.Render("Thread in "+m.channelTitle()) + faint.Render("  · Esc to go back")
	} else if m.switcher.open {
		title = bold.Render("Jump to a channel or DM") + faint.Render("  · "+m.active)
	} else if st := m.servers[m.current.server]; st != nil {
		where := m.current.server
		if ch := st.Channels[m.current.channel]; ch != nil {
			for _, t := range st.Teams {
				if t.Id == ch.TeamId {
					where += " / " + t.DisplayName
				}
			}
			if ch.Purpose != "" {
				title += "  " + faint.Render(ch.Purpose)
			}
		}
		title += faint.Render("  · " + where)
	}
	header := headerBox.Width(m.mainWidth()).Render(truncate(title, m.mainWidth()))
	body := m.vp.View()
	switch {
	case m.relogin != nil:
		body = m.renderRelogin(m.mainWidth(), m.vp.Height())
	case m.search != nil && m.thread == nil && !m.switcher.open && !m.picker.open:
		body = m.renderSearch(m.mainWidth(), m.vp.Height())
	case m.threads != nil && m.thread == nil && !m.switcher.open:
		body = m.renderThreadsList(m.mainWidth(), m.vp.Height())
	case m.picker.open:
		body = m.renderPicker(m.mainWidth(), m.vp.Height())
	case m.switcher.open:
		body = m.renderSwitcher(m.mainWidth(), m.vp.Height())
	}
	parts := []string{header, body}
	if m.completion.active() {
		parts = append(parts, m.renderCompletion(m.mainWidth()))
	}
	inputY := lipgloss.Height(lipgloss.JoinVertical(lipgloss.Left, parts...))
	main := lipgloss.JoinVertical(lipgloss.Left, append(parts, inputBox.Render(m.input.View()))...)

	x := 0
	if w := m.sidebarW(); w > 0 {
		sidebar := m.renderSidebar(w, bodyHeight)
		x = lipgloss.Width(sidebar)
		main = lipgloss.JoinHorizontal(lipgloss.Top, sidebar, main)
		m.sidebarZone(x, bodyHeight)
	}
	v.SetContent(lipgloss.JoinVertical(lipgloss.Left, main, m.statusBar()))
	m.mainZones(x, lipgloss.Height(header), lipgloss.Height(body), inputY)
	v.Cursor = m.terminalCursor(x, lipgloss.Height(header), inputY+inputBox.GetBorderTopSize())
	return v
}

func (m *Model) terminalCursor(x, bodyY, inputY int) *tea.Cursor {
	c, y := m.input.Cursor(), inputY
	switch {
	case m.relogin != nil:
		c, x, y = m.relogin.input.Cursor(), x+reloginIndent, bodyY+reloginInputRow
	case m.picker.open:
		c, y = m.picker.input.Cursor(), bodyY
	case m.switcher.open:
		c, y = m.switcher.input.Cursor(), bodyY
	case m.search != nil && m.thread == nil:
		c, y = m.search.input.Cursor(), bodyY
	}
	if c == nil || !m.termFocused {
		return nil
	}
	c.X += x
	c.Y += y
	return c
}

func (m *Model) statusBar() string {
	if m.deleting != nil {
		return truncate(" "+errorStyle.Bold(true).Render("Delete this message? y/n"), m.width)
	}
	if m.status != "" {
		return truncate(" "+errorStyle.Render(m.status), m.width)
	}
	if m.notice != "" {
		return truncate(" "+m.notice, m.width)
	}
	hint := "Ctrl+K jump · Alt+1-9 server · Alt+J/K channel · Alt+Shift+J/K unread · ↑/Tab select · Ctrl+C quit"
	if st := m.servers[m.active]; st != nil && st.CRT {
		hint = "Ctrl+K jump · Alt+T threads · Alt+1-9 server · Alt+J/K channel · Alt+Shift+J/K unread · ↑/Tab select · Ctrl+C quit"
	}
	switch {
	case m.editing != nil:
		hint = "Editing message · Enter save · Esc cancel"
	case m.search != nil && m.thread == nil:
		hint = m.searchHint()
	case m.threads != nil && m.thread == nil:
		hint = "j/k move · Enter open thread · Esc/Alt+T back"
	case m.focus == focusMessages:
		hint = "j/k move · Enter/r thread · + react · y copy · Y link · e edit · d delete · o open files · Esc back"
	case m.thread != nil:
		hint = "Enter reply · Esc close thread · ↑ select messages · Ctrl+K jump"
	case m.width < narrowWidth:
		hint = "Tab channels · Ctrl+K jump · Alt+J/K · Ctrl+C quit"
	}
	return truncate(" "+faint.Render(hint), m.width)
}
