package main

import (
	"context"
	"log/slog"
	"strings"
	"sync/atomic"

	"github.com/gotd/td/tg"
)

// gotd starts a goroutine for each inbound packet. Return immediately from its
// callback so slow Telegram lookups cannot accumulate blocked packet handlers.
type ingress struct {
	normal, controls chan tg.UpdatesClass
	dropped          atomic.Uint64
}

func newIngress() *ingress {
	return &ingress{normal: make(chan tg.UpdatesClass, 64), controls: make(chan tg.UpdatesClass, 16)}
}
func (i *ingress) Handle(_ context.Context, u tg.UpdatesClass) error {
	queue := i.normal
	if containsControl(u) {
		queue = i.controls
	}
	select {
	case queue <- u:
	default:
		select {
		case <-queue:
			i.dropped.Add(1)
		default:
		}
		select {
		case queue <- u:
		default:
			i.dropped.Add(1)
		}
		count := i.dropped.Load()
		if count == 1 || count%100 == 0 {
			slog.Warn("Telegram 更新队列已满，丢弃旧更新", "dropped", count)
		}
	}
	return nil
}
func (i *ingress) run(ctx context.Context, r *relay) {
	for {
		if ctx.Err() != nil {
			return
		}
		select {
		case u := <-i.controls:
			r.Handle(ctx, u)
			continue
		default:
		}
		select {
		case <-ctx.Done():
			return
		case u := <-i.controls:
			r.Handle(ctx, u)
		case u := <-i.normal:
			r.Handle(ctx, u)
		}
	}
}
func containsControl(u tg.UpdatesClass) bool {
	isCommand := func(out bool, text string) bool {
		if !out {
			return false
		}
		switch strings.ToLower(strings.TrimSpace(text)) {
		case "/on", "/off", "/status", "/help":
			return true
		}
		return false
	}
	check := func(update tg.UpdateClass) bool {
		v, ok := update.(*tg.UpdateNewMessage)
		if !ok {
			return false
		}
		m, ok := v.Message.(*tg.Message)
		return ok && isCommand(m.Out, m.Message)
	}
	switch v := u.(type) {
	case *tg.Updates:
		for _, x := range v.Updates {
			if check(x) {
				return true
			}
		}
	case *tg.UpdatesCombined:
		for _, x := range v.Updates {
			if check(x) {
				return true
			}
		}
	case *tg.UpdateShort:
		return check(v.Update)
	case *tg.UpdateShortMessage:
		return isCommand(v.Out, v.Message)
	}
	return false
}
