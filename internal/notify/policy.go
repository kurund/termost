package notify

import (
	"github.com/mattermost/mattermost/server/public/model"
)

type Input struct {
	Me         *model.User
	Channel    *model.Channel
	Member     *model.ChannelMember
	Post       *model.Post
	MentionsMe bool
	Viewing    bool
}

// ShouldNotify mirrors the official clients: the channel's desktop setting
// overrides the user's, DMs and group messages count as mentions, and muted
// channels only notify on mentions.
func ShouldNotify(in Input) bool {
	if in.Post.UserId == in.Me.Id || in.Viewing || in.Post.IsSystemMessage() {
		return false
	}
	mention := in.MentionsMe || in.Channel.IsGroupOrDirect()
	if Muted(in.Member) && !mention {
		return false
	}
	switch desktopLevel(in.Me, in.Member) {
	case model.UserNotifyAll:
		return true
	case model.UserNotifyMention:
		return mention
	}
	return false
}

func Muted(m *model.ChannelMember) bool {
	return m != nil && m.NotifyProps[model.MarkUnreadNotifyProp] == model.ChannelMarkUnreadMention
}

func desktopLevel(me *model.User, m *model.ChannelMember) string {
	if m != nil {
		if level := m.NotifyProps[model.DesktopNotifyProp]; level != "" && level != model.ChannelNotifyDefault {
			return level
		}
	}
	if level := me.NotifyProps[model.DesktopNotifyProp]; level != "" {
		return level
	}
	return model.UserNotifyMention
}
