// Package markdown renders Mattermost-flavoured markdown into compact,
// pre-wrapped terminal lines for a chat stream.
package markdown

import (
	"regexp"
	"strconv"
	"strings"
	"sync"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	east "github.com/yuin/goldmark/extension/ast"
	"github.com/yuin/goldmark/text"

	"github.com/kurund/termost/internal/emoji"
	"github.com/kurund/termost/internal/termsafe"
)

const cacheLimit = 4000

var (
	plain       = lipgloss.NewStyle()
	boldStyle   = lipgloss.NewStyle().Bold(true)
	faint       = lipgloss.NewStyle().Faint(true)
	codeStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("5"))
	linkStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("4")).Underline(true)
	headStyle   = lipgloss.NewStyle().Bold(true).Underline(true)
	mentionMe   = lipgloss.NewStyle().Foreground(lipgloss.Color("3")).Bold(true)
	mentionUser = lipgloss.NewStyle().Bold(true)

	// Mattermost usernames may contain . _ - but a trailing one is punctuation.
	mentionRe = regexp.MustCompile(`[@~][a-zA-Z0-9][a-zA-Z0-9._-]*[a-zA-Z0-9]|[@~][a-zA-Z0-9]`)

	parser = goldmark.New(goldmark.WithExtensions(
		extension.Strikethrough, extension.Table, extension.Linkify, extension.TaskList,
	)).Parser()

	cacheMu sync.Mutex
	cache   = map[string]string{}
)

// Render returns src as styled lines no wider than width. me is the current
// username, whose mentions are highlighted along with @here/@channel/@all.
func Render(src string, width int, me string) string {
	key := strconv.Itoa(width) + "\x00" + me + "\x00" + src
	cacheMu.Lock()
	out, ok := cache[key]
	cacheMu.Unlock()
	if ok {
		return out
	}

	source := []byte(termsafe.Strip(src))
	r := &renderer{src: source, me: me}
	out = strings.Join(r.blocks(parser.Parse(text.NewReader(source)), max(width, 4)), "\n")

	cacheMu.Lock()
	if len(cache) >= cacheLimit {
		clear(cache)
	}
	cache[key] = out
	cacheMu.Unlock()
	return out
}

type span struct {
	text  string
	style lipgloss.Style
}

type renderer struct {
	src []byte
	me  string
}

func (r *renderer) blocks(parent ast.Node, width int) []string {
	var lines []string
	for n := parent.FirstChild(); n != nil; n = n.NextSibling() {
		if len(lines) > 0 && (n.HasBlankPreviousLines() || n.Kind() == east.KindTable) {
			lines = append(lines, "")
		}
		lines = append(lines, r.block(n, width)...)
	}
	return lines
}

func (r *renderer) block(n ast.Node, width int) []string {
	switch n := n.(type) {
	case *ast.Paragraph, *ast.TextBlock:
		return wrap(r.inlines(n, plain), width)

	case *ast.Heading:
		return wrap(r.inlines(n, headStyle), width)

	case *ast.FencedCodeBlock, *ast.CodeBlock:
		return r.code(n, width)

	case *ast.Blockquote:
		return prefixLines(r.blocks(n, width-2), faint.Render("│ "), faint.Render("│ "))

	case *ast.List:
		return r.list(n, width)

	case *ast.ThematicBreak:
		return []string{faint.Render(strings.Repeat("─", min(width, 20)))}

	case *ast.HTMLBlock:
		var lines []string
		for i := 0; i < n.Lines().Len(); i++ {
			seg := n.Lines().At(i)
			lines = append(lines, wrap([]span{{strings.TrimRight(string(seg.Value(r.src)), "\n"), plain}}, width)...)
		}
		return lines

	case *east.Table:
		return r.table(n, width)
	}
	return r.blocks(n, width)
}

func (r *renderer) code(n ast.Node, width int) []string {
	bar := faint.Render("▏")
	var lines []string
	for i := 0; i < n.Lines().Len(); i++ {
		seg := n.Lines().At(i)
		line := strings.ReplaceAll(strings.TrimRight(string(seg.Value(r.src)), "\r\n"), "\t", "    ")
		for _, part := range hardSplit(line, width-1) {
			lines = append(lines, bar+codeStyle.Render(part))
		}
	}
	return lines
}

func (r *renderer) list(n *ast.List, width int) []string {
	var lines []string
	num := n.Start
	for item := n.FirstChild(); item != nil; item = item.NextSibling() {
		marker := "• "
		if n.IsOrdered() {
			marker = strconv.Itoa(num) + ". "
			num++
		}
		indent := ansi.StringWidth(marker)
		body := r.blocks(item, width-indent)
		if len(body) == 0 {
			body = []string{""}
		}
		lines = append(lines, prefixLines(body, faint.Render(marker), strings.Repeat(" ", indent))...)
	}
	return lines
}

func (r *renderer) table(n *east.Table, width int) []string {
	var rows [][]string
	for row := n.FirstChild(); row != nil; row = row.NextSibling() {
		var cells []string
		for cell := row.FirstChild(); cell != nil; cell = cell.NextSibling() {
			cells = append(cells, strings.TrimSpace(spansText(r.inlines(cell, plain))))
		}
		rows = append(rows, cells)
	}
	if len(rows) == 0 {
		return nil
	}

	cols := len(rows[0])
	widths := make([]int, cols)
	for _, row := range rows {
		for i := 0; i < cols && i < len(row); i++ {
			widths[i] = max(widths[i], ansi.StringWidth(row[i]))
		}
	}
	sep := " │ "
	for total(widths)+(cols-1)*len(sep) > width {
		widest := 0
		for i := range widths {
			if widths[i] > widths[widest] {
				widest = i
			}
		}
		if widths[widest] <= 3 {
			break
		}
		widths[widest]--
	}

	var lines []string
	for ri, row := range rows {
		var cells []string
		for i := 0; i < cols; i++ {
			cell := ""
			if i < len(row) {
				cell = ansi.Truncate(row[i], widths[i], "…")
			}
			cell += strings.Repeat(" ", widths[i]-ansi.StringWidth(cell))
			if ri == 0 {
				cell = boldStyle.Render(cell)
			}
			cells = append(cells, cell)
		}
		lines = append(lines, strings.Join(cells, faint.Render(sep)))
		if ri == 0 {
			var rule []string
			for _, w := range widths {
				rule = append(rule, strings.Repeat("─", w))
			}
			lines = append(lines, faint.Render(strings.Join(rule, "─┼─")))
		}
	}
	return lines
}

// inlines joins runs of adjacent text nodes before matching mentions, since
// the parser splits text at characters like ~ that may open other syntax.
func (r *renderer) inlines(parent ast.Node, style lipgloss.Style) []span {
	var spans []span
	var run strings.Builder
	for n := parent.FirstChild(); n != nil; n = n.NextSibling() {
		t, ok := n.(*ast.Text)
		if !ok {
			spans = append(spans, r.mentions(run.String(), style)...)
			run.Reset()
			spans = append(spans, r.inline(n, style)...)
			continue
		}
		run.Write(t.Segment.Value(r.src))
		if t.SoftLineBreak() || t.HardLineBreak() {
			spans = append(spans, r.mentions(run.String(), style)...)
			run.Reset()
			spans = append(spans, span{"\n", style})
		}
	}
	return append(spans, r.mentions(run.String(), style)...)
}

func (r *renderer) inline(n ast.Node, style lipgloss.Style) []span {
	switch n := n.(type) {
	case *ast.String:
		return r.mentions(string(n.Value), style)

	case *ast.CodeSpan:
		var b strings.Builder
		for c := n.FirstChild(); c != nil; c = c.NextSibling() {
			if t, ok := c.(*ast.Text); ok {
				b.Write(t.Segment.Value(r.src))
			}
		}
		return []span{{b.String(), codeStyle}}

	case *ast.Emphasis:
		if n.Level >= 2 {
			return r.inlines(n, style.Bold(true))
		}
		return r.inlines(n, style.Italic(true))

	case *east.Strikethrough:
		return r.inlines(n, style.Strikethrough(true))

	case *ast.Link:
		dest := string(n.Destination)
		spans := r.inlines(n, linkStyle.Hyperlink(dest))
		if dest != spansText(spans) {
			spans = append(spans, span{" (" + dest + ")", faint.Hyperlink(dest)})
		}
		return spans

	case *ast.AutoLink:
		url := string(n.URL(r.src))
		return []span{{string(n.Label(r.src)), linkStyle.Hyperlink(url)}}

	case *ast.Image:
		return []span{{"🖼 " + spansText(r.inlines(n, plain)) + " (" + string(n.Destination) + ")", faint}}

	case *ast.RawHTML:
		var b strings.Builder
		for i := 0; i < n.Segments.Len(); i++ {
			seg := n.Segments.At(i)
			b.Write(seg.Value(r.src))
		}
		return []span{{b.String(), style}}

	case *east.TaskCheckBox:
		if n.IsChecked {
			return []span{{"☑ ", style}}
		}
		return []span{{"☐ ", style}}
	}
	return r.inlines(n, style)
}

func (r *renderer) mentions(s string, style lipgloss.Style) []span {
	s = emoji.Replace(s)
	var spans []span
	last := 0
	for _, loc := range mentionRe.FindAllStringIndex(s, -1) {
		if loc[0] > 0 && isWordChar(s[loc[0]-1]) {
			continue
		}
		if loc[0] > last {
			spans = append(spans, span{s[last:loc[0]], style})
		}
		spans = append(spans, span{s[loc[0]:loc[1]], r.mentionStyle(s[loc[0]:loc[1]])})
		last = loc[1]
	}
	if last < len(s) {
		spans = append(spans, span{s[last:], style})
	}
	return spans
}

func (r *renderer) mentionStyle(m string) lipgloss.Style {
	if m[0] == '@' {
		switch strings.ToLower(m[1:]) {
		case strings.ToLower(r.me), "here", "channel", "all":
			return mentionMe
		}
	}
	return mentionUser
}

func isWordChar(b byte) bool {
	return b == '_' || b == '.' || b >= '0' && b <= '9' || b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z'
}

// wrap word-wraps spans to width, styling each piece separately so no style
// leaks across lines.
func wrap(spans []span, width int) []string {
	var lines []string
	var line strings.Builder
	lineWidth := 0
	pendingSpace := span{}

	flush := func() {
		lines = append(lines, line.String())
		line.Reset()
		lineWidth = 0
		pendingSpace = span{}
	}
	write := func(s string, style lipgloss.Style) {
		line.WriteString(style.Render(s))
		lineWidth += ansi.StringWidth(s)
	}

	for _, sp := range spans {
		if sp.text == "\n" {
			flush()
			continue
		}
		for _, tok := range tokens(sp.text) {
			if tok == " " {
				if lineWidth > 0 {
					pendingSpace = span{" ", sp.style}
				}
				continue
			}
			w := ansi.StringWidth(tok)
			spaceW := len(pendingSpace.text)
			if lineWidth > 0 && lineWidth+spaceW+w > width {
				flush()
				spaceW = 0
			}
			if spaceW > 0 {
				write(pendingSpace.text, pendingSpace.style)
				pendingSpace = span{}
			}
			if w > width {
				parts := hardSplit(tok, width)
				for _, p := range parts[:len(parts)-1] {
					write(p, sp.style)
					flush()
				}
				tok = parts[len(parts)-1]
			}
			write(tok, sp.style)
		}
	}
	if line.Len() > 0 || len(lines) == 0 {
		lines = append(lines, line.String())
	}
	return lines
}

// tokens splits s into words and single-space separators.
func tokens(s string) []string {
	var out []string
	for _, f := range strings.SplitAfter(s, " ") {
		if f == "" {
			continue
		}
		if word := strings.TrimRight(f, " "); word != "" {
			out = append(out, word)
		}
		if strings.HasSuffix(f, " ") {
			out = append(out, " ")
		}
	}
	return out
}

func hardSplit(s string, width int) []string {
	width = max(width, 1)
	if ansi.StringWidth(s) <= width {
		return []string{s}
	}
	var parts []string
	var cur strings.Builder
	curW := 0
	for _, r := range s {
		rw := ansi.StringWidth(string(r))
		if curW+rw > width {
			parts = append(parts, cur.String())
			cur.Reset()
			curW = 0
		}
		cur.WriteRune(r)
		curW += rw
	}
	return append(parts, cur.String())
}

func prefixLines(lines []string, first, rest string) []string {
	out := make([]string, len(lines))
	for i, l := range lines {
		if i == 0 {
			out[i] = first + l
		} else {
			out[i] = rest + l
		}
	}
	return out
}

func spansText(spans []span) string {
	var b strings.Builder
	for _, s := range spans {
		b.WriteString(s.text)
	}
	return b.String()
}

func total(ns []int) int {
	t := 0
	for _, n := range ns {
		t += n
	}
	return t
}
