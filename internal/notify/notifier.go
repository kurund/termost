package notify

import (
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/godbus/dbus/v5"

	"github.com/kurund/termost/internal/config"
	"github.com/kurund/termost/internal/termsafe"
)

const (
	coalesceWindow = 2 * time.Minute
	maxBodyRunes   = 300
	dbusDest       = "org.freedesktop.Notifications"
	dbusPath       = "/org/freedesktop/Notifications"
)

type Message struct {
	Key   string
	Title string
	Body  string
}

type shown struct {
	id    uint32
	count int
	at    time.Time
}

type Notifier struct {
	cfg config.Notifications

	mu         sync.Mutex
	conn       *dbus.Conn
	markup     bool
	dbusFailed bool
	shown      map[string]*shown
}

func New(cfg config.Notifications) *Notifier {
	return &Notifier{cfg: cfg, shown: map[string]*shown{}}
}

// Desktop shows msg through the freedesktop notification service, replacing
// a recent notification for the same key with a running count.
func (n *Notifier) Desktop(msg Message) error {
	if !n.cfg.Desktop {
		return nil
	}
	n.mu.Lock()
	defer n.mu.Unlock()

	conn, err := n.bus()
	if err != nil {
		return err
	}
	replaces, summary := n.prepare(msg, time.Now())
	body := Body(msg.Body)
	if n.markup {
		body = escapeMarkup(body)
	}
	hints := map[string]dbus.Variant{
		"urgency":  dbus.MakeVariant(byte(1)),
		"category": dbus.MakeVariant("im.received"),
	}
	var id uint32
	err = conn.Object(dbusDest, dbusPath).Call(dbusDest+".Notify", 0,
		"termost", replaces, "", summary, body, []string{}, hints, int32(-1)).Store(&id)
	if err != nil {
		slog.Warn("desktop notification failed", "err", err)
		n.dbusFailed = true
		return err
	}
	n.shown[msg.Key].id = id
	return nil
}

func (n *Notifier) Dismiss(key string) {
	n.mu.Lock()
	defer n.mu.Unlock()

	s, ok := n.shown[key]
	if !ok {
		return
	}
	delete(n.shown, key)
	if s.id == 0 || n.conn == nil {
		return
	}
	if err := n.conn.Object(dbusDest, dbusPath).Call(dbusDest+".CloseNotification", 0, s.id).Err; err != nil {
		slog.Debug("closing notification failed", "err", err)
	}
}

// Terminal returns the escape sequence to write for msg, or "" for none.
// The bell is used as a fallback when desktop notifications are unavailable.
func (n *Notifier) Terminal(msg Message) string {
	n.mu.Lock()
	failed := n.dbusFailed
	n.mu.Unlock()

	var seq string
	if n.cfg.OSC {
		seq += "\x1b]777;notify;" + oscSafe(msg.Title) + ";" + oscSafe(Body(msg.Body)) + "\x1b\\"
	}
	if n.cfg.Bell || (n.cfg.Desktop && failed && !n.cfg.OSC) {
		seq += "\a"
	}
	return seq
}

func (n *Notifier) bus() (*dbus.Conn, error) {
	if n.conn != nil {
		return n.conn, nil
	}
	if n.dbusFailed {
		return nil, fmt.Errorf("dbus unavailable")
	}
	conn, err := dbus.SessionBus()
	if err != nil {
		slog.Warn("no D-Bus session bus, desktop notifications disabled", "err", err)
		n.dbusFailed = true
		return nil, err
	}
	n.conn = conn
	var caps []string
	if err := conn.Object(dbusDest, dbusPath).Call(dbusDest+".GetCapabilities", 0).Store(&caps); err != nil {
		slog.Debug("reading notification capabilities failed", "err", err)
	}
	n.markup = slices.Contains(caps, "body-markup")
	return conn, nil
}

var markupEscaper = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;")

// escapeMarkup keeps notification daemons that render body markup from
// interpreting tags or images in message text.
func escapeMarkup(s string) string {
	return markupEscaper.Replace(s)
}

func (n *Notifier) prepare(msg Message, now time.Time) (replaces uint32, summary string) {
	s, ok := n.shown[msg.Key]
	if !ok || now.Sub(s.at) > coalesceWindow {
		n.shown[msg.Key] = &shown{count: 1, at: now}
		return 0, msg.Title
	}
	s.count++
	s.at = now
	return s.id, fmt.Sprintf("%s (%d new)", msg.Title, s.count)
}

func Body(text string) string {
	text = strings.Join(strings.Fields(text), " ")
	if r := []rune(text); len(r) > maxBodyRunes {
		text = string(r[:maxBodyRunes-1]) + "…"
	}
	return text
}

func oscSafe(s string) string {
	return strings.ReplaceAll(termsafe.Line(s), ";", ",")
}
