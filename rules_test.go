package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRules(t *testing.T) {
	cases := []struct {
		name string
		m    message
		self bool
		want string
	}{
		{"private", message{Private: true}, false, "私聊"},
		{"out", message{Private: true, Out: true}, false, ""},
		{"self enabled", message{Private: true, Out: true}, true, "私聊"},
		{"mention flag", message{Mentioned: true}, false, "被@"},
		{"username", message{Text: "Hello @ALICE!"}, false, "用户名@"},
		{"reply", message{ReplyToMe: true}, false, "回复你"},
		{"unrelated", message{Text: "ordinary"}, false, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := reason(tc.m, "alice", tc.self); got != tc.want {
				t.Fatalf("got %q want %q", got, tc.want)
			}
		})
	}
	for _, s := range []string{"x@alice", "@alice_more", "中@alice", "@alice中", "@alice2"} {
		if usernameMention(s, "alice") {
			t.Errorf("unexpected match %q", s)
		}
	}
	for _, s := range []string{"@alice", "(@Alice)", "你好 @alice！"} {
		if !usernameMention(s, "alice") {
			t.Errorf("missing match %q", s)
		}
	}
	if usernameMention("@any", "") {
		t.Fatal("empty username matched")
	}
	m := message{Text: "SECRET BODY", SenderName: "Alice", ChatName: "Group"}
	if got := notificationBody(m); got != "Group\nAlice: 点击查看" || strings.Contains(got, m.Text) {
		t.Fatal(got)
	}
}

func TestSavedCommandAuthority(t *testing.T) {
	m := message{Private: true, Out: true, ChatID: 7, Text: " /OFF "}
	if savedCommand(m, 7) != "/off" {
		t.Fatal("command missing")
	}
	for _, v := range []message{{Private: true, ChatID: 7, Text: "/off"}, {Private: true, Out: true, ChatID: 8, Text: "/off"}, {Out: true, ChatID: 7, Text: "/off"}, {Private: true, Out: true, ChatID: 7, Text: "/off extra"}} {
		if savedCommand(v, 7) != "" {
			t.Fatal("unauthorized command")
		}
	}
}

func TestStatePersistence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	s, err := openState(path)
	if err != nil {
		t.Fatal(err)
	}
	_, g := s.snapshot()
	if err := s.set(false); err != nil {
		t.Fatal(err)
	}
	restored, err := openState(path)
	if err != nil {
		t.Fatal(err)
	}
	if on, _ := restored.snapshot(); on {
		t.Fatal("off not persisted")
	}
	if err := s.set(true); err != nil {
		t.Fatal(err)
	}
	if s.permits(g) {
		t.Fatal("stale generation accepted")
	}
	if err := os.WriteFile(path, []byte("broken"), 0600); err != nil {
		t.Fatal(err)
	}
	s, err = openState(path)
	if err == nil {
		t.Fatal("corruption not reported")
	}
	if on, _ := s.snapshot(); !on {
		t.Fatal("expected enabled fallback")
	}
	s.path = filepath.Join(path, "impossible.json")
	if s.set(false) == nil {
		t.Fatal("expected write failure")
	}
	if on, _ := s.snapshot(); !on {
		t.Fatal("changed state on failed save")
	}
}

func TestConfig(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("TG_API_ID", "42")
	t.Setenv("TG_API_HASH", "hash")
	t.Setenv("PUSH_SELF_MESSAGES", "false")
	t.Setenv("SHOUTRRR_URLS", `["generic://localhost/a,b?token=abc"]`)
	if err := os.WriteFile(".env", []byte("TG_API_ID=99\n"), 0600); err != nil {
		t.Fatal(err)
	}
	c, err := loadConfig(true)
	if err != nil {
		t.Fatal(err)
	}
	if c.APIID != 42 || len(c.URLs) != 1 {
		t.Fatal(c.APIID, len(c.URLs))
	}
	for _, raw := range []string{"", "null", "[]", "bad-secret-url", `[""]`} {
		t.Setenv("SHOUTRRR_URLS", raw)
		if _, err := loadConfig(true); err == nil {
			t.Fatalf("accepted %q", raw)
		}
	}
	t.Setenv("SHOUTRRR_URLS", "[]")
	if _, err := loadConfig(false); err != nil {
		t.Fatal("login required notifications")
	}
}

func TestBoundedCache(t *testing.T) {
	c := newCache[int, int](2)
	c.put(1, 1)
	c.put(2, 2)
	c.get(1)
	c.put(3, 3)
	if _, ok := c.get(2); ok {
		t.Fatal("LRU not evicted")
	}
	for i := 0; i < 10000; i++ {
		c.put(i, i)
	}
	if len(c.items) != 2 || c.order.Len() != 2 {
		t.Fatal("cache grew")
	}
}
