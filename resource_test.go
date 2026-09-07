package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gotd/td/tg"
)

func resourceSample(t *testing.T, label string) uint64 {
	t.Helper()
	runtime.GC()
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	rss, peak := "unavailable (non-Linux)", "unavailable (non-Linux)"
	if b, err := os.ReadFile("/proc/self/status"); err == nil {
		for _, line := range strings.Split(string(b), "\n") {
			if strings.HasPrefix(line, "VmRSS:") {
				rss = strings.TrimSpace(strings.TrimPrefix(line, "VmRSS:"))
			}
			if strings.HasPrefix(line, "VmHWM:") {
				peak = strings.TrimSpace(strings.TrimPrefix(line, "VmHWM:"))
			}
		}
	}
	t.Logf("%s: heap_live=%d KiB RSS=%s peak_RSS=%s goroutines=%d", label, m.HeapAlloc/1024, rss, peak, runtime.NumGoroutine())
	return m.HeapAlloc
}

// This exercises the real relay with synthetic updates and fake senders. It does
// not measure a live Telegram connection, TLS sockets, or third-party SDK state.
func TestResourceProfile(t *testing.T) {
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(io.Discard, nil)))
	defer slog.SetDefault(old)
	r, _ := testRelay(t)
	var sent atomic.Int64
	n := mockNotifier(r.state, func(notification) error { sent.Add(1); return nil })
	r.notify = n
	ctx, cancel := context.WithCancel(context.Background())
	n.start(ctx)
	defer func() { cancel(); n.wg.Wait() }()
	resourceSample(t, "idle")
	feed := func(start, count int) {
		for i := start; i < start+count; i++ {
			r.Handle(ctx, &tg.Updates{Users: []tg.UserClass{&tg.User{ID: int64(i + 100), FirstName: fmt.Sprint(i)}}, Updates: []tg.UpdateClass{&tg.UpdateNewMessage{Message: &tg.Message{ID: i, PeerID: &tg.PeerUser{UserID: int64(i + 100)}, FromID: &tg.PeerUser{UserID: int64(i + 100)}, Message: "synthetic hidden text"}}}})
		}
	}
	feed(1, 50000)
	first := resourceSample(t, "after 50000 messages")
	feed(50001, 50000)
	second := resourceSample(t, "after 100000 messages")
	if second > first+8*1024*1024 {
		t.Fatal("live heap continued to grow")
	}
	if len(r.users.items) > 1024 || len(r.seen.items) > 4096 {
		t.Fatal("cache exceeded bounds")
	}
	blocked := make(chan struct{})
	var calls atomic.Int32
	fault := mockNotifier(r.state, func(notification) error { calls.Add(1); <-blocked; return nil })
	fault.start(ctx)
	defer func() { close(blocked); cancel(); fault.wg.Wait() }()
	fault.enqueue("first", "body")
	waitFor(t, func() bool { return calls.Load() == 1 })
	time.Sleep(10 * time.Millisecond)
	baseline := runtime.NumGoroutine()
	for i := 0; i < 100000; i++ {
		fault.enqueue("fault", "body")
	}
	resourceSample(t, "blocked channel after 100000 enqueues")
	if calls.Load() != 1 || runtime.NumGoroutine() > baseline+2 {
		t.Fatal("unbounded in-flight sends")
	}
	t.Logf("healthy delivered=%d dropped=%d fault_dropped=%d", sent.Load(), n.targets[0].dropped.Load(), fault.targets[0].dropped.Load())
}
