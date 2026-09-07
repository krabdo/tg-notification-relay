package main

import (
	"context"
	"errors"
	"testing"

	"github.com/gotd/td/tg"
)

type fakeTelegram struct {
	reply   tg.MessagesMessagesClass
	err     error
	sent    []string
	queried int
}

func (f *fakeTelegram) MessagesGetMessages(context.Context, []tg.InputMessageClass) (tg.MessagesMessagesClass, error) {
	f.queried++
	return f.reply, f.err
}
func (f *fakeTelegram) ChannelsGetMessages(context.Context, *tg.ChannelsGetMessagesRequest) (tg.MessagesMessagesClass, error) {
	f.queried++
	return f.reply, f.err
}
func (f *fakeTelegram) MessagesSendMessage(_ context.Context, r *tg.MessagesSendMessageRequest) (tg.UpdatesClass, error) {
	f.sent = append(f.sent, r.Message)
	return &tg.Updates{}, nil
}
func (f *fakeTelegram) UpdatesGetState(context.Context) (*tg.UpdatesState, error) {
	return &tg.UpdatesState{}, nil
}
func testRelay(t *testing.T) (*relay, *fakeTelegram) {
	s := &pushState{enabled: true, path: t.TempDir() + "/state.json"}
	n := mockNotifier(s, func(notification) error { return nil })
	r := newRelay(config{Username: "alice"}, s, n)
	f := &fakeTelegram{reply: &tg.MessagesMessages{}}
	r.api = f
	r.me = 7
	r.ready = true
	r.users.put(7, userInfo{name: "Me"})
	r.users.put(8, userInfo{name: "Alice"})
	r.chats.put(peerKey{'g', 9}, userInfo{name: "Group"})
	return r, f
}
func TestTelegramShortUpdatesAndDedup(t *testing.T) {
	r, _ := testRelay(t)
	ctx := context.Background()
	u := &tg.UpdateShortMessage{ID: 1, UserID: 8, Message: "PRIVATE_SECRET"}
	r.Handle(ctx, u)
	r.Handle(ctx, u)
	if len(r.notify.targets[0].queue) != 1 {
		t.Fatal("duplicate notification")
	}
	got := <-r.notify.targets[0].queue
	if got.Body != "Alice: PRIVATE_SECRET" {
		t.Fatal(got)
	}
	r.Handle(ctx, &tg.UpdateShortChatMessage{ID: 2, ChatID: 9, FromID: 8, Message: "hi @ALICE!"})
	got = <-r.notify.targets[0].queue
	if got.Title != "Telegram｜用户名@" || got.Body != "Group\nAlice: hi @ALICE!" {
		t.Fatal(got)
	}
}
func TestTelegramCommandsWhileDisabled(t *testing.T) {
	r, f := testRelay(t)
	ctx := context.Background()
	r.Handle(ctx, &tg.UpdateShortMessage{ID: 1, UserID: 7, Out: true, Message: "/off"})
	r.Handle(ctx, &tg.UpdateShortMessage{ID: 2, UserID: 8, Message: "hello"})
	r.Handle(ctx, &tg.UpdateShortMessage{ID: 3, UserID: 7, Out: true, Message: "/status"})
	r.Handle(ctx, &tg.UpdateShortMessage{ID: 4, UserID: 7, Out: true, Message: "/on"})
	r.Handle(ctx, &tg.UpdateShortMessage{ID: 5, UserID: 8, Message: "hello"})
	if len(f.sent) != 3 || len(r.notify.targets[0].queue) != 1 {
		t.Fatal(len(f.sent), len(r.notify.targets[0].queue))
	}
	if on, _ := r.state.snapshot(); !on {
		t.Fatal("command did not enable")
	}
}
func TestTelegramReplyFailureAndSuccess(t *testing.T) {
	for _, fail := range []bool{false, true} {
		r, f := testRelay(t)
		f.reply = &tg.MessagesMessages{Messages: []tg.MessageClass{&tg.Message{ID: 42, FromID: &tg.PeerUser{UserID: 7}}}}
		if fail {
			f.err = errors.New("private upstream error")
		}
		r.Handle(context.Background(), &tg.UpdateShortChatMessage{ID: 10, FromID: 8, ChatID: 9, Message: "reply", ReplyTo: &tg.MessageReplyHeader{ReplyToMsgID: 42}})
		want := 1
		if fail {
			want = 0
		}
		if len(r.notify.targets[0].queue) != want || f.queried != 1 {
			t.Fatal("reply handling", fail)
		}
	}
}
func TestChannelUpdatesAndNamespace(t *testing.T) {
	r, _ := testRelay(t)
	r.Handle(context.Background(), &tg.Updates{Users: []tg.UserClass{&tg.User{ID: 8, FirstName: "Alice"}}, Chats: []tg.ChatClass{&tg.Channel{ID: 9, Title: "Channel", AccessHash: 123}}, Updates: []tg.UpdateClass{
		&tg.UpdateNewChannelMessage{Message: &tg.Message{ID: 1, PeerID: &tg.PeerChannel{ChannelID: 9}, FromID: &tg.PeerUser{UserID: 8}, Mentioned: true}},
		&tg.UpdateNewMessage{Message: &tg.Message{ID: 1, PeerID: &tg.PeerChat{ChatID: 9}, FromID: &tg.PeerUser{UserID: 8}, Mentioned: true}},
	}})
	if len(r.notify.targets[0].queue) != 2 {
		t.Fatal("peer namespaces collided")
	}
}

func TestTelegramDestinationDoesNotLoop(t *testing.T) {
	r, _ := testRelay(t)
	r.notify.botIDs = map[int64]bool{8: true}
	r.Handle(context.Background(), &tg.UpdateShortMessage{ID: 1, UserID: 8, Message: "Telegram｜私聊\nAlice: 点击查看\ntg://"})
	r.Handle(context.Background(), &tg.UpdateShortMessage{ID: 2, UserID: 8, Message: "regular bot message"})
	if len(r.notify.targets[0].queue) != 1 {
		t.Fatal("notification loop or ordinary bot message suppressed")
	}
}
