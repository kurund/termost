# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

termost is a Linux-only terminal Mattermost client in Go (Bubble Tea v2 under `charm.land/...`, the official SDK `github.com/mattermost/mattermost/server/public/model`). Password login only. User-facing docs are in `README.md`;

## Commands

Go is not installed globally (NixOS); run everything through the flake's dev shell. Inside a git repo the flake only sees tracked files, so use `nix develop path:.` while `flake.nix` or new files are untracked (don't stage files just to make Nix see them):

```sh
nix develop -c go build -o bin/termost ./cmd/termost
nix develop -c go test ./...
nix develop -c go test ./internal/store -run TestCRTCounts   # single test
nix develop -c golangci-lint run ./...
nix develop -c gofmt -l .
nix develop .#demo -c demo/record.sh                         # re-record demo/demo.gif (needs termost-mm)
nix build .                                                  # the flake package
```

After changing `go.mod`/`go.sum`, update `vendorHash` in `flake.nix`: `nix build .` fails and prints the new hash.

Verify SDK, Bubble Tea and lipgloss APIs by grepping the module cache (`$(go env GOMODCACHE)/github.com/mattermost/mattermost/server/public@v0.5.0/model`, `charm.land/bubbletea/v2@v2.1.0`, ...) rather than from memory: Bubble Tea v2 differs from v1 (`View() tea.View`, `tea.KeyPressMsg`, `v.AltScreen`, key strings like `alt+shift+j` / `alt+J`).

## Architecture

```
cmd/termost  -> config, auth (token store), mm.Session per server -> ui.Run
mm.Session   REST (Client4) + websocket goroutine with backoff; Load() builds a store.Server
store.Server per-server state and all unread/visibility rules (pure, unit-tested)
ui.Model     Bubble Tea model; owns map[server]*store.Server, never shares it across goroutines
```

- **Data flow**: websocket events and REST results become `tea.Msg`s (`p.Send` in `ui/run.go`, or values returned by `tea.Cmd` closures). Only `Update` mutates the model and stores. Never do network I/O in `Update`; return a `tea.Cmd`.
- **Reconnect = full reload**: on a websocket reconnect `loadServer` replaces that server's `store.Server`, which drops cached posts; the open channel is refetched. There is no event replay.
- **Session expiry**: `Session.check()` flags any 401; `Model.Update` calls `checkExpired()` after every message and opens the in-app password prompt (`ui/relogin.go`).
- **Key routing** in `handleKey` is ordered: quit, relogin prompt, `Ctrl+K` switcher, emoji picker, search (passes `Ctrl+K`/`Ctrl+L`/`Alt+…` on), `@`/`~` completion, global keys (`Tab`, `Alt+…`, `PgUp`), Threads list, then by focus (`focusInput`, `focusMessages`, `focusSidebar`). Overlays (relogin, search, threads list, picker, switcher) replace the message viewport in `View`; a thread opened from search or the threads list returns to it on `Esc`.
- **Search** (`ui/search.go`) queries every team of the active server in parallel and merges by post ID, since DMs and group messages come back from each team and older servers have no cross-team search endpoint.
- **Cursor**: every text input uses the terminal's real cursor (`SetVirtualCursor(false)`); `View` places it via `terminalCursor`, so a new input must be added there. A virtual blinking cursor would redraw the whole screen twice a second while idle.
- **Mouse**: `View` rebuilds `m.zones` (screen rectangles with click/wheel handlers, `ui/mouse.go`) on every render, so hit-testing always matches what is drawn. Overlays that scroll record what they drew (`switcher.start`, `picker.start`, `threadsList.lineItem`); a new clickable area needs a zone registered in `sidebarZone`/`mainZones`.
- **Message pane**: `refreshMessages` re-renders the whole visible list (channel posts, or the open thread) via `renderPosts`, which returns line spans per post for selection scrolling. It keeps the view pinned to the bottom if it was there. `Update` re-runs `layout()` whenever main width or viewport height changes (sidebar visibility, completion popup, resize).
- **Sidebar rows** (`ui/sidebar.go`) are rebuilt from the store on every render: server rows for all servers, then only the active server's teams (no team row when it has just one), `💬 Threads` (CRT only), categories and DMs. The server rows and divider stay pinned while the rest scrolls (`sidebarWindow`); clicks map screen lines to rows through `sidebarDrawn`.
- **Untrusted text**: anything from the server (messages, names, error text) can carry terminal escape sequences. `markdown.Render` and the CLI commands pass it through `termsafe.Strip`/`termsafe.Line`; do the same for any new output that bypasses them, especially plain stdout and `tea.Raw`.
- **Markdown** (`internal/markdown`) is a goldmark AST renderer that wraps text itself and styles each wrapped piece separately (including OSC 8 hyperlinks) so no ANSI state leaks into the next line or the sidebar. Output is cached by width + username + source.

## Mattermost behaviour the code relies on

- **Collapsed Reply Threads** (`store.Server.CRT`, from client config `CollapsedThreads` + the `display_settings/collapsed_reply_threads` preference): when on, replies are not added to channel post lists, posts are fetched with `collapsedThreads=true`, and unread/mention counts use the `*Root` counters. `CollapsedThreads` is only present in the client config for authenticated requests, and `Session.ClientConfig` must send `format=old`: older servers reject the SDK's `GetClientConfig`, while the local preview server accepts both, so it hides this.
- **DM visibility**: `direct_channel_show` / `group_channel_show` preferences and deactivated users hide DMs; unread or open DMs always show; DMs placed in a team category (e.g. Favorites) are removed from the DM section.
- **Websocket quirks**: `sidebar_category_*` events carry the team in the broadcast (`ev.GetBroadcast().TeamId`), not in `data`; `reaction_*` carry the channel in the broadcast; `status_change` is only sent for your own user, so DM presence is polled every minute; `posted` includes `metadata.files`.
- **Partial user records**: some endpoints (thread participants without `Extended`) return users with only an ID; `store.AddUsers` ignores records without a username so they never overwrite known users.

## Testing against a real server

Unit tests cover `store`, `notify`, `markdown`, `emoji`, `clip`, `config`, `auth` and UI pure functions. Check UI changes end to end against the local server:

```sh
docker start termost-mm   # mattermost/mattermost-preview on :8065; users kurund / alice / bob, password Passw0rd!x
```

- Drive the TUI in a detached tmux session on its own server (`tmux -L termost-test -f /dev/null …`, so options like `extended-keys` never touch the user's tmux) and read it with `capture-pane -p` (`-e` for colours). Tab characters in captures come from Bubble Tea's cursor movement, not the app. `Shift+Enter` only reaches the app with `extended-keys always` and `extended-keys-format csi-u` set on that server.
- Isolate every run from the real desktop: point `XDG_CONFIG_HOME`, `XDG_STATE_HOME`, `XDG_CACHE_HOME`, `XDG_RUNTIME_DIR` at a scratch directory and set `DBUS_SESSION_BUS_ADDRESS` to a dead socket, or tokens go into the real keyring.
- Pass environment through `env` inside the tmux command (`tmux new-session "env PATH=… XDG_…=… bin/termost"`). `tmux -e PATH=…` is overridden by the login shell, so stubbed tools like a fake `xdg-open` get bypassed.
- The preview image forces CRT `always_on`, and its `mmctl --local` mode is disabled; seed data through the REST API (the first account created is the system admin).
