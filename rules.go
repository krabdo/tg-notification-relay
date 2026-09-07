package main

import (
	"strings"
	"unicode"
)

type message struct {
	ID                                 int
	ChatID, SenderID                   int64
	Private, Out, Mentioned, ReplyToMe bool
	Text, ChatName, SenderName         string
}

func usernameMention(text, username string) bool {
	if username == "" {
		return false
	}
	r, needle := []rune(strings.ToLower(text)), []rune("@"+strings.ToLower(username))
	word := func(c rune) bool { return unicode.IsLetter(c) || unicode.IsNumber(c) || c == '_' }
	for i := 0; i+len(needle) <= len(r); i++ {
		if string(r[i:i+len(needle)]) == string(needle) && (i == 0 || !word(r[i-1])) && (i+len(needle) == len(r) || !word(r[i+len(needle)])) {
			return true
		}
	}
	return false
}

func reason(m message, username string, self bool) string {
	if m.Out && !self {
		return ""
	}
	if m.Private {
		return "私聊"
	}
	if m.Mentioned {
		return "被@"
	}
	if usernameMention(m.Text, username) {
		return "用户名@"
	}
	if m.ReplyToMe {
		return "回复你"
	}
	return ""
}

func savedCommand(m message, me int64) string {
	if !m.Out || !m.Private || m.ChatID != me {
		return ""
	}
	switch s := strings.ToLower(strings.TrimSpace(m.Text)); s {
	case "/on", "/off", "/status", "/help":
		return s
	}
	return ""
}

func notificationBody(m message) string {
	name := m.SenderName
	if name == "" {
		name = "未知"
	}
	body := name + ": 点击查看"
	if !m.Private {
		chat := m.ChatName
		if chat == "" {
			chat = "未知"
		}
		body = chat + "\n" + body
	}
	return body
}
