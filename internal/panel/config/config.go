package config

import (
	"fmt"
	"log/slog"
	"net"
	"os"
	"strconv"
)

const (
	// DefaultDataDir is where the panel and the node keep their files in the containers;
	// SCILLA_DATA_DIR moves it. The node binary shares it, which is why it lives in a
	// package that imports nothing of the panel.
	DefaultDataDir = "/data"
	// DefaultNodeSocket is the unix socket the panel reaches its own node by;
	// SCILLA_NODE_SOCKET moves it.
	DefaultNodeSocket = "/run/scilla/node.sock"
)

type Config struct {
	DataDir    string
	Listen     string
	NodeSocket string
	LogLevel   slog.Level
	TrustProxy bool
	// Dev serves plain HTTP for UI development; only a loopback address is accepted.
	Dev bool
	// AutotuneScale shortens the automatic moves' timings (tests: 0.01); 1 in production.
	AutotuneScale float64
	// TelegramAPI is the Bot API the bot talks to; a fake one in tests.
	TelegramAPI string
	// SupportOff: SCILLA_SUPPORT_ACCESS=off turns support access off for good on this server,
	// whatever the panel's setting says.
	SupportOff bool
	// SupportBotURL is the support bot (SCILLA_SUPPORT_BOT_URL, for tests and development);
	// "" is the official one.
	SupportBotURL string
}

func FromEnv() (Config, error) {
	c := Config{
		DataDir:    env("SCILLA_DATA_DIR", DefaultDataDir),
		Listen:     env("SCILLA_PANEL_LISTEN", "0.0.0.0:2053"),
		NodeSocket: env("SCILLA_NODE_SOCKET", DefaultNodeSocket),
	}
	if err := c.LogLevel.UnmarshalText([]byte(env("SCILLA_LOG_LEVEL", "info"))); err != nil {
		return c, fmt.Errorf("SCILLA_LOG_LEVEL: %w", err)
	}
	var err error
	if c.TrustProxy, err = boolEnv("SCILLA_TRUST_PROXY"); err != nil {
		return c, err
	}
	if c.Dev, err = boolEnv("SCILLA_DEV"); err != nil {
		return c, err
	}
	if _, _, err := net.SplitHostPort(c.Listen); err != nil {
		return c, fmt.Errorf("SCILLA_PANEL_LISTEN: %w", err)
	}
	c.TelegramAPI = os.Getenv("SCILLA_TG_API")
	c.SupportBotURL = os.Getenv("SCILLA_SUPPORT_BOT_URL")
	switch v := os.Getenv("SCILLA_SUPPORT_ACCESS"); v {
	case "", "on":
	case "off":
		c.SupportOff = true
	default:
		return c, fmt.Errorf("SCILLA_SUPPORT_ACCESS: want on or off, got %q", v)
	}
	c.AutotuneScale = 1
	if v := os.Getenv("SCILLA_AUTOTUNE_SCALE"); v != "" {
		if c.AutotuneScale, err = strconv.ParseFloat(v, 64); err != nil || c.AutotuneScale <= 0 || c.AutotuneScale > 1 {
			return c, fmt.Errorf("SCILLA_AUTOTUNE_SCALE: want a number in (0, 1], got %q", v)
		}
	}
	// "off" runs the panel without a node (UI development on a machine without mihomo).
	if c.NodeSocket == "off" {
		c.NodeSocket = ""
	}
	if c.Dev && !loopback(c.Listen) {
		return c, fmt.Errorf("SCILLA_DEV=1 allowed only with a loopback SCILLA_PANEL_LISTEN, got %q", c.Listen)
	}
	return c, nil
}

func env(key, def string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return def
}

func boolEnv(key string) (bool, error) {
	v := os.Getenv(key)
	if v == "" {
		return false, nil
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return false, fmt.Errorf("%s: %w", key, err)
	}
	return b, nil
}

func loopback(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return false
	}
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
