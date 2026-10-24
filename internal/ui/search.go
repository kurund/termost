package ui

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/mattermost/mattermost/server/public/model"

	"github.com/kurund/termost/internal/emoji"
)

type searchView struct {
	server   string
	input    textinput.Model
	searched string
	loading  bool
	err      error
	posts    []*model.Post
	matches  map[string][]string
	sel      int
	back     chanRef
	lineItem []int
}

type searchResultsMsg struct {
	server, query string
	posts         []*model.Post
	matches       map[string][]string
	users         map[string]*model.User
	err           error
}

const searchHeader = 2

var markdownMarks = strings.NewReplacer("**", "", "__", "", "~~", "", "`", "")

func (m *Model) openSearch(query string) tea.Cmd {
	back := m.current
	switch {
	case m.search != nil:
		back = m.search.back
	case m.threads != nil:
		back = m.threads.back
	}
	if m.editing != nil {
		m.stopEdit(m.editing.draft)
	}
	m.thread, m.threads = nil, nil
	m.setCompletion(0, "", nil)

	in := textinput.New()
	in.SetVirtualCursor(false)
	in.Prompt = "Search: "
	in.Placeholder = `words, "a phrase", from:user, in:channel, -exclude, after:2026-01-31`
	in.SetWidth(m.mainWidth() - lipgloss.Width(in.Prompt) - 1)
	in.SetValue(query)
	m.search = &searchView{server: m.active, input: in, back: back}
	m.focus = focusInput
	m.input.Blur()
	cmd := m.search.input.Focus()
	if strings.TrimSpace(query) != "" {
		cmd = tea.Batch(cmd, m.runSearch())
	}
	return cmd
}

func (m *Model) closeSearch() tea.Cmd {
	back := m.search.back
	m.search = nil
	if back.channel != "" && back != m.current {
		return tea.Batch(m.open(back), m.input.Focus())
	}
	m.input.Placeholder = "Message " + m.channelTitle()
	m.refreshMessages()
	return m.input.Focus()
}

func (m *Model) runSearch() tea.Cmd {
	s := m.search
	query := strings.TrimSpace(s.input.Value())
	st, sess := m.servers[s.server], m.byName[s.server]
	if query == "" || st == nil {
		return nil
	}
	s.searched, s.loading, s.err = query, true, nil
	s.posts, s.matches, s.sel = nil, nil, 0
	teams := make([]string, 0, len(st.Teams))
	for _, t := range st.Teams {
		teams = append(teams, t.Id)
	}
	server := s.server
	return func() tea.Msg {
		var (
			mu      sync.Mutex
			wg      sync.WaitGroup
			errs    []error
			posts   = map[string]*model.Post{}
			matches = map[string][]string{}
		)
		for _, team := range teams {
			wg.Go(func() {
				res, err := sess.Search(m.ctx, team, query)
				mu.Lock()
				defer mu.Unlock()
				if err != nil {
					errs = append(errs, err)
					return
				}
				for id, p := range res.Posts {
					posts[id] = p
				}
				for id, terms := range res.Matches {
					matches[id] = terms
				}
			})
		}
		wg.Wait()
		if len(errs) > 0 && len(errs) == len(teams) {
			return searchResultsMsg{server: server, query: query, err: errors.Join(errs...)}
		}
		list := make([]*model.Post, 0, len(posts))
		authors := map[string]bool{}
		for _, p := range posts {
			list = append(list, p)
			authors[p.UserId] = true
		}
		sort.Slice(list, func(i, j int) bool { return list[i].CreateAt > list[j].CreateAt })
		ids := make([]string, 0, len(authors))
		for id := range authors {
			ids = append(ids, id)
		}
		users, _ := sess.Users(m.ctx, ids)
		return searchResultsMsg{server: server, query: query, posts: list, matches: matches, users: users}
	}
}

func (m *Model) onSearchResults(msg searchResultsMsg) {
	s := m.search
	if s == nil || s.server != msg.server || s.searched != msg.query {
		return
	}
	s.loading, s.err = false, msg.err
	s.posts, s.matches, s.sel = msg.posts, msg.matches, 0
	if st := m.servers[msg.server]; st != nil && msg.users != nil {
		st.AddUsers(msg.users)
	}
}

// searchKey handles keys while results are shown; it reports false for keys
// that should reach the global bindings.
func (m *Model) searchKey(msg tea.KeyPressMsg) (tea.Cmd, bool) {
	s := m.search
	key := msg.String()
	switch {
	case key == "esc":
		return m.closeSearch(), true
	case key == "up" || key == "ctrl+p":
		s.sel = max(s.sel-1, 0)
	case key == "down" || key == "ctrl+n" || key == "tab":
		s.sel = min(s.sel+1, max(len(s.posts)-1, 0))
	case key == "pgup":
		s.sel = max(s.sel-5, 0)
	case key == "pgdown":
		s.sel = min(s.sel+5, max(len(s.posts)-1, 0))
	case key == "enter":
		query := strings.TrimSpace(s.input.Value())
		if query != s.searched || (len(s.posts) == 0 && !s.loading) {
			return m.runSearch(), true
		}
		return m.openSearchResult(s.sel), true
	case key == "ctrl+k" || key == "ctrl+l" || strings.HasPrefix(key, "alt+"):
		return nil, false
	default:
		var cmd tea.Cmd
		s.input, cmd = s.input.Update(msg)
		return cmd, true
	}
	return nil, true
}

func (m *Model) openSearchResult(i int) tea.Cmd {
	s := m.search
	if i < 0 || i >= len(s.posts) || s.loading {
		return nil
	}
	s.sel = i
	post := s.posts[i]
	m.current = chanRef{s.server, post.ChannelId}
	s.input.Blur()
	return m.openThread(post)
}

func (m *Model) searchTitle() string {
	return bold.Render("Search") + faint.Render("  · "+m.search.server+" · Enter to search or open · Esc to go back")
}

func (m *Model) renderSearch(width, height int) string {
	s := m.search
	st := m.servers[s.server]
	lines := []string{s.input.View(), faint.Render(strings.Repeat("─", width))}
	listHeight := max(height-searchHeader, 1)

	var list []string
	var lineItem []int
	note := func(text string) {
		list = append(list, text)
		lineItem = append(lineItem, -1)
	}
	switch {
	case s.loading:
		note(faint.Render(" Searching…"))
	case s.err != nil:
		note(errorStyle.Render(" " + s.err.Error()))
	case s.searched == "":
		note(faint.Render(" Type and press Enter to search " + s.server + "."))
	case len(s.posts) == 0:
		note(faint.Render(" No results for “" + s.searched + "”"))
	}

	selStart, selEnd := 0, 0
	terms := queryTerms(s.searched)
	for i, p := range s.posts {
		where := bold.Render("#" + st.ChannelName(p.ChannelId))
		if ch := st.Channels[p.ChannelId]; ch != nil && ch.IsGroupOrDirect() {
			where = bold.Render("@" + st.ChannelName(p.ChannelId))
		} else if ch != nil && len(st.Teams) > 1 {
			where += faint.Render(" · " + m.teamName(s.server, ch.TeamId))
		}
		meta := "  " + searchTime(p.CreateAt, time.Now())
		if p.RootId != "" {
			meta += " · reply"
		}
		first := truncate("  "+where+"  "+userStyle(st.Username(p.UserId)).Render(st.Username(p.UserId))+faint.Render(meta), width)
		if i == s.sel {
			first = selected.Render(padRight(ansi.Strip(first), width))
			selStart = len(list)
		}
		hl := terms
		if t := s.matches[p.Id]; len(t) > 0 {
			hl = t
		}
		list = append(list, first, "    "+excerpt(markdownMarks.Replace(emoji.Replace(p.Message)), hl, width-4), "")
		lineItem = append(lineItem, i, i, -1)
		if i == s.sel {
			selEnd = len(list)
		}
	}

	start := max(selEnd-listHeight, 0)
	start = min(start, selStart)
	list, lineItem = list[min(start, len(list)):], lineItem[min(start, len(lineItem)):]
	s.lineItem = lineItem
	lines = append(lines, list...)
	for len(lines) < height {
		lines = append(lines, "")
	}
	return strings.Join(lines[:height], "\n")
}

func searchTime(ms int64, now time.Time) string {
	t := time.UnixMilli(ms)
	switch {
	case t.YearDay() == now.YearDay() && t.Year() == now.Year():
		return t.Format("15:04")
	case t.Year() == now.Year():
		return t.Format("Jan 2 15:04")
	}
	return t.Format("Jan 2 2006")
}

// queryTerms picks the words to highlight from a search query, skipping
// modifiers such as from: and -excluded words.
func queryTerms(query string) []string {
	var terms []string
	for _, f := range strings.Fields(strings.ReplaceAll(query, `"`, " ")) {
		if strings.HasPrefix(f, "-") || strings.Contains(f, ":") {
			continue
		}
		if f = strings.Trim(f, "*"); f != "" {
			terms = append(terms, f)
		}
	}
	return terms
}

// excerpt flattens a message to one line of width cells, moved along to show
// the first match, with matches highlighted.
func excerpt(message string, terms []string, width int) string {
	text := []rune(strings.Join(strings.Fields(message), " "))
	spans := matchSpans(text, terms)
	if len(spans) > 0 && ansi.StringWidth(string(text[:spans[0][0]])) > width/2 {
		cut := max(spans[0][0]-width/3, 0)
		text = append([]rune("…"), text[cut:]...)
	}
	text = []rune(truncate(string(text), width))
	spans = matchSpans(text, terms)

	var b strings.Builder
	pos := 0
	for _, sp := range spans {
		b.WriteString(faint.Render(string(text[pos:sp[0]])))
		b.WriteString(matchStyle.Render(string(text[sp[0]:sp[1]])))
		pos = sp[1]
	}
	b.WriteString(faint.Render(string(text[pos:])))
	return b.String()
}

// matchSpans finds case-insensitive occurrences of terms as rune ranges,
// in order and without overlaps.
func matchSpans(text []rune, terms []string) [][2]int {
	lower := make([]rune, len(text))
	for i, r := range text {
		lower[i] = unicode.ToLower(r)
	}
	var spans [][2]int
	for i := 0; i < len(lower); {
		end := 0
		for _, t := range terms {
			tr := []rune(strings.ToLower(t))
			if len(tr) > 0 && i+len(tr) <= len(lower) && string(lower[i:i+len(tr)]) == string(tr) {
				end = max(end, i+len(tr))
			}
		}
		if end > 0 {
			spans = append(spans, [2]int{i, end})
			i = end
			continue
		}
		i++
	}
	return spans
}

func (m *Model) searchHint() string {
	s := m.search
	if s.searched != "" && s.searched == strings.TrimSpace(s.input.Value()) && len(s.posts) > 0 {
		return fmt.Sprintf("%d results · ↑/↓ move · Enter open in thread · Esc back", len(s.posts))
	}
	return "Enter search · from:user in:channel \"phrase\" -exclude before:/after:/on:YYYY-MM-DD · Esc back"
}
