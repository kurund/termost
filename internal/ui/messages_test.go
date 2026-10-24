package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/mattermost/mattermost/server/public/model"

	"github.com/kurund/termost/internal/store"
)

func TestRenderPostsSpansAndSelection(t *testing.T) {
	me := &model.User{Id: "me", Username: "me"}
	st := store.NewServer("work", me)
	st.AddUsers(map[string]*model.User{"al": {Id: "al", Username: "alice"}})
	posts := []*model.Post{
		{Id: "a", UserId: "al", Message: "root", ReplyCount: 2, CreateAt: 1000},
		{Id: "b", UserId: "al", Message: "second", CreateAt: 2000},
	}

	out, spans := renderPosts(st, posts, 40, renderOpts{selected: "a", crt: true})
	lines := strings.Split(out, "\n")
	if len(spans) != 2 || spans[0].id != "a" || spans[1].id != "b" {
		t.Fatalf("spans = %+v", spans)
	}
	for i := spans[0].start; i < spans[0].end; i++ {
		if !strings.HasPrefix(lines[i], selBar) {
			t.Errorf("selected line %d lacks gutter bar: %q", i, ansi.Strip(lines[i]))
		}
	}
	for i := spans[1].start; i < spans[1].end; i++ {
		if strings.HasPrefix(lines[i], selBar) {
			t.Errorf("unselected line %d has gutter bar", i)
		}
	}
	if got := ansi.Strip(lines[spans[0].end-1]); !strings.Contains(got, "💬 2 replies") {
		t.Errorf("reply count line = %q", got)
	}

	out, _ = renderPosts(st, posts, 40, renderOpts{thread: true})
	if !strings.Contains(ansi.Strip(out), "── 1 reply ──") || strings.Contains(out, "💬") {
		t.Errorf("thread render:\n%s", ansi.Strip(out))
	}
}

func TestReactionNamesAndHits(t *testing.T) {
	st := store.NewServer("t", &model.User{Id: "me", Username: "me"})
	st.AddUsers(map[string]*model.User{"a": {Id: "a", Username: "alice"}, "b": {Id: "b", Username: "bob"}})
	post := &model.Post{Id: "p", UserId: "a", Message: "hi", Metadata: &model.PostMetadata{Reactions: []*model.Reaction{
		{UserId: "a", EmojiName: "+1"}, {UserId: "me", EmojiName: "+1"}, {UserId: "b", EmojiName: "tada"},
	}}}

	collapsed, _ := reactionLines(st, post, 60, false)
	if got := ansi.Strip(collapsed[0]); got != "👍 2  🎉 1" {
		t.Errorf("collapsed = %q", got)
	}
	expanded, hits := reactionLines(st, post, 60, true)
	if got := ansi.Strip(expanded[0]); got != "👍 alice, you  🎉 bob" {
		t.Errorf("expanded = %q", got)
	}
	if len(hits) != 2 || hits[0] != (reactionHit{0, 0, 13, "+1"}) || hits[1] != (reactionHit{0, 15, 21, "tada"}) {
		t.Errorf("hits = %+v", hits)
	}

	content, spans := renderPosts(st, []*model.Post{post}, 60, renderOpts{selected: "p"})
	lines := strings.Split(content, "\n")
	h := spans[0].reactions[1]
	if got := ansi.Strip(lines[h.line]); ansi.Cut(got, h.x0, h.x1) != "🎉 bob" {
		t.Errorf("hit points at %q in %q", ansi.Cut(got, h.x0, h.x1), got)
	}
}
