package store

import (
	"reflect"
	"strings"
	"testing"

	"github.com/mattermost/mattermost/server/public/model"
)

const (
	meID    = "me00000000000000000000000a"
	aliceID = "alice000000000000000000000"
)

func fixture() *Server {
	s := NewServer("work", &model.User{Id: meID, Username: "me"})
	dm := &model.Channel{Id: "dm", Type: model.ChannelTypeDirect, Name: model.GetDMNameFromIds(meID, aliceID), TotalMsgCount: 3, LastPostAt: 10}
	dev := &model.Channel{Id: "dev", Type: model.ChannelTypeOpen, DisplayName: "Dev", TotalMsgCount: 5}
	town := &model.Channel{Id: "town", Type: model.ChannelTypeOpen, DisplayName: "Town Square", TotalMsgCount: 2}
	members := map[string]model.ChannelMember{
		"dm":   {ChannelId: "dm", MsgCount: 3},
		"dev":  {ChannelId: "dev", MsgCount: 3, MentionCount: 1},
		"town": {ChannelId: "town", MsgCount: 2},
	}
	s.AddTeam(&model.Team{Id: "eng", DisplayName: "Engineering"}, []*model.Channel{town, dm, dev}, members)
	s.AddTeam(&model.Team{Id: "sup", DisplayName: "Support"}, []*model.Channel{dm}, members)
	return s
}

func TestAddTeam(t *testing.T) {
	s := fixture()
	if got := s.TeamChannels("eng"); len(got) != 2 || got[0] != "dev" || got[1] != "town" {
		t.Errorf("eng channels = %v, want [dev town]", got)
	}
	if got := s.DMs(); len(got) != 1 {
		t.Errorf("DMs = %v, want one entry despite appearing in two teams", got)
	}
	if u, m := s.Unread("dev"); u != 2 || m != 1 {
		t.Errorf("Unread(dev) = %d,%d want 2,1", u, m)
	}
	if u, m := s.TotalUnread(); u != 2 || m != 1 {
		t.Errorf("TotalUnread = %d,%d want 2,1", u, m)
	}
}

func TestChannelName(t *testing.T) {
	s := fixture()
	if got := s.ChannelName("dm"); got != "…" {
		t.Errorf("unknown DM user = %q", got)
	}
	s.AddUsers(map[string]*model.User{aliceID: {Id: aliceID, Username: "alice"}})
	if got := s.ChannelName("dm"); got != "alice" {
		t.Errorf("ChannelName(dm) = %q, want alice", got)
	}
	if got := s.ChannelName("dev"); got != "Dev" {
		t.Errorf("ChannelName(dev) = %q", got)
	}
}

func TestAddPostUnread(t *testing.T) {
	cases := []struct {
		name              string
		user              string
		mentions, viewing bool
		wantUnread        int64
		wantMentions      int64
	}{
		{"other user", aliceID, false, false, 1, 0},
		{"mention", aliceID, true, false, 1, 1},
		{"while viewing", aliceID, true, true, 0, 0},
		{"own post", meID, false, false, 0, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := fixture()
			s.SetPosts("dm", nil, false)
			s.AddPost(&model.Post{Id: "p1", ChannelId: "dm", UserId: tc.user, CreateAt: 20}, tc.mentions, tc.viewing)
			if u, m := s.Unread("dm"); u != tc.wantUnread || m != tc.wantMentions {
				t.Errorf("Unread = %d,%d want %d,%d", u, m, tc.wantUnread, tc.wantMentions)
			}
			if got := len(s.Posts["dm"].List); got != 1 {
				t.Errorf("posts = %d, want 1", got)
			}
			if s.Channels["dm"].LastPostAt != 20 {
				t.Error("LastPostAt not updated")
			}
		})
	}
}

func TestAddPostDuplicateIgnored(t *testing.T) {
	s := fixture()
	s.SetPosts("dev", nil, false)
	p := &model.Post{Id: "p1", ChannelId: "dev", UserId: aliceID}
	s.AddPost(p, false, false)
	s.AddPost(p, false, false)
	if u, _ := s.Unread("dev"); u != 3 {
		t.Errorf("unread = %d, want 3 (duplicate counted)", u)
	}
}

func TestDMsReorderOnPost(t *testing.T) {
	s := fixture()
	other := &model.Channel{Id: "dm2", Type: model.ChannelTypeDirect, Name: model.GetDMNameFromIds(meID, "bob00000000000000000000000"), LastPostAt: 50}
	s.AddTeam(&model.Team{Id: "x", DisplayName: "X"}, []*model.Channel{other}, nil)
	if s.DMs()[0] != "dm2" {
		t.Fatalf("DMs = %v, want dm2 first", s.DMs())
	}
	s.AddPost(&model.Post{Id: "p", ChannelId: "dm", UserId: aliceID, CreateAt: 99}, false, false)
	if s.DMs()[0] != "dm" {
		t.Errorf("DMs = %v, want dm first after new post", s.DMs())
	}
}

func TestMarkViewed(t *testing.T) {
	s := fixture()
	s.MarkViewed("dev")
	if u, m := s.Unread("dev"); u != 0 || m != 0 {
		t.Errorf("Unread after MarkViewed = %d,%d", u, m)
	}
}

func TestPostsLifecycle(t *testing.T) {
	s := fixture()
	s.SetPosts("dev", []*model.Post{
		{Id: "c", ChannelId: "dev", CreateAt: 3},
		{Id: "b", ChannelId: "dev", CreateAt: 2},
	}, true)
	s.PrependPosts("dev", []*model.Post{
		{Id: "b", ChannelId: "dev", CreateAt: 2},
		{Id: "a", ChannelId: "dev", CreateAt: 1},
	}, false)

	got := ids(s.Posts["dev"].List)
	if got != "abc" {
		t.Fatalf("order = %s, want abc", got)
	}
	if s.Posts["dev"].HasMore {
		t.Error("HasMore should be false")
	}

	s.EditPost(&model.Post{Id: "b", ChannelId: "dev", CreateAt: 2, Message: "edited"})
	if s.Posts["dev"].List[1].Message != "edited" {
		t.Error("edit not applied")
	}
	s.DeletePost(&model.Post{Id: "b", ChannelId: "dev"})
	if got := ids(s.Posts["dev"].List); got != "ac" {
		t.Errorf("after delete = %s, want ac", got)
	}
}

func TestPostCap(t *testing.T) {
	s := fixture()
	s.SetPosts("dev", nil, false)
	for i := range MaxPostsPerChannel + 5 {
		s.AddPost(&model.Post{Id: model.NewId(), ChannelId: "dev", UserId: aliceID, CreateAt: int64(i)}, false, true)
	}
	p := s.Posts["dev"]
	if len(p.List) != MaxPostsPerChannel || !p.HasMore {
		t.Errorf("len = %d HasMore = %v", len(p.List), p.HasMore)
	}
}

func ids(posts []*model.Post) string {
	var out string
	for _, p := range posts {
		out += p.Id
	}
	return out
}

func TestMutedExcludedFromTotals(t *testing.T) {
	s := fixture()
	s.Members["dev"].NotifyProps = model.StringMap{model.MarkUnreadNotifyProp: model.ChannelMarkUnreadMention}
	if !s.Muted("dev") || s.Muted("town") {
		t.Fatal("Muted wrong")
	}
	if u, m := s.TotalUnread(); u != 0 || m != 1 {
		t.Errorf("TotalUnread = %d,%d want 0,1", u, m)
	}
	if u, m := s.TeamUnread("eng"); u != 0 || m != 1 {
		t.Errorf("TeamUnread = %d,%d want 0,1", u, m)
	}
}

func TestVisibleDMs(t *testing.T) {
	s := NewServer("work", &model.User{Id: meID})
	var chans []*model.Channel
	members := map[string]model.ChannelMember{}
	for i := range 5 {
		id := string(rune('a' + i))
		chans = append(chans, &model.Channel{Id: id, Type: model.ChannelTypeGroup, LastPostAt: int64(100 - i), TotalMsgCount: 1})
		members[id] = model.ChannelMember{ChannelId: id, MsgCount: 1}
	}
	members["e"] = model.ChannelMember{ChannelId: "e"}
	s.AddTeam(&model.Team{Id: "t"}, chans, members)

	if got := strings.Join(s.VisibleDMs(2, ""), ""); got != "abe" {
		t.Errorf("limit 2 = %s, want abe (e is unread)", got)
	}
	if got := strings.Join(s.VisibleDMs(2, "d"), ""); got != "abde" {
		t.Errorf("keep d = %s, want abde", got)
	}
	if got := len(s.VisibleDMs(0, "")); got != 5 {
		t.Errorf("no limit = %d", got)
	}
}

func TestHiddenDMs(t *testing.T) {
	s := NewServer("work", &model.User{Id: meID})
	bobID := "bob00000000000000000000000"
	carolID := "carol0000000000000000000000"
	chans := []*model.Channel{
		{Id: "alice", Type: model.ChannelTypeDirect, Name: model.GetDMNameFromIds(meID, aliceID), LastPostAt: 30, TotalMsgCount: 1},
		{Id: "bob", Type: model.ChannelTypeDirect, Name: model.GetDMNameFromIds(meID, bobID), LastPostAt: 20, TotalMsgCount: 1},
		{Id: "carol", Type: model.ChannelTypeDirect, Name: model.GetDMNameFromIds(meID, carolID), LastPostAt: 10, TotalMsgCount: 1},
		{Id: "group", Type: model.ChannelTypeGroup, LastPostAt: 5, TotalMsgCount: 1},
	}
	members := map[string]model.ChannelMember{}
	for _, c := range chans {
		members[c.Id] = model.ChannelMember{ChannelId: c.Id, MsgCount: 1}
	}
	s.AddTeam(&model.Team{Id: "t"}, chans, members)
	s.AddUsers(map[string]*model.User{carolID: {Id: carolID, Username: "carol", DeleteAt: 1}})
	s.ApplyDMPreferences(model.Preferences{
		{Category: model.PreferenceCategoryDirectChannelShow, Name: bobID, Value: "false"},
		{Category: model.PreferenceCategoryGroupChannelShow, Name: "group", Value: "false"},
		{Category: "display_settings", Name: "x", Value: "false"},
	}, false)

	if got := strings.Join(s.VisibleDMs(0, ""), ","); got != "alice" {
		t.Errorf("visible = %s, want alice (bob/group closed, carol deactivated)", got)
	}
	if got := strings.Join(s.VisibleDMs(0, "bob"), ","); got != "alice,bob" {
		t.Errorf("keep bob = %s", got)
	}

	s.Channels["group"].TotalMsgCount = 2
	if got := strings.Join(s.VisibleDMs(0, ""), ","); got != "alice,group" {
		t.Errorf("unread closed DM = %s, want alice,group", got)
	}

	s.ApplyDMPreferences(model.Preferences{{Category: model.PreferenceCategoryDirectChannelShow, Name: bobID, Value: "false"}}, true)
	if got := strings.Join(s.VisibleDMs(0, ""), ","); got != "alice,bob,group" {
		t.Errorf("after preference deleted = %s", got)
	}
	if got := strings.Join(s.VisibleDMs(1, ""), ","); got != "alice,group" {
		t.Errorf("limit 1 = %s, want alice plus unread group", got)
	}
}

func TestCategories(t *testing.T) {
	s := fixture()
	s.Channels["dev"].LastPostAt = 1
	s.Channels["town"].LastPostAt = 9
	cat := func(id string, typ model.SidebarCategoryType, sorting model.SidebarCategorySorting, channels ...string) *model.SidebarCategoryWithChannels {
		return &model.SidebarCategoryWithChannels{
			SidebarCategory: model.SidebarCategory{Id: id, Type: typ, DisplayName: id, Sorting: sorting},
			Channels:        channels,
		}
	}

	if got := s.TeamCategories("eng"); len(got) != 1 || got[0].Name != "Channels" || strings.Join(got[0].Channels, ",") != "dev,town" {
		t.Fatalf("fallback = %+v", got)
	}

	s.SetCategories("eng", &model.OrderedSidebarCategories{
		Categories: model.SidebarCategoriesWithChannels{
			cat("Channels", model.SidebarCategoryChannels, model.SidebarCategorySortAlphabetical, "town", "dev", "unknown"),
			cat("Favorites", model.SidebarCategoryFavorites, model.SidebarCategorySortRecent, "dev", "dm"),
			cat("DMs", model.SidebarCategoryDirectMessages, ""),
			cat("Mine", model.SidebarCategoryCustom, model.SidebarCategorySortManual, "town", "dev"),
		},
		Order: []string{"Favorites", "Mine", "Channels", "DMs"},
	})

	var got []string
	for _, c := range s.TeamCategories("eng") {
		got = append(got, c.Name+":"+strings.Join(c.Channels, ","))
	}
	want := "Favorites:dm,dev Mine:town,dev Channels:dev,town"
	if strings.Join(got, " ") != want {
		t.Errorf("categories = %s\nwant         %s", strings.Join(got, " "), want)
	}

	if !s.CategorisedDMs()["dm"] {
		t.Error("favourited DM not reported")
	}
	if got := s.VisibleDMs(0, ""); len(got) != 0 {
		t.Errorf("favourited DM still in DM list: %v", got)
	}
}

func TestDMStatus(t *testing.T) {
	s := fixture()
	if got := s.DMStatus("dm"); got != "" {
		t.Errorf("unknown status = %q", got)
	}
	s.SetStatuses([]*model.Status{{UserId: aliceID, Status: model.StatusAway}})
	if got := s.DMStatus("dm"); got != model.StatusAway {
		t.Errorf("DMStatus = %q", got)
	}
	if got := s.DMStatus("dev"); got != "" {
		t.Errorf("channel status = %q", got)
	}
}

func TestCRTCounts(t *testing.T) {
	s := fixture()
	s.CRT = true
	s.Channels["dev"].TotalMsgCountRoot = 4
	s.Members["dev"].MsgCountRoot = 3
	s.Members["dev"].MentionCountRoot = 0
	root := &model.Post{Id: "root", ChannelId: "dev", UserId: aliceID, CreateAt: 1}
	s.SetPosts("dev", []*model.Post{root}, false)

	if u, m := s.Unread("dev"); u != 1 || m != 0 {
		t.Fatalf("root counters = %d,%d want 1,0", u, m)
	}
	s.AddPost(&model.Post{Id: "r1", ChannelId: "dev", RootId: "root", UserId: aliceID, CreateAt: 2}, true, false)
	if u, m := s.Unread("dev"); u != 1 || m != 0 {
		t.Errorf("reply changed channel unread: %d,%d", u, m)
	}
	if got := len(s.Posts["dev"].List); got != 1 {
		t.Errorf("reply added to channel list under CRT: %d posts", got)
	}
	if root.ReplyCount != 1 {
		t.Errorf("ReplyCount = %d, want 1", root.ReplyCount)
	}
	s.AddPost(&model.Post{Id: "p2", ChannelId: "dev", UserId: aliceID, CreateAt: 3}, true, false)
	if u, m := s.Unread("dev"); u != 2 || m != 1 {
		t.Errorf("after root post = %d,%d want 2,1", u, m)
	}
	s.MarkViewed("dev")
	if u, m := s.Unread("dev"); u != 0 || m != 0 {
		t.Errorf("after MarkViewed = %d,%d", u, m)
	}
	s.DeletePost(&model.Post{Id: "r1", ChannelId: "dev", RootId: "root"})
	if root.ReplyCount != 0 {
		t.Errorf("ReplyCount after delete = %d", root.ReplyCount)
	}
}

func TestRepliesInlineWithoutCRT(t *testing.T) {
	s := fixture()
	root := &model.Post{Id: "root", ChannelId: "dev", UserId: aliceID}
	s.SetPosts("dev", []*model.Post{root}, false)
	s.AddPost(&model.Post{Id: "r1", ChannelId: "dev", RootId: "root", UserId: aliceID, CreateAt: 2}, false, false)
	if len(s.Posts["dev"].List) != 2 || root.ReplyCount != 1 {
		t.Errorf("posts = %d, ReplyCount = %d", len(s.Posts["dev"].List), root.ReplyCount)
	}
}

func TestCRTEnabled(t *testing.T) {
	on := model.Preferences{{Category: model.PreferenceCategoryDisplaySettings, Name: model.PreferenceNameCollapsedThreadsEnabled, Value: "on"}}
	off := model.Preferences{{Category: model.PreferenceCategoryDisplaySettings, Name: model.PreferenceNameCollapsedThreadsEnabled, Value: "off"}}
	cases := []struct {
		setting string
		prefs   model.Preferences
		want    bool
	}{
		{model.CollapsedThreadsDisabled, on, false},
		{model.CollapsedThreadsAlwaysOn, off, true},
		{model.CollapsedThreadsDefaultOn, nil, true},
		{model.CollapsedThreadsDefaultOn, off, false},
		{model.CollapsedThreadsDefaultOff, nil, false},
		{model.CollapsedThreadsDefaultOff, on, true},
		{"", on, false},
	}
	for _, tc := range cases {
		if got := CRTEnabled(tc.setting, tc.prefs); got != tc.want {
			t.Errorf("CRTEnabled(%q, %v) = %v", tc.setting, tc.prefs, got)
		}
	}
}

func TestReactions(t *testing.T) {
	s := fixture()
	p := &model.Post{Id: "p", ChannelId: "dev"}
	s.SetPosts("dev", []*model.Post{p}, false)
	r := func(user, emoji string) *model.Reaction {
		return &model.Reaction{PostId: "p", UserId: user, EmojiName: emoji}
	}
	s.ApplyReaction("dev", r(aliceID, "+1"), true)
	s.ApplyReaction("dev", r(meID, "tada"), true)
	s.ApplyReaction("dev", r(meID, "+1"), true)
	s.ApplyReaction("dev", r(meID, "+1"), true)

	got := s.Reactions(p)
	if len(got) != 2 || !reflect.DeepEqual(got[0], ReactionSummary{"+1", 2, true, []string{aliceID, meID}}) ||
		!reflect.DeepEqual(got[1], ReactionSummary{"tada", 1, true, []string{meID}}) {
		t.Fatalf("summary = %+v", got)
	}
	if !s.HasReacted(p, "tada") || s.HasReacted(p, "heart") {
		t.Error("HasReacted wrong")
	}
	s.ApplyReaction("dev", r(meID, "+1"), false)
	if got := s.Reactions(p); !reflect.DeepEqual(got[0], ReactionSummary{"+1", 1, false, []string{aliceID}}) {
		t.Errorf("after removal = %+v", got)
	}
	if ApplyReaction(nil, r(meID, "x"), true) {
		t.Error("missing post reported as found")
	}
}

func TestAddUsersIgnoresBareIDs(t *testing.T) {
	s := fixture()
	s.AddUsers(map[string]*model.User{aliceID: {Id: aliceID, Username: "alice"}})
	s.AddUsers(map[string]*model.User{aliceID: {Id: aliceID}})
	if got := s.Username(aliceID); got != "alice" {
		t.Errorf("Username = %q, bare ID overwrote the known user", got)
	}
}
