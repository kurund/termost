package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"runtime/debug"
	"sort"
	"strings"
	"sync"
	"syscall"

	"github.com/mattermost/mattermost/server/public/model"
	"golang.org/x/term"

	"github.com/kurund/termost/internal/auth"
	"github.com/kurund/termost/internal/config"
	"github.com/kurund/termost/internal/mm"
	"github.com/kurund/termost/internal/notify"
	"github.com/kurund/termost/internal/ui"
)

const usage = `usage: termost [command] [server...]

With no command, termost opens the chat UI.

commands:
  login        log in with your password and store the session token
  logout       forget the stored session token
  channels     list teams, channels and DMs with unread counts
  tail         print new messages from all servers as they arrive
  notify-test  send a test desktop notification
  version      print the termost version

Servers default to all entries in the config file.
`

var version = "dev"

func main() {
	cmd, names := "ui", os.Args[1:]
	if len(os.Args) > 1 && !strings.HasPrefix(os.Args[1], "-") {
		cmd, names = os.Args[1], os.Args[2:]
	}
	if len(names) > 0 && strings.HasPrefix(names[0], "-") {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	if cmd == "version" {
		fmt.Println("termost", buildVersion())
		return
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := run(ctx, cmd, names); err != nil {
		fmt.Fprintln(os.Stderr, "termost:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, cmd string, names []string) error {
	cfg, err := config.Load()
	if errors.Is(err, config.ErrNotFound) {
		return fmt.Errorf("%w\nsee config.example.toml for the format", err)
	}
	if err != nil {
		return err
	}
	servers, err := cfg.Select(names)
	if err != nil {
		return err
	}
	store, err := auth.NewTokenStore()
	if err != nil {
		return err
	}

	switch cmd {
	case "ui":
		return runUI(ctx, store, servers, cfg)
	case "login":
		return login(ctx, store, servers)
	case "logout":
		return logout(ctx, store, servers)
	case "channels":
		return channels(ctx, store, servers)
	case "tail":
		return tail(ctx, store, servers)
	case "notify-test":
		return notifyTest(cfg.Notifications)
	}
	fmt.Fprint(os.Stderr, usage)
	return fmt.Errorf("unknown command %q", cmd)
}

func buildVersion() string {
	if version != "dev" {
		return version
	}
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" && info.Main.Version != "(devel)" {
		return info.Main.Version
	}
	return version
}

func login(ctx context.Context, store *auth.TokenStore, servers []config.Server) error {
	for _, srv := range servers {
		password, err := readPassword(fmt.Sprintf("Password for %s on %s: ", srv.Username, srv.Name))
		if err != nil {
			return err
		}
		sess, err := mm.Login(ctx, srv, password)
		if err != nil {
			return fmt.Errorf("%s: %w", srv.Name, err)
		}
		fallback, err := store.Set(auth.Key(srv.URL, srv.Username), sess.Token())
		if err != nil {
			return fmt.Errorf("%s: storing token: %w", srv.Name, err)
		}
		if fallback {
			fmt.Fprintf(os.Stderr, "warning: no Secret Service available, token saved to %s\n", store.FilePath)
		}
		fmt.Printf("%s: logged in as @%s\n", srv.Name, sess.Me.Username)
	}
	return nil
}

func notifyTest(cfg config.Notifications) error {
	if !cfg.Desktop {
		return errors.New("desktop notifications are disabled in the config")
	}
	n := notify.New(cfg)
	msg := notify.Message{Key: "test", Title: "termost", Body: "Test notification. If you can see this, desktop notifications work."}
	if err := n.Desktop(msg); err != nil {
		return fmt.Errorf("desktop notification failed: %w", err)
	}
	fmt.Println("sent a test notification")
	return nil
}

func runUI(ctx context.Context, store *auth.TokenStore, servers []config.Server, cfg *config.Config) error {
	closeLog, err := setupLog()
	if err != nil {
		return err
	}
	defer closeLog()

	var sessions []*mm.Session
	for _, srv := range servers {
		sess, err := resumeOrLogin(ctx, store, srv)
		if err != nil {
			return err
		}
		sessions = append(sessions, sess)
	}
	saveToken := func(srv config.Server, token string) error {
		_, err := store.Set(auth.Key(srv.URL, srv.Username), token)
		return err
	}
	return ui.Run(ctx, sessions, notify.New(cfg.Notifications), cfg.UI, saveToken)
}

func resumeOrLogin(ctx context.Context, store *auth.TokenStore, srv config.Server) (*mm.Session, error) {
	key := auth.Key(srv.URL, srv.Username)
	token, err := store.Get(key)
	if err != nil && !errors.Is(err, auth.ErrNoToken) {
		return nil, err
	}
	if err == nil {
		sess, err := mm.Resume(ctx, srv, token)
		if err == nil {
			return sess, nil
		}
		if !errors.Is(err, mm.ErrUnauthorized) {
			return nil, fmt.Errorf("%s: %w", srv.Name, err)
		}
		fmt.Fprintf(os.Stderr, "%s: session expired\n", srv.Name)
	}
	if err := login(ctx, store, []config.Server{srv}); err != nil {
		return nil, err
	}
	token, err = store.Get(key)
	if err != nil {
		return nil, err
	}
	return mm.Resume(ctx, srv, token)
}

func setupLog() (func(), error) {
	store, err := auth.NewTokenStore()
	if err != nil {
		return nil, err
	}
	path := filepath.Join(filepath.Dir(store.FilePath), "termost.log")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, err
	}
	slog.SetDefault(slog.New(slog.NewTextHandler(f, nil)))
	return func() { _ = f.Close() }, nil
}

func logout(ctx context.Context, store *auth.TokenStore, servers []config.Server) error {
	for _, srv := range servers {
		key := auth.Key(srv.URL, srv.Username)
		if sess, err := resume(ctx, store, srv); err == nil {
			_, _ = sess.Client.Logout(ctx)
		}
		if err := store.Delete(key); err != nil {
			return fmt.Errorf("%s: %w", srv.Name, err)
		}
		fmt.Printf("%s: logged out\n", srv.Name)
	}
	return nil
}

func resume(ctx context.Context, store *auth.TokenStore, srv config.Server) (*mm.Session, error) {
	token, err := store.Get(auth.Key(srv.URL, srv.Username))
	if errors.Is(err, auth.ErrNoToken) {
		return nil, fmt.Errorf("%s: not logged in, run `termost login %s`", srv.Name, srv.Name)
	}
	if err != nil {
		return nil, err
	}
	sess, err := mm.Resume(ctx, srv, token)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", srv.Name, err)
	}
	return sess, nil
}

func channels(ctx context.Context, store *auth.TokenStore, servers []config.Server) error {
	for _, srv := range servers {
		sess, err := resume(ctx, store, srv)
		if err != nil {
			return err
		}
		teams, err := sess.Teams(ctx)
		if err != nil {
			return fmt.Errorf("%s: %w", srv.Name, err)
		}
		fmt.Printf("%s (@%s)\n", srv.Name, sess.Me.Username)

		dms := map[string]*model.Channel{}
		dmMembers := map[string]model.ChannelMember{}
		for _, team := range teams {
			chans, members, err := sess.Channels(ctx, team.Id)
			if err != nil {
				return fmt.Errorf("%s/%s: %w", srv.Name, team.Name, err)
			}
			fmt.Printf("  %s\n", team.DisplayName)
			sort.Slice(chans, func(i, j int) bool { return chans[i].DisplayName < chans[j].DisplayName })
			for _, ch := range chans {
				if ch.IsGroupOrDirect() {
					dms[ch.Id] = ch
					dmMembers[ch.Id] = members[ch.Id]
					continue
				}
				prefix := "#"
				if ch.Type == model.ChannelTypePrivate {
					prefix = "🔒"
				}
				fmt.Printf("    %s %-30s %s\n", prefix, ch.DisplayName, unreadLabel(ch, members[ch.Id]))
			}
		}

		names, err := dmNames(ctx, sess, dms)
		if err != nil {
			return fmt.Errorf("%s: %w", srv.Name, err)
		}
		fmt.Println("  Direct messages")
		ids := make([]string, 0, len(dms))
		for id := range dms {
			ids = append(ids, id)
		}
		sort.Slice(ids, func(i, j int) bool { return dms[ids[i]].LastPostAt > dms[ids[j]].LastPostAt })
		for _, id := range ids {
			fmt.Printf("    @ %-30s %s\n", names[id], unreadLabel(dms[id], dmMembers[id]))
		}
		fmt.Println()
	}
	return nil
}

func dmNames(ctx context.Context, sess *mm.Session, dms map[string]*model.Channel) (map[string]string, error) {
	var userIDs []string
	for _, ch := range dms {
		if ch.Type == model.ChannelTypeDirect {
			userIDs = append(userIDs, ch.GetOtherUserIdForDM(sess.Me.Id))
		}
	}
	users, err := sess.Users(ctx, userIDs)
	if err != nil {
		return nil, err
	}
	names := make(map[string]string, len(dms))
	for id, ch := range dms {
		names[id] = ch.DisplayName
		if ch.Type != model.ChannelTypeDirect {
			continue
		}
		other := ch.GetOtherUserIdForDM(sess.Me.Id)
		switch {
		case other == "":
			names[id] = sess.Me.Username + " (you)"
		case users[other] != nil:
			names[id] = users[other].Username
		}
	}
	return names, nil
}

func unreadLabel(ch *model.Channel, m model.ChannelMember) string {
	var parts []string
	if unread := ch.TotalMsgCount - m.MsgCount; unread > 0 {
		parts = append(parts, fmt.Sprintf("%d unread", unread))
	}
	if m.MentionCount > 0 {
		parts = append(parts, fmt.Sprintf("%d mentions", m.MentionCount))
	}
	return strings.Join(parts, ", ")
}

func tail(ctx context.Context, store *auth.TokenStore, servers []config.Server) error {
	var sessions []*mm.Session
	for _, srv := range servers {
		sess, err := resume(ctx, store, srv)
		if err != nil {
			return err
		}
		sessions = append(sessions, sess)
	}

	events := make(chan mm.Event, 64)
	var wg sync.WaitGroup
	for _, sess := range sessions {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = sess.Listen(ctx, events, func(err error) {
				if err == nil {
					fmt.Fprintf(os.Stderr, "[%s] connected\n", sess.Server.Name)
					return
				}
				fmt.Fprintf(os.Stderr, "[%s] disconnected: %v, reconnecting\n", sess.Server.Name, err)
			})
		}()
	}
	go func() {
		wg.Wait()
		close(events)
	}()

	for ev := range events {
		if ev.EventType() == model.WebsocketEventPosted {
			printPosted(ev)
		}
	}
	return nil
}

func printPosted(ev mm.Event) {
	data := ev.GetData()
	raw, _ := data["post"].(string)
	var post model.Post
	if err := json.Unmarshal([]byte(raw), &post); err != nil {
		return
	}
	channel, _ := data["channel_display_name"].(string)
	sender, _ := data["sender_name"].(string)
	where := "~" + channel + " " + sender
	if model.ChannelType(fmt.Sprint(data["channel_type"])) == model.ChannelTypeDirect {
		where = "DM " + sender
	}
	if post.RootId != "" {
		where += " (thread)"
	}
	fmt.Printf("[%s] %s: %s\n", ev.Server, where, post.Message)
}

func readPassword(prompt string) (string, error) {
	fmt.Fprint(os.Stderr, prompt)
	fd := int(os.Stdin.Fd())
	if term.IsTerminal(fd) {
		b, err := term.ReadPassword(fd)
		fmt.Fprintln(os.Stderr)
		return string(b), err
	}
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	return strings.TrimRight(line, "\r\n"), err
}
