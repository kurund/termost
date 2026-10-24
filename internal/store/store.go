package store

import (
	"sort"
	"strings"

	"github.com/mattermost/mattermost/server/public/model"
)

const MaxPostsPerChannel = 500

type Server struct {
	Name     string
	Me       *model.User
	Teams    []*model.Team
	Channels map[string]*model.Channel
	Members  map[string]*model.ChannelMember
	Users    map[string]*model.User
	Posts    map[string]*Posts

	teamChannels map[string][]string
	dms          []string
	closedDMs    map[string]bool
	categories   map[string][]*model.SidebarCategoryWithChannels
	statuses     map[string]string
	seen         map[string]bool
	threadTotals map[string]ThreadTotals

	// CRT is true when collapsed reply threads are on for this user.
	CRT bool
}

type Posts struct {
	List    []*model.Post
	HasMore bool
}

func NewServer(name string, me *model.User) *Server {
	return &Server{
		Name:         name,
		Me:           me,
		Channels:     map[string]*model.Channel{},
		Members:      map[string]*model.ChannelMember{},
		Users:        map[string]*model.User{me.Id: me},
		Posts:        map[string]*Posts{},
		teamChannels: map[string][]string{},
		closedDMs:    map[string]bool{},
		categories:   map[string][]*model.SidebarCategoryWithChannels{},
		statuses:     map[string]string{},
		seen:         map[string]bool{},
		threadTotals: map[string]ThreadTotals{},
	}
}

// AddTeam registers a team and its channels. DM and group channels are
// returned by the API for every team, so they are collected once per server.
func (s *Server) AddTeam(team *model.Team, channels []*model.Channel, members map[string]model.ChannelMember) {
	s.Teams = append(s.Teams, team)
	sort.Slice(s.Teams, func(i, j int) bool {
		return strings.ToLower(s.Teams[i].DisplayName) < strings.ToLower(s.Teams[j].DisplayName)
	})

	var ids []string
	for _, ch := range channels {
		if _, seen := s.Channels[ch.Id]; !seen && ch.IsGroupOrDirect() {
			s.dms = append(s.dms, ch.Id)
		}
		s.Channels[ch.Id] = ch
		if m, ok := members[ch.Id]; ok {
			s.Members[ch.Id] = &m
		}
		if !ch.IsGroupOrDirect() {
			ids = append(ids, ch.Id)
		}
	}
	sort.Slice(ids, func(i, j int) bool {
		return strings.ToLower(s.Channels[ids[i]].DisplayName) < strings.ToLower(s.Channels[ids[j]].DisplayName)
	})
	s.teamChannels[team.Id] = ids
	s.sortDMs()
}

func (s *Server) TeamChannels(teamID string) []string {
	return s.teamChannels[teamID]
}

func (s *Server) DMs() []string {
	return s.dms
}

// ApplyDMPreferences records DMs the user closed in another client
// (direct_channel_show / group_channel_show = "false").
func (s *Server) ApplyDMPreferences(prefs model.Preferences, deleted bool) {
	for _, p := range prefs {
		var id string
		switch p.Category {
		case model.PreferenceCategoryDirectChannelShow:
			id = s.directChannelWith(p.Name)
		case model.PreferenceCategoryGroupChannelShow:
			id = p.Name
		default:
			continue
		}
		if id != "" {
			s.closedDMs[id] = !deleted && p.Value == "false"
		}
	}
}

func (s *Server) directChannelWith(userID string) string {
	for _, id := range s.dms {
		ch := s.Channels[id]
		if ch.Type == model.ChannelTypeDirect && ch.GetOtherUserIdForDM(s.Me.Id) == userID {
			return id
		}
	}
	return ""
}

// Hidden reports whether the official clients would leave a DM out of the
// sidebar: closed by the user, or with a deactivated user.
func (s *Server) Hidden(channelID string) bool {
	if s.closedDMs[channelID] {
		return true
	}
	ch, ok := s.Channels[channelID]
	if !ok || ch.Type != model.ChannelTypeDirect {
		return false
	}
	u, ok := s.Users[ch.GetOtherUserIdForDM(s.Me.Id)]
	return ok && u.DeleteAt > 0
}

// VisibleDMs returns the limit most recent DMs that are not hidden or shown
// in another category, plus any other DM that is unread or equal to keep.
// limit <= 0 means no limit.
func (s *Server) VisibleDMs(limit int, keep string) []string {
	var out []string
	shown := 0
	elsewhere := s.CategorisedDMs()
	for _, id := range s.dms {
		if elsewhere[id] {
			continue
		}
		u, m := s.Unread(id)
		switch {
		case u > 0 || m > 0 || id == keep:
		case s.Hidden(id) || (limit > 0 && shown >= limit):
			continue
		}
		out = append(out, id)
		shown++
	}
	return out
}

func (s *Server) sortDMs() {
	sort.SliceStable(s.dms, func(i, j int) bool {
		return s.Channels[s.dms[i]].LastPostAt > s.Channels[s.dms[j]].LastPostAt
	})
}

// DMUserIDs returns the other participant of every DM, for name lookup.
func (s *Server) DMUserIDs() []string {
	var ids []string
	for _, id := range s.dms {
		ch := s.Channels[id]
		if ch.Type != model.ChannelTypeDirect {
			continue
		}
		if other := ch.GetOtherUserIdForDM(s.Me.Id); other != "" {
			ids = append(ids, other)
		}
	}
	return ids
}

func (s *Server) SetStatuses(statuses []*model.Status) {
	for _, st := range statuses {
		s.statuses[st.UserId] = st.Status
	}
}

// DMStatus returns the other participant's presence for a DM, or "" for
// other channel types and unknown users.
func (s *Server) DMStatus(channelID string) string {
	ch, ok := s.Channels[channelID]
	if !ok || ch.Type != model.ChannelTypeDirect {
		return ""
	}
	other := ch.GetOtherUserIdForDM(s.Me.Id)
	if other == "" {
		other = s.Me.Id
	}
	return s.statuses[other]
}

// AddUsers ignores records without a username, such as the bare IDs some
// endpoints return, so they never replace a known user.
func (s *Server) AddUsers(users map[string]*model.User) {
	for id, u := range users {
		if u != nil && u.Username != "" {
			s.Users[id] = u
		}
	}
}

func (s *Server) MissingUsers(posts []*model.Post) []string {
	seen := map[string]bool{}
	var ids []string
	for _, p := range posts {
		if _, ok := s.Users[p.UserId]; !ok && !seen[p.UserId] {
			seen[p.UserId] = true
			ids = append(ids, p.UserId)
		}
	}
	return ids
}

func (s *Server) Username(userID string) string {
	if u, ok := s.Users[userID]; ok {
		return u.Username
	}
	return "…"
}

func (s *Server) ChannelName(channelID string) string {
	ch, ok := s.Channels[channelID]
	if !ok {
		return channelID
	}
	if ch.Type == model.ChannelTypeDirect {
		other := ch.GetOtherUserIdForDM(s.Me.Id)
		if other == "" {
			return s.Me.Username + " (you)"
		}
		return s.Username(other)
	}
	return ch.DisplayName
}

// Unread uses root-post counters when collapsed reply threads are on, as the
// official clients do: replies then count towards threads, not the channel.
func (s *Server) Unread(channelID string) (unread, mentions int64) {
	ch, ok := s.Channels[channelID]
	m, mok := s.Members[channelID]
	if !ok || !mok {
		return 0, 0
	}
	if s.CRT {
		return max(ch.TotalMsgCountRoot-m.MsgCountRoot, 0), m.MentionCountRoot
	}
	return max(ch.TotalMsgCount-m.MsgCount, 0), m.MentionCount
}

func (s *Server) Muted(channelID string) bool {
	m, ok := s.Members[channelID]
	return ok && m.NotifyProps[model.MarkUnreadNotifyProp] == model.ChannelMarkUnreadMention
}

// TeamUnread and TotalUnread leave out muted channels' unread counts but
// keep their mentions.
func (s *Server) TeamUnread(teamID string) (unread, mentions int64) {
	for _, id := range s.teamChannels[teamID] {
		u, m := s.Unread(id)
		if !s.Muted(id) {
			unread += u
		}
		mentions += m
	}
	return unread, mentions
}

func (s *Server) TotalUnread() (unread, mentions int64) {
	seen := map[string]bool{}
	ids := append([]string{}, s.dms...)
	for _, team := range s.Teams {
		ids = append(ids, s.teamChannels[team.Id]...)
	}
	for _, id := range ids {
		if seen[id] {
			continue
		}
		seen[id] = true
		u, m := s.Unread(id)
		if !s.Muted(id) {
			unread += u
		}
		mentions += m
	}
	return unread, mentions
}

func (s *Server) MarkViewed(channelID string) {
	ch, ok := s.Channels[channelID]
	m, mok := s.Members[channelID]
	if !ok || !mok {
		return
	}
	m.MsgCount, m.MsgCountRoot = ch.TotalMsgCount, ch.TotalMsgCountRoot
	m.MentionCount, m.MentionCountRoot = 0, 0
}

func (s *Server) SetPosts(channelID string, posts []*model.Post, hasMore bool) {
	sortPosts(posts)
	s.Posts[channelID] = &Posts{List: posts, HasMore: hasMore}
}

func (s *Server) PrependPosts(channelID string, older []*model.Post, hasMore bool) {
	p, ok := s.Posts[channelID]
	if !ok {
		s.SetPosts(channelID, older, hasMore)
		return
	}
	sortPosts(older)
	have := make(map[string]bool, len(p.List))
	for _, post := range p.List {
		have[post.Id] = true
	}
	var fresh []*model.Post
	for _, post := range older {
		if !have[post.Id] {
			fresh = append(fresh, post)
		}
	}
	p.List = append(fresh, p.List...)
	p.HasMore = hasMore
}

// AddPost applies a newly created post. viewing means the user is looking at
// the channel right now, so it must not become unread.
func (s *Server) AddPost(post *model.Post, mentionsMe, viewing bool) {
	if s.seen[post.Id] {
		return
	}
	if len(s.seen) >= 5000 {
		clear(s.seen)
	}
	s.seen[post.Id] = true

	reply := post.RootId != ""
	if p, ok := s.Posts[post.ChannelId]; ok {
		if reply {
			for _, existing := range p.List {
				if existing.Id == post.RootId {
					existing.ReplyCount++
				}
			}
		}
		if !reply || !s.CRT {
			p.List = append(p.List, post)
			if len(p.List) > MaxPostsPerChannel {
				p.List = p.List[len(p.List)-MaxPostsPerChannel:]
				p.HasMore = true
			}
		}
	}

	ch, ok := s.Channels[post.ChannelId]
	if !ok {
		return
	}
	ch.TotalMsgCount++
	if !reply {
		ch.TotalMsgCountRoot++
	}
	ch.LastPostAt = max(ch.LastPostAt, post.CreateAt)
	if ch.IsGroupOrDirect() {
		s.sortDMs()
	}

	m, ok := s.Members[post.ChannelId]
	if !ok {
		return
	}
	if viewing || post.UserId == s.Me.Id {
		m.MsgCount, m.MsgCountRoot = ch.TotalMsgCount, ch.TotalMsgCountRoot
		return
	}
	if mentionsMe {
		m.MentionCount++
		if !reply {
			m.MentionCountRoot++
		}
	}
}

func (s *Server) EditPost(post *model.Post) {
	p, ok := s.Posts[post.ChannelId]
	if !ok {
		return
	}
	for i, existing := range p.List {
		if existing.Id == post.Id {
			p.List[i] = post
			return
		}
	}
}

func (s *Server) DeletePost(post *model.Post) {
	p, ok := s.Posts[post.ChannelId]
	if !ok {
		return
	}
	for i := 0; i < len(p.List); i++ {
		existing := p.List[i]
		switch {
		case existing.Id == post.Id:
			p.List = append(p.List[:i], p.List[i+1:]...)
			i--
		case post.RootId != "" && existing.Id == post.RootId && existing.ReplyCount > 0:
			existing.ReplyCount--
		}
	}
}

func sortPosts(posts []*model.Post) {
	sort.Slice(posts, func(i, j int) bool { return posts[i].CreateAt < posts[j].CreateAt })
}

// CRTEnabled mirrors the web app: the server setting decides unless it lets
// users choose, then the user's display preference wins.
func CRTEnabled(serverSetting string, prefs model.Preferences) bool {
	switch serverSetting {
	case model.CollapsedThreadsDisabled, "":
		return false
	case model.CollapsedThreadsAlwaysOn:
		return true
	}
	for _, p := range prefs {
		if p.Category == model.PreferenceCategoryDisplaySettings && p.Name == model.PreferenceNameCollapsedThreadsEnabled {
			return p.Value == "on"
		}
	}
	return serverSetting == model.CollapsedThreadsDefaultOn
}

type ThreadTotals struct {
	Unread   int64
	Mentions int64
}

func (s *Server) SetThreadTotals(teamID string, t ThreadTotals) {
	s.threadTotals[teamID] = t
}

func (s *Server) ThreadTotals(teamID string) ThreadTotals {
	return s.threadTotals[teamID]
}

// ThreadTeam is the team to use for thread API calls on a channel; DM and
// group threads are reachable through any of the user's teams.
func (s *Server) ThreadTeam(channelID string) string {
	if ch, ok := s.Channels[channelID]; ok && ch.TeamId != "" {
		return ch.TeamId
	}
	if len(s.Teams) > 0 {
		return s.Teams[0].Id
	}
	return ""
}
