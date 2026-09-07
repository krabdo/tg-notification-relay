package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/gotd/td/session"
	"github.com/gotd/td/telegram"
	"github.com/gotd/td/telegram/auth"
	"github.com/gotd/td/tg"
	"golang.org/x/term"
)

var version = "dev"

func main() {
	if err := execute(); err != nil {
		slog.Error(err.Error())
		os.Exit(1)
	}
}
func execute() error {
	command := "run"
	if len(os.Args) > 1 {
		command = os.Args[1]
	}
	switch command {
	case "version":
		fmt.Println(version)
		return nil
	case "services":
		fmt.Println(strings.Join(services(), "\n"))
		return nil
	case "help", "--help", "-h":
		fmt.Println("tg-notification-relay [login|run|version|services]")
		return nil
	case "run", "login":
	default:
		return errors.New("未知命令，使用 --help 查看用法")
	}
	cfg, err := loadConfig(command == "run")
	if err != nil {
		return err
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	storage := &sessionFile{path: cfg.Session}
	if command == "login" {
		client := telegram.NewClient(cfg.APIID, cfg.APIHash, telegram.Options{SessionStorage: storage})
		err = client.Run(ctx, func(ctx context.Context) error {
			flow := auth.NewFlow(&terminalAuth{reader: bufio.NewReader(os.Stdin)}, auth.SendCodeOptions{})
			if err := client.Auth().IfNecessary(ctx, flow); err != nil {
				return err
			}
			slog.Info("Telegram 登录成功，会话已保存")
			return nil
		})
		if err != nil && ctx.Err() == nil {
			return errors.New("Telegram 登录失败，请检查凭据、验证码和网络后重试")
		}
		return nil
	}
	state, err := openState(cfg.State)
	if err != nil {
		slog.Warn("状态文件读取失败，默认开启推送")
	}
	notify, err := newNotifier(cfg.URLs, state)
	if err != nil {
		return err
	}
	notify.start(ctx)
	defer func() { cancel(); notify.wg.Wait() }()
	for ctx.Err() == nil {
		r := newRelay(cfg, state, notify)
		inbox := newIngress()
		client := telegram.NewClient(cfg.APIID, cfg.APIHash, telegram.Options{SessionStorage: storage, UpdateHandler: inbox})
		r.api = client.API()
		var unauthorized bool
		err = client.Run(ctx, func(ctx context.Context) error {
			status, err := client.Auth().Status(ctx)
			if err != nil {
				return err
			}
			if !status.Authorized {
				unauthorized = true
				return errors.New("unauthorized")
			}
			me, err := client.Self(ctx)
			if err != nil {
				return err
			}
			r.mu.Lock()
			r.me = me.ID
			r.users.put(me.ID, userInfo{name: userName(me), hash: me.AccessHash})
			r.ready = true
			r.mu.Unlock()
			// GetState subscribes this connection to account updates. No history replay.
			if _, err := r.api.UpdatesGetState(ctx); err != nil {
				return err
			}
			slog.Info("Telegram 监听已启动")
			inbox.run(ctx, r)
			return ctx.Err()
		})
		if unauthorized {
			return errors.New("尚未登录或会话已失效，请先执行 login")
		}
		if ctx.Err() != nil {
			break
		}
		slog.Warn("Telegram 连接中断，5 秒后重连")
		select {
		case <-ctx.Done():
		case <-time.After(5 * time.Second):
		}
	}
	return nil
}

type sessionFile struct {
	mu   sync.Mutex
	path string
}

func (s *sessionFile) LoadSession(context.Context) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, err := os.ReadFile(s.path)
	if os.IsNotExist(err) {
		return nil, session.ErrNotFound
	}
	return b, err
}
func (s *sessionFile) StoreSession(_ context.Context, b []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return atomicWrite(s.path, b)
}

type terminalAuth struct{ reader *bufio.Reader }

func (a *terminalAuth) read(prompt string) (string, error) {
	fmt.Print(prompt)
	s, err := a.reader.ReadString('\n')
	return strings.TrimSpace(s), err
}
func (a *terminalAuth) Phone(context.Context) (string, error) {
	return a.read("手机号（含国家区号）: ")
}
func (a *terminalAuth) Code(context.Context, *tg.AuthSentCode) (string, error) {
	return a.read("Telegram 验证码: ")
}
func (a *terminalAuth) Password(context.Context) (string, error) {
	fmt.Print("二步验证密码: ")
	b, err := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Println()
	return string(b), err
}
func (a *terminalAuth) AcceptTermsOfService(context.Context, tg.HelpTermsOfService) error {
	return errors.New("请先在 Telegram 官方客户端注册账号")
}
func (a *terminalAuth) SignUp(context.Context) (auth.UserInfo, error) {
	return auth.UserInfo{}, errors.New("请先在 Telegram 官方客户端注册账号")
}
