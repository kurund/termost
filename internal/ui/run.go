package ui

import (
	"context"

	tea "charm.land/bubbletea/v2"

	"github.com/kurund/termost/internal/config"
	"github.com/kurund/termost/internal/mm"
	"github.com/kurund/termost/internal/notify"
)

func Run(ctx context.Context, sessions []*mm.Session, notifier *notify.Notifier, cfg config.UI, saveToken func(config.Server, string) error) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	p := tea.NewProgram(New(ctx, sessions, notifier, cfg, saveToken), tea.WithContext(ctx))

	events := make(chan mm.Event, 64)
	for _, sess := range sessions {
		go func() {
			_ = sess.Listen(ctx, events, func(err error) {
				p.Send(connMsg{server: sess.Server.Name, err: err})
			})
		}()
	}
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case ev := <-events:
				p.Send(wsMsg{ev})
			}
		}
	}()

	_, err := p.Run()
	return err
}
