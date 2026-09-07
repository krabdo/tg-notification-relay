package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/joho/godotenv"
)

type config struct {
	APIID                             int
	APIHash, Session, State, Username string
	PushSelf                          bool
	URLs                              []string
}

func loadConfig(notifications bool) (config, error) {
	if err := godotenv.Load(); err != nil && !os.IsNotExist(err) {
		return config{}, errors.New("无法读取 .env")
	}
	c := config{APIHash: os.Getenv("TG_API_HASH"), Session: envDefault("TG_SESSION", "data/telegram.session.json"), State: envDefault("STATE_FILE", "data/state.json"), Username: strings.TrimPrefix(strings.ToLower(os.Getenv("MY_USERNAME")), "@")}
	var err error
	c.APIID, err = strconv.Atoi(os.Getenv("TG_API_ID"))
	if err != nil || c.APIID <= 0 || c.APIHash == "" {
		return c, errors.New("请配置有效的 TG_API_ID 和 TG_API_HASH")
	}
	c.PushSelf, err = strconv.ParseBool(envDefault("PUSH_SELF_MESSAGES", "false"))
	if err != nil {
		return c, errors.New("PUSH_SELF_MESSAGES 必须为 true 或 false")
	}
	if !notifications {
		return c, nil
	}
	if raw, ok := os.LookupEnv("SHOUTRRR_URLS"); ok {
		if json.Unmarshal([]byte(raw), &c.URLs) != nil {
			return c, errors.New("SHOUTRRR_URLS 必须是 URL 字符串的 JSON 数组")
		}
	}
	if len(c.URLs) == 0 {
		return c, errors.New("请配置 SHOUTRRR_URLS，至少包含一个通知 URL")
	}
	for i, u := range c.URLs {
		if strings.TrimSpace(u) == "" {
			return c, fmt.Errorf("通知目标 %d 为空", i+1)
		}
	}
	return c, nil
}

func envDefault(key, fallback string) string {
	if s := os.Getenv(key); s != "" {
		return s
	}
	return fallback
}
