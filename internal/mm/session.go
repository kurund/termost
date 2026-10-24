package mm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/mattermost/mattermost/server/public/model"

	"github.com/kurund/termost/internal/config"
	"github.com/kurund/termost/internal/store"
	"github.com/kurund/termost/internal/termsafe"
)

var ErrUnauthorized = errors.New("session expired or invalid, run `termost login`")

type Session struct {
	Server config.Server
	Client *model.Client4
	Me     *model.User

	expired atomic.Bool
	wsMu    sync.Mutex
	ws      *model.WebSocketClient
}

// TakeExpired reports whether a request was rejected as unauthorised since
// the last call.
func (s *Session) TakeExpired() bool {
	return s.expired.Swap(false)
}

func (s *Session) check(resp *model.Response, err error) error {
	err = apiError(resp, err)
	if errors.Is(err, ErrUnauthorized) {
		s.expired.Store(true)
	}
	return err
}

// Relogin replaces the session token and drops the websocket so it
// reconnects with the new one.
func (s *Session) Relogin(ctx context.Context, password string) error {
	_, resp, err := s.Client.Login(ctx, s.Server.Username, password)
	if resp != nil && resp.StatusCode == http.StatusUnauthorized {
		return errors.New("invalid password")
	}
	if err != nil {
		return apiError(resp, err)
	}
	s.expired.Store(false)
	s.wsMu.Lock()
	if s.ws != nil {
		s.ws.Close()
	}
	s.wsMu.Unlock()
	return nil
}

func Login(ctx context.Context, srv config.Server, password string) (*Session, error) {
	client := model.NewAPIv4Client(srv.URL)
	me, resp, err := client.Login(ctx, srv.Username, password)
	if resp != nil && resp.StatusCode == http.StatusUnauthorized {
		return nil, errors.New("invalid username or password")
	}
	if err != nil {
		return nil, apiError(resp, err)
	}
	return &Session{Server: srv, Client: client, Me: me}, nil
}

func Resume(ctx context.Context, srv config.Server, token string) (*Session, error) {
	client := model.NewAPIv4Client(srv.URL)
	client.SetToken(token)
	me, resp, err := client.GetMe(ctx, "")
	if err != nil {
		return nil, apiError(resp, err)
	}
	return &Session{Server: srv, Client: client, Me: me}, nil
}

func (s *Session) Token() string {
	return s.Client.AuthToken
}

func (s *Session) Teams(ctx context.Context) ([]*model.Team, error) {
	teams, resp, err := s.Client.GetTeamsForUser(ctx, s.Me.Id, "")
	if err != nil {
		return nil, s.check(resp, err)
	}
	return teams, nil
}

// Channels returns the user's channels in a team and their membership
// records keyed by channel ID. DM and group channels are included with
// every team.
func (s *Session) Channels(ctx context.Context, teamID string) ([]*model.Channel, map[string]model.ChannelMember, error) {
	channels, resp, err := s.Client.GetChannelsForTeamForUser(ctx, teamID, s.Me.Id, false, "")
	if err != nil {
		return nil, nil, s.check(resp, err)
	}
	members, resp, err := s.Client.GetChannelMembersForUser(ctx, s.Me.Id, teamID, "")
	if err != nil {
		return nil, nil, s.check(resp, err)
	}
	byChannel := make(map[string]model.ChannelMember, len(members))
	for _, m := range members {
		byChannel[m.ChannelId] = m
	}
	return channels, byChannel, nil
}

// ClientConfig asks for format=old, which every server version accepts; the
// SDK's GetClientConfig omits it and older servers reject that request.
func (s *Session) ClientConfig(ctx context.Context) (map[string]string, error) {
	r, err := s.Client.DoAPIGet(ctx, "/config/client?format=old", "")
	if err != nil {
		return nil, s.check(model.BuildResponse(r), err)
	}
	defer func() { _ = r.Body.Close() }()
	cfg, _, err := model.DecodeJSONFromResponse[map[string]string](r)
	return cfg, err
}

func (s *Session) Statuses(ctx context.Context, userIDs []string) ([]*model.Status, error) {
	if len(userIDs) == 0 {
		return nil, nil
	}
	statuses, resp, err := s.Client.GetUsersStatusesByIds(ctx, userIDs)
	if err != nil {
		return nil, s.check(resp, err)
	}
	return statuses, nil
}

func (s *Session) Categories(ctx context.Context, teamID string) (*model.OrderedSidebarCategories, error) {
	cats, resp, err := s.Client.GetSidebarCategoriesForTeamForUser(ctx, s.Me.Id, teamID, "")
	if err != nil {
		return nil, s.check(resp, err)
	}
	return cats, nil
}

func (s *Session) Users(ctx context.Context, ids []string) (map[string]*model.User, error) {
	out := make(map[string]*model.User, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	users, resp, err := s.Client.GetUsersByIds(ctx, ids)
	if err != nil {
		return nil, s.check(resp, err)
	}
	for _, u := range users {
		out[u.Id] = u
	}
	return out, nil
}

type Event struct {
	Server string
	*model.WebSocketEvent
}

// Listen streams websocket events into out until ctx is cancelled,
// reconnecting with exponential backoff. status, if set, receives nil on each
// successful connect and the error on each disconnect.
func (s *Session) Listen(ctx context.Context, out chan<- Event, status func(error)) error {
	backoff := time.Second
	for {
		connectedAt := time.Now()
		err := s.listenOnce(ctx, out, status)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if status != nil {
			status(err)
		}
		if time.Since(connectedAt) > time.Minute {
			backoff = time.Second
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(backoff):
		}
		backoff = min(backoff*2, time.Minute)
	}
}

func (s *Session) listenOnce(ctx context.Context, out chan<- Event, status func(error)) error {
	ws, appErr := model.NewWebSocketClient4(websocketURL(s.Server.URL), s.Token())
	if appErr != nil {
		return appErr
	}
	defer ws.Close()
	s.wsMu.Lock()
	s.ws = ws
	s.wsMu.Unlock()
	ws.Listen()
	if status != nil {
		status(nil)
	}

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ws.PingTimeoutChannel:
			return errors.New("websocket ping timeout")
		case <-ws.ResponseChannel:
		case ev, ok := <-ws.EventChannel:
			if !ok {
				if ws.ListenError != nil {
					return ws.ListenError
				}
				return errors.New("websocket closed")
			}
			select {
			case out <- Event{Server: s.Server.Name, WebSocketEvent: ev}:
			case <-ctx.Done():
				return nil
			}
		}
	}
}

func websocketURL(serverURL string) string {
	switch {
	case strings.HasPrefix(serverURL, "https://"):
		return "wss://" + strings.TrimPrefix(serverURL, "https://")
	case strings.HasPrefix(serverURL, "http://"):
		return "ws://" + strings.TrimPrefix(serverURL, "http://")
	}
	return serverURL
}

func apiError(resp *model.Response, err error) error {
	if resp != nil && resp.StatusCode == http.StatusUnauthorized {
		return ErrUnauthorized
	}
	var appErr *model.AppError
	if errors.As(err, &appErr) && appErr.Message != "" {
		return fmt.Errorf("%s (%s)", termsafe.Line(appErr.Message), termsafe.Line(appErr.Id))
	}
	return err
}

// Load fetches teams, channels, memberships and DM partner names into a
// fresh store.
func (s *Session) Load(ctx context.Context) (*store.Server, error) {
	teams, err := s.Teams(ctx)
	if err != nil {
		return nil, err
	}
	st := store.NewServer(s.Server.Name, s.Me)
	for _, team := range teams {
		channels, members, err := s.Channels(ctx, team.Id)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", team.Name, err)
		}
		st.AddTeam(team, channels, members)
		cats, err := s.Categories(ctx, team.Id)
		if err != nil {
			slog.Warn("sidebar categories unavailable, using a flat channel list", "team", team.Name, "err", err)
		}
		st.SetCategories(team.Id, cats)
	}
	users, err := s.Users(ctx, st.DMUserIDs())
	if err != nil {
		return nil, err
	}
	st.AddUsers(users)
	prefs, resp, err := s.Client.GetPreferences(ctx, s.Me.Id)
	if err != nil {
		return nil, s.check(resp, err)
	}
	st.ApplyDMPreferences(prefs, false)
	cfg, err := s.ClientConfig(ctx)
	if err != nil {
		slog.Warn("client config unavailable, assuming collapsed reply threads are off", "err", err)
	}
	st.CRT = store.CRTEnabled(cfg["CollapsedThreads"], prefs)
	if st.CRT {
		for _, team := range teams {
			totals, err := s.ThreadTotals(ctx, team.Id)
			if err != nil {
				slog.Warn("thread totals unavailable", "team", team.Name, "err", err)
				continue
			}
			st.SetThreadTotals(team.Id, totals)
		}
	}
	statuses, err := s.Statuses(ctx, append(st.DMUserIDs(), s.Me.Id))
	if err != nil {
		slog.Warn("loading statuses failed", "err", err)
	}
	st.SetStatuses(statuses)
	return st, nil
}

// Posts returns up to perPage posts, newest page first, or the page before
// beforeID when set. hasMore reports whether older posts may exist.
func (s *Session) Posts(ctx context.Context, channelID, beforeID string, perPage int, collapsed bool) (posts []*model.Post, hasMore bool, err error) {
	var list *model.PostList
	var resp *model.Response
	if beforeID == "" {
		list, resp, err = s.Client.GetPostsForChannel(ctx, channelID, 0, perPage, "", collapsed, false)
	} else {
		list, resp, err = s.Client.GetPostsBefore(ctx, channelID, beforeID, 0, perPage, "", collapsed, false)
	}
	if err != nil {
		return nil, false, s.check(resp, err)
	}
	posts = list.ToSlice()
	return posts, len(posts) >= perPage, nil
}

// Thread returns a root post and all its replies, oldest first.
func (s *Session) Thread(ctx context.Context, rootID string) ([]*model.Post, error) {
	list, resp, err := s.Client.GetPostThread(ctx, rootID, "", false)
	if err != nil {
		return nil, s.check(resp, err)
	}
	list.SortByCreateAt()
	posts := list.ToSlice()
	for i, j := 0, len(posts)-1; i < j; i, j = i+1, j-1 {
		posts[i], posts[j] = posts[j], posts[i]
	}
	return posts, nil
}

// ToggleReaction adds emoji to the post, or removes it when remove is set.
func (s *Session) ToggleReaction(ctx context.Context, postID, emoji string, remove bool) error {
	r := &model.Reaction{UserId: s.Me.Id, PostId: postID, EmojiName: emoji}
	if remove {
		resp, err := s.Client.DeleteReaction(ctx, r)
		if err != nil {
			return s.check(resp, err)
		}
		return nil
	}
	_, resp, err := s.Client.SaveReaction(ctx, r)
	if err != nil {
		return s.check(resp, err)
	}
	return nil
}

// Upload attaches data as a new post, in a thread when rootID is set.
type File struct {
	Name string
	Data []byte
}

// Upload posts the files together in one message.
func (s *Session) Upload(ctx context.Context, channelID, rootID, message string, files ...File) error {
	var ids []string
	for _, f := range files {
		res, resp, err := s.Client.UploadFile(ctx, f.Data, channelID, f.Name)
		if err != nil {
			return s.check(resp, err)
		}
		for _, info := range res.FileInfos {
			ids = append(ids, info.Id)
		}
	}
	_, resp, err := s.Client.CreatePost(ctx, &model.Post{ChannelId: channelID, RootId: rootID, Message: message, FileIds: ids})
	if err != nil {
		return s.check(resp, err)
	}
	return nil
}

const searchPageSize = 60

// Search runs a message search in one team; results include DMs and group
// messages, which belong to no team.
func (s *Session) Search(ctx context.Context, teamID, terms string) (*model.PostSearchResults, error) {
	_, offset := time.Now().Zone()
	body, err := json.Marshal(map[string]any{
		"terms":            terms,
		"is_or_search":     false,
		"time_zone_offset": offset,
		"page":             0,
		"per_page":         searchPageSize,
	})
	if err != nil {
		return nil, err
	}
	r, err := s.Client.DoAPIPost(ctx, "/teams/"+teamID+"/posts/search", string(body))
	if err != nil {
		return nil, s.check(model.BuildResponse(r), err)
	}
	defer func() { _ = r.Body.Close() }()
	res, _, err := model.DecodeJSONFromResponse[*model.PostSearchResults](r)
	return res, err
}

// DownloadTo streams a file to path, which only appears once complete.
func (s *Session) DownloadTo(ctx context.Context, fileID, path string) error {
	r, err := s.Client.DoAPIGet(ctx, "/files/"+fileID, "")
	if err != nil {
		return s.check(model.BuildResponse(r), err)
	}
	defer func() { _ = r.Body.Close() }()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".download-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if _, err := io.Copy(tmp, r.Body); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// Command runs a server slash command and returns any text it responds with.
func (s *Session) Command(ctx context.Context, channelID, teamID, command string) (string, error) {
	var res *model.CommandResponse
	var resp *model.Response
	var err error
	if teamID == "" {
		res, resp, err = s.Client.ExecuteCommand(ctx, channelID, command)
	} else {
		res, resp, err = s.Client.ExecuteCommandWithTeam(ctx, channelID, teamID, command)
	}
	if err != nil {
		return "", s.check(resp, err)
	}
	return res.Text, nil
}

func (s *Session) ThreadTotals(ctx context.Context, teamID string) (store.ThreadTotals, error) {
	res, resp, err := s.Client.GetUserThreads(ctx, s.Me.Id, teamID, model.GetUserThreadsOpts{TotalsOnly: true})
	if err != nil {
		return store.ThreadTotals{}, s.check(resp, err)
	}
	return store.ThreadTotals{Unread: res.TotalUnreadThreads, Mentions: res.TotalUnreadMentions}, nil
}

// FollowedThreads lists the threads the user follows in a team, most
// recently active first.
func (s *Session) FollowedThreads(ctx context.Context, teamID string) ([]*model.ThreadResponse, error) {
	res, resp, err := s.Client.GetUserThreads(ctx, s.Me.Id, teamID, model.GetUserThreadsOpts{PageSize: 50, ThreadsOnly: true, Extended: true})
	if err != nil {
		return nil, s.check(resp, err)
	}
	return res.Threads, nil
}

func (s *Session) MarkThreadRead(ctx context.Context, teamID, rootID string) error {
	_, resp, err := s.Client.UpdateThreadReadForUser(ctx, s.Me.Id, teamID, rootID, model.GetMillis())
	var appErr *model.AppError
	if errors.As(err, &appErr) && appErr.Id == "app.user.get_thread_membership_for_user.not_found" {
		return nil
	}
	if err != nil {
		return s.check(resp, err)
	}
	return nil
}

func (s *Session) EditPost(ctx context.Context, postID, message string) error {
	_, resp, err := s.Client.PatchPost(ctx, postID, &model.PostPatch{Message: &message})
	if err != nil {
		return s.check(resp, err)
	}
	return nil
}

func (s *Session) DeletePost(ctx context.Context, postID string) error {
	resp, err := s.Client.DeletePost(ctx, postID)
	if err != nil {
		return s.check(resp, err)
	}
	return nil
}

func (s *Session) Send(ctx context.Context, channelID, rootID, message string) error {
	_, resp, err := s.Client.CreatePost(ctx, &model.Post{ChannelId: channelID, RootId: rootID, Message: message})
	if err != nil {
		return s.check(resp, err)
	}
	return nil
}

func (s *Session) View(ctx context.Context, channelID string) error {
	_, resp, err := s.Client.ViewChannel(ctx, s.Me.Id, &model.ChannelView{ChannelId: channelID})
	if err != nil {
		return s.check(resp, err)
	}
	return nil
}
