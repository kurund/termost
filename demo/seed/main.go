// Command seed resets the demo data recorded by demo/demo.tape on a local test server.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"

	"github.com/mattermost/mattermost/server/public/model"
)

const password = "Passw0rd!x"

type person struct {
	username, first, last, status string
}

var people = []person{
	{"maya", "Maya", "Chen", "online"},
	{"mchen", "Maya", "Chen", "online"},
	{"devon", "Devon", "Park", "online"},
	{"priya", "Priya", "Nair", "away"},
	{"lena", "Lena", "Fischer", "online"},
	{"sam", "Sam", "Okafor", "dnd"},
}

const searchFix = "Probably the new full-text index. This keeps slow queries from piling up:\n" +
	"```go\n" +
	"func (s *Store) Search(ctx context.Context, q string) ([]Post, error) {\n" +
	"\tctx, cancel := context.WithTimeout(ctx, 2*time.Second)\n" +
	"\tdefer cancel()\n" +
	"\treturn s.db.QueryPosts(ctx, q, s.limit)\n" +
	"}\n" +
	"```"

const releaseNotes = "**v2.4.0** is rolling out :rocket:\n\n" +
	"| Service | Status |\n" +
	"|---|---|\n" +
	"| api | :white_check_mark: deployed |\n" +
	"| web | :white_check_mark: deployed |\n" +
	"| worker | :hourglass: canary |"

type seeder struct {
	ctx     context.Context
	url     string
	admin   *model.Client4
	clients map[string]*model.Client4
	ids     map[string]string
	created []string
}

func main() {
	url := flag.String("url", "http://localhost:8065", "server URL")
	adminUser := flag.String("admin", "kurund", "system admin username")
	adminPass := flag.String("admin-password", password, "system admin password")
	flag.Parse()

	s := &seeder{
		ctx:     context.Background(),
		url:     strings.TrimRight(*url, "/"),
		clients: map[string]*model.Client4{},
		ids:     map[string]string{},
	}
	s.admin = model.NewAPIv4Client(s.url)
	if _, _, err := s.admin.Login(s.ctx, *adminUser, *adminPass); err != nil {
		log.Fatalf("admin login: %v", err)
	}
	s.users()
	s.reset()
	s.acme()
	s.openkit()
	s.directMessages()
	s.dropSystemPosts()
	fmt.Println("seeded demo data on", s.url)
}

func (s *seeder) users() {
	for _, p := range people {
		u, _, err := s.admin.GetUserByUsername(s.ctx, p.username, "")
		if err != nil {
			u, _, err = s.admin.CreateUser(s.ctx, &model.User{
				Username:  p.username,
				Email:     p.username + "@example.com",
				Password:  password,
				FirstName: p.first,
				LastName:  p.last,
			})
			must(err, "create user "+p.username)
		}
		s.ids[p.username] = u.Id
		c := model.NewAPIv4Client(s.url)
		_, _, err = c.Login(s.ctx, p.username, password)
		must(err, "login "+p.username)
		s.clients[p.username] = c
		_, _, err = s.admin.UpdateUserStatus(s.ctx, u.Id, &model.Status{UserId: u.Id, Status: p.status})
		must(err, "status "+p.username)
	}
}

func (s *seeder) reset() {
	for _, owner := range []string{"maya", "mchen"} {
		teams, _, err := s.clients[owner].GetTeamsForUser(s.ctx, s.ids[owner], "")
		must(err, "list teams")
		for _, t := range teams {
			if strings.HasPrefix(t.Name, "demo-") {
				_, err := s.clients[owner].SoftDeleteTeam(s.ctx, t.Id)
				must(err, "archive team "+t.Name)
			}
		}
	}
	for _, pair := range [][2]string{{"maya", "lena"}, {"maya", "devon"}, {"maya", "priya"}} {
		ch, _, err := s.admin.CreateDirectChannel(s.ctx, s.ids[pair[0]], s.ids[pair[1]])
		must(err, "dm channel")
		posts, _, err := s.admin.GetPostsForChannel(s.ctx, ch.Id, 0, 200, "", false, false)
		must(err, "dm posts")
		for _, id := range posts.Order {
			_, err := s.admin.DeletePost(s.ctx, id)
			must(err, "delete dm post")
		}
		s.view("maya", ch.Id)
	}
}

func (s *seeder) acme() {
	members := []string{"devon", "priya", "lena", "sam"}
	team := s.team("maya", "Acme", members)
	backend := s.channel("maya", team, "backend", "Backend", members)
	s.channel("maya", team, "frontend", "Frontend", members)
	releases := s.channel("maya", team, "releases", "Releases", members)
	design := s.channel("maya", team, "design", "Design", members)
	town := s.channelByName(team, "town-square")

	s.post("sam", town, "Welcome to the new Acme workspace :wave: Lunch is on us this Friday!", "")
	s.post("lena", design, "New onboarding mockups are up for review :art:", "")

	rel := s.post("devon", releases, releaseNotes, "")
	s.react(rel, "priya", "tada")
	s.react(rel, "sam", "tada")
	s.react(rel, "maya", "rocket")

	s.post("devon", backend, "Seeing p99 latency spikes on `/v4/search` since this morning :eyes:", "")
	fix := s.post("priya", backend, searchFix, "")
	s.react(fix, "devon", "+1")
	s.react(fix, "lena", "+1")
	s.post("devon", backend, "That takes p99 from 1.8s down to 120ms :fire:", fix)
	s.post("sam", backend, "Nice, let's ship it before the release freeze", fix)
	s.post("priya", backend, "@maya could you review? https://github.com/acme/api/pull/482", fix)
	confirm := s.post("lena", backend, "> Probably the new full-text index\n\nConfirmed, dropping `idx_posts_fts_old` fixed staging too.", "")
	s.react(confirm, "priya", "raised_hands")
	s.post("sam", backend, "@maya heads up, standup moved to **10:30** today", "")

	s.favorite("maya", backend)
	s.view("maya", town)
	s.view("maya", design)
}

func (s *seeder) openkit() {
	members := []string{"sam", "devon"}
	team := s.team("mchen", "OpenKit", members)
	contributors := s.channel("mchen", team, "contributors", "Contributors", members)
	town := s.channelByName(team, "town-square")
	s.post("devon", town, "Meetup slides are in the wiki :books:", "")
	s.post("sam", contributors, "@mchen thanks for the docs PR, merged :tada:", "")
	s.view("mchen", town)
}

func (s *seeder) directMessages() {
	devon := s.dm("maya", "devon")
	s.post("maya", devon, "lunch?", "")
	s.post("devon", devon, "always :burrito:", "")
	s.view("maya", devon)

	lena := s.dm("maya", "lena")
	s.post("lena", lena, "Hey! Do you have the slides for Thursday's demo? :pray:", "")

	s.dm("maya", "priya")
	var prefs model.Preferences
	for _, u := range []string{"devon", "lena", "priya"} {
		prefs = append(prefs, model.Preference{UserId: s.ids["maya"], Category: model.PreferenceCategoryDirectChannelShow, Name: s.ids[u], Value: "true"})
	}
	_, err := s.clients["maya"].UpdatePreferences(s.ctx, s.ids["maya"], prefs)
	must(err, "dm preferences")
}

func (s *seeder) team(owner, display string, members []string) string {
	name := "demo-" + strings.ToLower(display) + "-" + strconv.FormatInt(time.Now().Unix(), 36)
	t, _, err := s.clients[owner].CreateTeam(s.ctx, &model.Team{Name: name, DisplayName: display, Type: model.TeamOpen})
	must(err, "create team "+display)
	for _, m := range members {
		_, _, err := s.clients[owner].AddTeamMember(s.ctx, t.Id, s.ids[m])
		must(err, "add team member "+m)
	}
	return t.Id
}

func (s *seeder) channel(owner, team, name, display string, members []string) string {
	ch, _, err := s.clients[owner].CreateChannel(s.ctx, &model.Channel{TeamId: team, Name: name, DisplayName: display, Type: model.ChannelTypeOpen})
	must(err, "create channel "+name)
	s.created = append(s.created, ch.Id)
	for _, m := range members {
		_, _, err := s.clients[owner].AddChannelMember(s.ctx, ch.Id, s.ids[m])
		must(err, "add channel member "+m)
	}
	return ch.Id
}

func (s *seeder) channelByName(team, name string) string {
	ch, _, err := s.admin.GetChannelByName(s.ctx, name, team, "")
	must(err, "channel "+name)
	s.created = append(s.created, ch.Id)
	return ch.Id
}

func (s *seeder) dropSystemPosts() {
	for _, ch := range s.created {
		posts, _, err := s.admin.GetPostsForChannel(s.ctx, ch, 0, 200, "", false, false)
		must(err, "channel posts")
		for _, p := range posts.Posts {
			if p.IsSystemMessage() {
				_, err := s.admin.DeletePost(s.ctx, p.Id)
				must(err, "delete system post")
			}
		}
	}
}

func (s *seeder) dm(a, b string) string {
	ch, _, err := s.clients[a].CreateDirectChannel(s.ctx, s.ids[a], s.ids[b])
	must(err, "dm "+a+" "+b)
	return ch.Id
}

func (s *seeder) post(user, channel, message, root string) string {
	p, _, err := s.clients[user].CreatePost(s.ctx, &model.Post{ChannelId: channel, Message: message, RootId: root})
	must(err, "post as "+user)
	time.Sleep(20 * time.Millisecond)
	return p.Id
}

func (s *seeder) react(post, user, emoji string) {
	_, _, err := s.clients[user].SaveReaction(s.ctx, &model.Reaction{UserId: s.ids[user], PostId: post, EmojiName: emoji})
	must(err, "reaction")
}

func (s *seeder) favorite(user, channel string) {
	_, err := s.clients[user].UpdatePreferences(s.ctx, s.ids[user], model.Preferences{{UserId: s.ids[user], Category: model.PreferenceCategoryFavoriteChannel, Name: channel, Value: "true"}})
	must(err, "favorite")
}

func (s *seeder) view(user, channel string) {
	_, _, err := s.clients[user].ViewChannel(s.ctx, s.ids[user], &model.ChannelView{ChannelId: channel})
	must(err, "view channel")
}

func must(err error, what string) {
	if err != nil {
		log.Fatalf("%s: %v", what, err)
	}
}
