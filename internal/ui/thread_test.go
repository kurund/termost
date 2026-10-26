package ui

import (
	"testing"

	"github.com/mattermost/mattermost/server/public/model"
)

func TestTabIntoThreadSelectsLatest(t *testing.T) {
	m := threadsTestModel(true)
	m.current = chanRef{"work", "c-t1"}
	root := &model.Post{Id: "root", ChannelId: "c-t1"}
	reply := &model.Post{Id: "reply", ChannelId: "c-t1", RootId: "root"}
	latest := &model.Post{Id: "latest", ChannelId: "c-t1", RootId: "root"}

	m.openThread(root)
	m.thread.posts = []*model.Post{root, reply, latest}
	m.enterMessages()
	if m.selected != "latest" {
		t.Errorf("thread opened from its root: selected = %q, want the latest reply", m.selected)
	}

	m.closeThread()
	m.openThread(reply)
	m.thread.posts = []*model.Post{root, reply, latest}
	m.enterMessages()
	if m.selected != "reply" {
		t.Errorf("thread opened from a reply: selected = %q, want that reply", m.selected)
	}
}
