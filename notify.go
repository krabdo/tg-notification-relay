package main

import (
	"context"
	"fmt"
	"log"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/containrrr/shoutrrr/pkg/router"
	"github.com/containrrr/shoutrrr/pkg/services/bark"
	"github.com/containrrr/shoutrrr/pkg/types"
)

type notification struct {
	Title, Body string
	Generation  uint64
}
type sendFunc func(notification) error
type target struct {
	queue   chan notification
	send    sendFunc
	index   int
	dropped atomic.Uint64
}
type notifier struct {
	targets             []*target
	state               *pushState
	timeout, retryDelay time.Duration
	wg                  sync.WaitGroup
	botIDs              map[int64]bool
}

func services() []string {
	r, _ := router.New(nil)
	names := r.ListServices()
	sort.Strings(names)
	return names
}

func newNotifier(urls []string, state *pushState) (*notifier, error) {
	// Some Shoutrrr services use the default HTTP client. Configure it before workers start.
	http.DefaultClient.Timeout = 10 * time.Second
	n := &notifier{state: state, timeout: 10 * time.Second, retryDelay: 2 * time.Second, botIDs: make(map[int64]bool)}
	r, _ := router.New(nil)
	transport := newBarkTransport(http.DefaultTransport)
	for i, raw := range urls {
		service, err := r.Locate(raw)
		if err != nil {
			return nil, fmt.Errorf("通知目标 %d 配置无效（运行 services 查看支持的渠道）", i+1)
		}
		parsed, _ := url.Parse(raw)
		scheme := strings.ToLower(parsed.Scheme)
		if scheme == "telegram" && parsed.User != nil {
			if id, err := strconv.ParseInt(parsed.User.Username(), 10, 64); err == nil {
				n.botIDs[id] = true
			}
		}
		if scheme == "logger" {
			service.SetLogger(log.New(os.Stdout, "", log.LstdFlags))
		}
		if scheme == "bark" {
			cfg := &bark.Config{Scheme: "https"}
			if err := cfg.SetURL(parsed); err != nil {
				return nil, fmt.Errorf("通知目标 %d 配置无效", i+1)
			}
			transport.destinations[barkDestination{cfg.GetAPIURL("push"), cfg.DeviceKey}] = true
		}
		t := &target{queue: make(chan notification, 64), index: i + 1}
		t.send = func(msg notification) error {
			params := types.Params{}
			body := msg.Title + "\n" + msg.Body + "\ntg://"
			if scheme == "bark" {
				params["title"] = msg.Title
				params["url"] = "tg://"
				body = msg.Body
			}
			// Generic text works across every service without unsupported dynamic properties.
			return service.Send(body, &params)
		}
		n.targets = append(n.targets, t)
	}
	http.DefaultClient.Transport = transport
	return n, nil
}

func (n *notifier) start(ctx context.Context) {
	for _, t := range n.targets {
		n.wg.Add(1)
		go func() { defer n.wg.Done(); n.work(ctx, t) }()
	}
}
func (n *notifier) enqueue(title, body string) {
	on, generation := n.state.snapshot()
	if !on {
		return
	}
	for _, t := range n.targets {
		msg := notification{title, body, generation}
		select {
		case t.queue <- msg:
		default:
			select {
			case <-t.queue:
				t.dropped.Add(1)
			default:
			}
			select {
			case t.queue <- msg:
			default:
				t.dropped.Add(1)
			}
			count := t.dropped.Load()
			if count == 1 || count%100 == 0 {
				slog.Warn("通知队列已满，丢弃旧通知", "target", t.index, "dropped", count)
			}
		}
	}
}
func (n *notifier) clear() {
	for _, t := range n.targets {
		for {
			select {
			case <-t.queue:
			default:
				goto next
			}
		}
	next:
	}
}

func (n *notifier) work(ctx context.Context, t *target) {
	for {
		select {
		case <-ctx.Done():
			return
		case msg := <-t.queue:
			for attempt := 1; attempt <= 3; attempt++ {
				if ctx.Err() != nil || !n.state.permits(msg.Generation) {
					break
				}
				// Buffered completion plus one in-flight call per target: timeout cannot leak
				// a completion sender or cause overlapping retries of an uncancellable service.
				done := make(chan error, 1)
				go func() { done <- t.send(msg) }()
				timer := time.NewTimer(n.timeout)
				var err error
				select {
				case <-ctx.Done():
					timer.Stop()
					return
				case err = <-done:
					timer.Stop()
				case <-timer.C:
					slog.Warn("通知发送超时，等待该目标当前请求退出", "target", t.index)
					select {
					case <-ctx.Done():
						return
					case err = <-done:
					}
				}
				if err == nil {
					break
				}
				// Do not log service errors: upstream errors may contain tokens or response bodies.
				slog.Warn("通知发送失败", "target", t.index, "attempt", attempt)
				if attempt < 3 {
					select {
					case <-ctx.Done():
						return
					case <-time.After(n.retryDelay):
					}
				}
			}
		}
	}
}
