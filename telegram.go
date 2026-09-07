package main

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/gotd/td/tg"
)

type telegramAPI interface {
	MessagesGetMessages(context.Context, []tg.InputMessageClass) (tg.MessagesMessagesClass, error)
	ChannelsGetMessages(context.Context, *tg.ChannelsGetMessagesRequest) (tg.MessagesMessagesClass, error)
	MessagesSendMessage(context.Context, *tg.MessagesSendMessageRequest) (tg.UpdatesClass, error)
	UpdatesGetState(context.Context) (*tg.UpdatesState, error)
}
type userInfo struct {
	name string
	hash int64
}
type peerKey struct {
	kind byte
	id   int64
}
type messageKey struct {
	peer peerKey
	id   int
}
type relay struct {
	mu     sync.Mutex
	api    telegramAPI
	cfg    config
	state  *pushState
	notify *notifier
	me     int64
	ready  bool
	users  *boundedCache[int64, userInfo]
	chats  *boundedCache[peerKey, userInfo]
	seen   *boundedCache[messageKey, bool]
}

func newRelay(c config, s *pushState, n *notifier) *relay {
	return &relay{cfg: c, state: s, notify: n, users: newCache[int64, userInfo](1024), chats: newCache[peerKey, userInfo](1024), seen: newCache[messageKey, bool](4096)}
}
func peer(p tg.PeerClass) peerKey {
	switch v := p.(type) {
	case *tg.PeerUser:
		return peerKey{'u', v.UserID}
	case *tg.PeerChat:
		return peerKey{'g', v.ChatID}
	case *tg.PeerChannel:
		return peerKey{'c', v.ChannelID}
	}
	return peerKey{}
}
func userName(u *tg.User) string {
	name := strings.TrimSpace(u.FirstName + " " + u.LastName)
	if name == "" {
		name = u.Username
	}
	if name == "" {
		name = fmt.Sprint(u.ID)
	}
	return name
}
func (r *relay) entities(users []tg.UserClass, chats []tg.ChatClass) {
	for _, v := range users {
		if u, ok := v.(*tg.User); ok {
			info := userInfo{userName(u), u.AccessHash}
			if old, ok := r.users.get(u.ID); ok && u.Min {
				info.hash = old.hash
			}
			r.users.put(u.ID, info)
		}
	}
	for _, v := range chats {
		switch c := v.(type) {
		case *tg.Chat:
			r.chats.put(peerKey{'g', c.ID}, userInfo{name: c.Title})
		case *tg.Channel:
			info := userInfo{c.Title, c.AccessHash}
			if old, ok := r.chats.get(peerKey{'c', c.ID}); ok && c.Min {
				info.hash = old.hash
			}
			r.chats.put(peerKey{'c', c.ID}, info)
		}
	}
}
func (r *relay) Handle(ctx context.Context, updates tg.UpdatesClass) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.ready {
		return nil
	}
	switch u := updates.(type) {
	case *tg.Updates:
		r.entities(u.Users, u.Chats)
		for _, v := range u.Updates {
			r.update(ctx, v)
		}
	case *tg.UpdatesCombined:
		r.entities(u.Users, u.Chats)
		for _, v := range u.Updates {
			r.update(ctx, v)
		}
	case *tg.UpdateShort:
		r.update(ctx, u.Update)
	case *tg.UpdateShortMessage:
		sender := u.UserID
		if u.Out {
			sender = r.me
		}
		r.process(ctx, &tg.Message{ID: u.ID, PeerID: &tg.PeerUser{UserID: u.UserID}, FromID: &tg.PeerUser{UserID: sender}, Out: u.Out, Mentioned: u.Mentioned, Message: u.Message, ReplyTo: u.ReplyTo})
	case *tg.UpdateShortChatMessage:
		r.process(ctx, &tg.Message{ID: u.ID, PeerID: &tg.PeerChat{ChatID: u.ChatID}, FromID: &tg.PeerUser{UserID: u.FromID}, Out: u.Out, Mentioned: u.Mentioned, Message: u.Message, ReplyTo: u.ReplyTo})
	case *tg.UpdatesTooLong:
		slog.Warn("Telegram 更新出现缺口；仅继续处理实时消息")
	}
	return nil
}
func (r *relay) update(ctx context.Context, u tg.UpdateClass) {
	switch v := u.(type) {
	case *tg.UpdateNewMessage:
		if m, ok := v.Message.(*tg.Message); ok {
			r.process(ctx, m)
		}
	case *tg.UpdateNewChannelMessage:
		if m, ok := v.Message.(*tg.Message); ok {
			r.process(ctx, m)
		}
	}
}

func (r *relay) getMessages(ctx context.Context, p tg.PeerClass, id int) (tg.ModifiedMessagesMessages, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	ids := []tg.InputMessageClass{&tg.InputMessageID{ID: id}}
	var result tg.MessagesMessagesClass
	var err error
	if c, ok := p.(*tg.PeerChannel); ok {
		info, found := r.chats.get(peerKey{'c', c.ChannelID})
		if !found {
			return nil, fmt.Errorf("unknown channel")
		}
		result, err = r.api.ChannelsGetMessages(ctx, &tg.ChannelsGetMessagesRequest{Channel: &tg.InputChannel{ChannelID: c.ChannelID, AccessHash: info.hash}, ID: ids})
	} else {
		result, err = r.api.MessagesGetMessages(ctx, ids)
	}
	if err != nil {
		return nil, err
	}
	modified, ok := result.(tg.ModifiedMessagesMessages)
	if !ok {
		return nil, fmt.Errorf("message unavailable")
	}
	r.entities(modified.GetUsers(), modified.GetChats())
	return modified, nil
}

func (r *relay) replyToMe(ctx context.Context, m *tg.Message) bool {
	h, ok := m.ReplyTo.(*tg.MessageReplyHeader)
	if !ok || h.ReplyToMsgID == 0 {
		return false
	}
	p := m.PeerID
	if h.ReplyToPeerID != nil {
		p = h.ReplyToPeerID
	}
	result, err := r.getMessages(ctx, p, h.ReplyToMsgID)
	if err != nil {
		slog.Warn("查询回复消息失败")
		return false
	}
	for _, v := range result.GetMessages() {
		if original, ok := v.(*tg.Message); ok && original.ID == h.ReplyToMsgID {
			return peer(original.FromID) == (peerKey{'u', r.me})
		}
	}
	return false
}

func (r *relay) process(ctx context.Context, raw *tg.Message) {
	p := peer(raw.PeerID)
	if p.kind == 0 {
		return
	}
	key := messageKey{p, raw.ID}
	if _, ok := r.seen.get(key); ok {
		return
	}
	r.seen.put(key, true)
	sender := peer(raw.FromID)
	if sender.kind == 0 {
		if raw.Out {
			sender = peerKey{'u', r.me}
		} else {
			sender = p
		}
	}
	m := message{ID: raw.ID, ChatID: p.id, SenderID: sender.id, Private: p.kind == 'u', Out: raw.Out, Mentioned: raw.Mentioned, Text: raw.Message}
	// A Telegram notification destination may be this same account. Suppress the
	// relay's own summary echoed by a configured notification bot, not other bot messages.
	if sender.kind == 'u' && r.notify.botIDs[sender.id] && strings.HasPrefix(m.Text, "Telegram｜") && strings.HasSuffix(m.Text, "tg://") {
		return
	}
	if command := savedCommand(m, r.me); command != "" {
		r.command(ctx, command)
		return
	}
	on, _ := r.state.snapshot()
	if !on || (m.Out && !r.cfg.PushSelf) {
		return
	}
	why := reason(m, r.cfg.Username, r.cfg.PushSelf)
	if why == "" {
		m.ReplyToMe = r.replyToMe(ctx, raw)
		why = reason(m, r.cfg.Username, r.cfg.PushSelf)
	}
	if why == "" {
		return
	}
	// Resolve names only for notifications, and only when absent from our bounded cache.
	_, knownSender := r.users.get(sender.id)
	_, knownChat := r.chats.get(p)
	if (sender.kind == 'u' && !knownSender) || (!m.Private && !knownChat) {
		_, _ = r.getMessages(ctx, raw.PeerID, raw.ID)
	}
	if sender.kind == 'u' {
		if u, ok := r.users.get(sender.id); ok {
			m.SenderName = u.name
		}
	} else {
		if c, ok := r.chats.get(sender); ok {
			m.SenderName = c.name
		}
	}
	if m.SenderName == "" {
		m.SenderName = fmt.Sprint(sender.id)
	}
	if c, ok := r.chats.get(p); ok {
		m.ChatName = c.name
	} else {
		m.ChatName = fmt.Sprint(p.id)
	}
	r.notify.enqueue("Telegram｜"+why, notificationBody(m))
}

func (r *relay) command(ctx context.Context, command string) {
	text := ""
	switch command {
	case "/on", "/off":
		on := command == "/on"
		if err := r.state.set(on); err != nil {
			text = "⚠️ 保存推送状态失败，请检查数据目录权限"
		} else {
			r.notify.clear()
			if on {
				text = "✅ 通知推送已开启"
			} else {
				text = "🔕 通知推送已关闭"
			}
		}
	case "/status":
		on, _ := r.state.snapshot()
		if on {
			text = "当前推送状态：开启 ✅"
		} else {
			text = "当前推送状态：关闭 🔕"
		}
	case "/help":
		text = "Telegram 通知控制命令：\n/on 开启推送\n/off 关闭推送\n/status 查看状态\n/help 查看帮助\n仅在收藏夹 / Saved Messages 中生效。"
	}
	var random [8]byte
	if _, err := rand.Read(random[:]); err != nil {
		slog.Warn("生成命令响应标识失败")
		return
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	_, err := r.api.MessagesSendMessage(ctx, &tg.MessagesSendMessageRequest{Peer: &tg.InputPeerSelf{}, Message: text, RandomID: int64(binary.LittleEndian.Uint64(random[:]))})
	if err != nil {
		slog.Warn("发送收藏夹命令响应失败")
	}
}
