package ui

import (
	"fmt"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/mattermost/mattermost/server/public/model"

	"github.com/kurund/termost/internal/emoji"
	"github.com/kurund/termost/internal/markdown"
	"github.com/kurund/termost/internal/store"
)

const groupGap = 5 * time.Minute

type postSpan struct {
	id         string
	start, end int
	reactions  []reactionHit
}

// reactionHit is where a reaction chip was drawn: a content line and the
// columns it covers.
type reactionHit struct {
	line, x0, x1 int
	emoji        string
}

type renderOpts struct {
	selected string
	hasMore  bool
	thread   bool
	crt      bool
}

// renderPosts lays out posts with a one-column gutter that marks the
// selected post, and reports the line range of every post.
func renderPosts(st *store.Server, posts []*model.Post, width int, o renderOpts) (string, []postSpan) {
	var lines []string
	var spans []postSpan
	inner := width - 1

	if o.hasMore {
		lines = append(lines, " "+faint.Render("↑ at the top loads older messages"))
	}
	if len(posts) == 0 {
		return strings.Join(append(lines, " "+faint.Render("No messages yet.")), "\n"), nil
	}

	var prev *model.Post
	var day time.Time
	for i, post := range posts {
		at := time.UnixMilli(post.CreateAt).Local()
		if day.IsZero() || !sameDay(day, at) {
			lines = append(lines, "", " "+daySeparator(at, inner))
			day = at
			prev = nil
		}

		start := len(lines)
		var body []string
		var reactions []reactionHit
		switch {
		case strings.HasPrefix(post.Type, model.PostSystemMessagePrefix):
			body = strings.Split(faint.Italic(true).Width(inner).Render(at.Format("15:04")+"  "+post.Message), "\n")
			prev = nil
		default:
			if startsGroup(prev, post) {
				name := st.Username(post.UserId)
				style := userStyle(name)
				if post.UserId == st.Me.Id {
					style = ownName
				}
				body = append(body, style.Render(name)+"  "+faint.Render(at.Format("15:04")))
			}
			plines, hits := postLines(st, post, inner-2, !o.thread, post.Id == o.selected)
			for _, h := range hits {
				h.line += start + len(body)
				h.x0, h.x1 = h.x0+3, h.x1+3
				reactions = append(reactions, h)
			}
			for _, line := range plines {
				body = append(body, "  "+line)
			}
			if o.crt && !o.thread && post.RootId == "" && post.ReplyCount > 0 {
				body = append(body, "  "+faint.Render(fmt.Sprintf("💬 %d %s", post.ReplyCount, plural(post.ReplyCount, "reply", "replies"))))
			}
			prev = post
		}

		gutter := " "
		if post.Id == o.selected {
			gutter = selBar
		}
		for _, l := range body {
			lines = append(lines, gutter+l)
		}
		spans = append(spans, postSpan{id: post.Id, start: start, end: len(lines), reactions: reactions})

		if o.thread && i == 0 && len(posts) > 1 {
			n := len(posts) - 1
			lines = append(lines, " "+faint.Render(fmt.Sprintf("── %d %s ──", n, plural(int64(n), "reply", "replies"))))
			prev = nil
		}
	}
	return strings.Join(lines, "\n"), spans
}

func plural(n int64, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

func startsGroup(prev, post *model.Post) bool {
	return prev == nil ||
		prev.UserId != post.UserId ||
		post.CreateAt-prev.CreateAt > groupGap.Milliseconds() ||
		(prev.RootId == "") != (post.RootId == "")
}

// postLines renders a post body as lines no wider than width, and where its
// reaction chips are; names lists who reacted instead of counts.
func postLines(st *store.Server, post *model.Post, width int, replyMark, names bool) ([]string, []reactionHit) {
	prefix, rest := "", ""
	if replyMark && post.RootId != "" {
		prefix, rest = faint.Render("↳ "), "  "
	}
	inner := width - lipgloss.Width(rest)

	var lines []string
	if post.Message != "" {
		lines = strings.Split(markdown.Render(post.Message, inner, st.Me.Username), "\n")
	}
	if post.EditAt > 0 {
		edited := faint.Render("(edited)")
		if n := len(lines); n > 0 && lipgloss.Width(lines[n-1])+1+lipgloss.Width(edited) <= inner {
			lines[n-1] += " " + edited
		} else {
			lines = append(lines, edited)
		}
	}
	for _, f := range files(post) {
		lines = append(lines, faint.Render(truncate(f, inner)))
	}
	rlines, hits := reactionLines(st, post, inner, names)
	for i := range hits {
		indent := lipgloss.Width(rest)
		if hits[i].line+len(lines) == 0 {
			indent = lipgloss.Width(prefix)
		}
		hits[i].line += len(lines)
		hits[i].x0 += indent
		hits[i].x1 += indent
	}
	lines = append(lines, rlines...)
	for i := range lines {
		if i == 0 {
			lines[i] = prefix + lines[i]
		} else {
			lines[i] = rest + lines[i]
		}
	}
	return lines, hits
}

func files(post *model.Post) []string {
	if post.Metadata != nil && len(post.Metadata.Files) > 0 {
		names := make([]string, 0, len(post.Metadata.Files))
		for _, f := range post.Metadata.Files {
			icon := "📎"
			if strings.HasPrefix(f.MimeType, "image/") {
				icon = "🖼"
			}
			names = append(names, fmt.Sprintf("%s %s (%s)", icon, f.Name, humanSize(f.Size)))
		}
		return names
	}
	if n := len(post.FileIds); n > 0 {
		return []string{fmt.Sprintf("📎 %d attachment(s)", n)}
	}
	return nil
}

func daySeparator(t time.Time, width int) string {
	label := " " + t.Format("Monday, 2 Jan 2006") + " "
	side := max((width-lipgloss.Width(label))/2, 2)
	return faint.Render(strings.Repeat("─", side) + label + strings.Repeat("─", side))
}

func sameDay(a, b time.Time) bool {
	ay, am, ad := a.Date()
	by, bm, bd := b.Date()
	return ay == by && am == bm && ad == bd
}

func humanSize(n int64) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%d KB", n>>10)
	}
	return fmt.Sprintf("%d B", n)
}

func reactionLines(st *store.Server, post *model.Post, width int, names bool) ([]string, []reactionHit) {
	var lines []string
	var hits []reactionHit
	line, lineW := "", 0
	for _, r := range st.Reactions(post) {
		glyph, ok := emoji.Lookup(r.Emoji)
		if !ok {
			glyph = ":" + r.Emoji + ":"
		}
		chip := fmt.Sprintf("%s %d", glyph, r.Count)
		if names {
			chip = truncate(glyph+" "+reactorNames(st, r.Users), width)
		}
		w := lipgloss.Width(chip)
		if r.Mine {
			chip = unreadStyle.Render(chip)
		} else {
			chip = faint.Render(chip)
		}
		if lineW > 0 && lineW+2+w > width {
			lines = append(lines, line)
			line, lineW = "", 0
		}
		if lineW > 0 {
			line += "  "
			lineW += 2
		}
		hits = append(hits, reactionHit{line: len(lines), x0: lineW, x1: lineW + w, emoji: r.Emoji})
		line += chip
		lineW += w
	}
	if lineW > 0 {
		lines = append(lines, line)
	}
	return lines, hits
}

func reactorNames(st *store.Server, users []string) string {
	names := make([]string, len(users))
	for i, id := range users {
		if id == st.Me.Id {
			names[i] = "you"
		} else {
			names[i] = st.Username(id)
		}
	}
	return strings.Join(names, ", ")
}
