package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func waitFor(t *testing.T, f func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for !f() {
		if time.Now().After(deadline) {
			t.Fatal("condition timed out")
		}
		time.Sleep(time.Millisecond)
	}
}
func mockNotifier(s *pushState, sends ...sendFunc) *notifier {
	n := &notifier{state: s, timeout: 5 * time.Millisecond, retryDelay: time.Millisecond}
	for i, f := range sends {
		n.targets = append(n.targets, &target{queue: make(chan notification, 64), send: f, index: i + 1})
	}
	return n
}
func startTestNotifier(t *testing.T, n *notifier) context.CancelFunc {
	ctx, cancel := context.WithCancel(context.Background())
	n.start(ctx)
	t.Cleanup(func() { cancel(); n.wg.Wait() })
	return cancel
}

func TestRetryIsolationAndRedaction(t *testing.T) {
	var output bytes.Buffer
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&output, nil)))
	defer slog.SetDefault(old)
	var success, fail atomic.Int32
	n := mockNotifier(&pushState{enabled: true}, func(notification) error { success.Add(1); return nil }, func(notification) error { fail.Add(1); return errors.New("https://SECRET_TOKEN/PRIVATE_BODY") })
	cancel := startTestNotifier(t, n)
	n.enqueue("title", "body")
	waitFor(t, func() bool { return success.Load() == 1 && fail.Load() == 3 })
	cancel()
	n.wg.Wait()
	if strings.Contains(output.String(), "SECRET") || strings.Contains(output.String(), "PRIVATE") {
		t.Fatal("secret leaked")
	}
	if success.Load() != 1 {
		t.Fatal("successful target retried")
	}
}

func TestTimeoutNeverOverlaps(t *testing.T) {
	blocked := make(chan struct{})
	var calls, healthy atomic.Int32
	n := mockNotifier(&pushState{enabled: true}, func(notification) error { calls.Add(1); <-blocked; return nil }, func(notification) error { healthy.Add(1); return nil })
	cancel := startTestNotifier(t, n)
	defer close(blocked)
	n.enqueue("first", "body")
	waitFor(t, func() bool { return calls.Load() == 1 && healthy.Load() == 1 })
	time.Sleep(20 * time.Millisecond)
	for i := 0; i < 200; i++ {
		n.enqueue("queued", "body")
	}
	time.Sleep(20 * time.Millisecond)
	if calls.Load() != 1 {
		t.Fatal("overlapping calls after timeout")
	}
	if healthy.Load() < 2 {
		t.Fatal("healthy target blocked")
	}
	cancel()
	n.wg.Wait()
}

func TestQueueAndDisable(t *testing.T) {
	s := &pushState{enabled: true, path: t.TempDir() + "/state.json"}
	var count atomic.Int32
	n := mockNotifier(s, func(notification) error { count.Add(1); return nil })
	for i := 0; i < 100; i++ {
		n.enqueue("title", "body")
	}
	if len(n.targets[0].queue) != 64 || n.targets[0].dropped.Load() != 36 {
		t.Fatal("queue is not bounded")
	}
	if err := s.set(false); err != nil {
		t.Fatal(err)
	}
	n.clear()
	n.enqueue("off", "body")
	if len(n.targets[0].queue) != 0 {
		t.Fatal("off queued messages")
	}
	// A stale task remains invalid even after re-enabling.
	if err := s.set(true); err != nil {
		t.Fatal(err)
	}
	n.targets[0].queue <- notification{Generation: 0}
	startTestNotifier(t, n)
	n.enqueue("new", "body")
	waitFor(t, func() bool { return count.Load() == 1 })
}

func TestShoutrrrBarkLocal(t *testing.T) {
	received := make(chan map[string]any, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var p map[string]any
		if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
			t.Error(err)
		}
		received <- p
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"code":200,"message":"success"}`)
	}))
	defer server.Close()
	n, err := newNotifier([]string{"bark://:TEST_KEY@" + strings.TrimPrefix(server.URL, "http://") + "/?scheme=http&group=Telegram&sound=healthnotification"}, &pushState{enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	startTestNotifier(t, n)
	n.enqueue("Telegram｜私聊", "Alice: 点击查看")
	select {
	case p := <-received:
		if p["title"] != "Telegram｜私聊" || p["body"] != "Alice: 点击查看" || p["url"] != "tg://" || p["group"] != "Telegram" {
			t.Fatal(p)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("no Bark request")
	}
}

func TestServiceCoverage(t *testing.T) {
	expected := []string{"bark", "discord", "generic", "googlechat", "gotify", "hangouts", "ifttt", "join", "logger", "matrix", "mattermost", "ntfy", "opsgenie", "pushbullet", "pushover", "rocketchat", "slack", "smtp", "teams", "telegram", "zulip"}
	actual := services()
	if strings.Join(actual, ",") != strings.Join(expected, ",") {
		t.Fatalf("services: %v", actual)
	}
	if _, err := newNotifier([]string{"invalid://SECRET"}, &pushState{}); err == nil || strings.Contains(err.Error(), "SECRET") {
		t.Fatal("invalid target error not sanitized")
	}
}
