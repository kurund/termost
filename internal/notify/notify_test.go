package notify

import (
	"strings"
	"testing"
	"time"

	"github.com/mattermost/mattermost/server/public/model"

	"github.com/kurund/termost/internal/config"
)

func TestShouldNotify(t *testing.T) {
	me := &model.User{Id: "me", NotifyProps: model.StringMap{"desktop": "mention"}}
	open := &model.Channel{Id: "c", Type: model.ChannelTypeOpen}
	dm := &model.Channel{Id: "d", Type: model.ChannelTypeDirect}
	member := func(props model.StringMap) *model.ChannelMember {
		return &model.ChannelMember{NotifyProps: props}
	}
	fromAlice := &model.Post{UserId: "alice"}

	cases := []struct {
		name string
		in   Input
		want bool
	}{
		{"plain channel message", Input{Me: me, Channel: open, Post: fromAlice}, false},
		{"mention", Input{Me: me, Channel: open, Post: fromAlice, MentionsMe: true}, true},
		{"dm without mention", Input{Me: me, Channel: dm, Post: fromAlice}, true},
		{"own post", Input{Me: me, Channel: dm, Post: &model.Post{UserId: "me"}}, false},
		{"viewing", Input{Me: me, Channel: dm, Post: fromAlice, Viewing: true}, false},
		{"system message", Input{Me: me, Channel: dm, Post: &model.Post{UserId: "alice", Type: model.PostTypeJoinChannel}}, false},
		{"channel set to all", Input{Me: me, Channel: open, Member: member(model.StringMap{"desktop": "all"}), Post: fromAlice}, true},
		{"channel default falls back to user", Input{Me: me, Channel: open, Member: member(model.StringMap{"desktop": "default"}), Post: fromAlice}, false},
		{"channel set to none", Input{Me: me, Channel: dm, Member: member(model.StringMap{"desktop": "none"}), Post: fromAlice}, false},
		{"muted without mention", Input{Me: me, Channel: open, Member: member(model.StringMap{"desktop": "all", "mark_unread": "mention"}), Post: fromAlice}, false},
		{"muted with mention", Input{Me: me, Channel: open, Member: member(model.StringMap{"mark_unread": "mention"}), Post: fromAlice, MentionsMe: true}, true},
		{"user setting missing defaults to mention", Input{Me: &model.User{Id: "me"}, Channel: open, Post: fromAlice, MentionsMe: true}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ShouldNotify(tc.in); got != tc.want {
				t.Errorf("ShouldNotify = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestPrepareCoalesces(t *testing.T) {
	n := New(config.Notifications{Desktop: true})
	now := time.Now()
	msg := Message{Key: "work/dev", Title: "alice in ~Dev"}

	if id, s := n.prepare(msg, now); id != 0 || s != "alice in ~Dev" {
		t.Fatalf("first = %d %q", id, s)
	}
	n.shown[msg.Key].id = 42
	if id, s := n.prepare(msg, now.Add(time.Second)); id != 42 || s != "alice in ~Dev (2 new)" {
		t.Errorf("second = %d %q", id, s)
	}
	if id, _ := n.prepare(msg, now.Add(time.Hour)); id != 0 {
		t.Errorf("after window replaces = %d, want 0", id)
	}
	if _, s := n.prepare(Message{Key: "work/other", Title: "x"}, now); s != "x" {
		t.Errorf("other key coalesced: %q", s)
	}
}

func TestTerminal(t *testing.T) {
	msg := Message{Title: "alice; in ~Dev", Body: "hi\nthere\x1b]0;evil\a"}

	if got := New(config.Notifications{}).Terminal(msg); got != "" {
		t.Errorf("all off = %q", got)
	}
	if got := New(config.Notifications{Bell: true}).Terminal(msg); got != "\a" {
		t.Errorf("bell = %q", got)
	}
	got := New(config.Notifications{OSC: true}).Terminal(msg)
	want := "\x1b]777;notify;alice, in ~Dev;hi there]0,evil\x1b\\"
	if got != want {
		t.Errorf("osc = %q, want %q", got, want)
	}

	n := New(config.Notifications{Desktop: true})
	n.dbusFailed = true
	if got := n.Terminal(msg); got != "\a" {
		t.Errorf("fallback = %q, want bell", got)
	}
}

func TestBody(t *testing.T) {
	if got := Body("a\n\n  b"); got != "a b" {
		t.Errorf("Body = %q", got)
	}
	long := strings.Repeat("é", 400)
	if got := []rune(Body(long)); len(got) != maxBodyRunes {
		t.Errorf("len = %d", len(got))
	}
}

func TestEscapeMarkup(t *testing.T) {
	got := escapeMarkup(`<img src="file:///home/me/x.png"> Tom & Jerry <b>`)
	want := `&lt;img src="file:///home/me/x.png"&gt; Tom &amp; Jerry &lt;b&gt;`
	if got != want {
		t.Errorf("escapeMarkup = %q, want %q", got, want)
	}
}
