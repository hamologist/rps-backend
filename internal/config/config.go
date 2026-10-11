// Package config loads server configuration from environment variables,
// overridable by command-line flags.
package config

import (
	"flag"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"
)

// Config holds all runtime settings.
type Config struct {
	Port           int
	DBPath         string
	AllowedOrigins []string
	PlayerTTL      time.Duration
	GameTTL        time.Duration
	SweepInterval  time.Duration
	AuthTimeout    time.Duration
	PingInterval   time.Duration
	LogLevel       slog.Level
}

// Default returns the configuration used when nothing is set.
func Default() Config {
	return Config{
		Port:          8080,
		DBPath:        "./rps.db",
		PlayerTTL:     24 * time.Hour,
		GameTTL:       24 * time.Hour,
		SweepInterval: time.Minute,
		AuthTimeout:   10 * time.Second,
		PingInterval:  30 * time.Second,
		LogLevel:      slog.LevelInfo,
	}
}

// Load builds a Config from getenv (e.g. os.Getenv) and args (flags, without
// the program name). Flags take precedence over environment variables.
func Load(name string, args []string, getenv func(string) string) (Config, error) {
	cfg := Default()

	envOr := func(key, def string) string {
		if v := strings.TrimSpace(getenv(key)); v != "" {
			return v
		}
		return def
	}

	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	port := fs.String("port", envOr("RPS_PORT", strconv.Itoa(cfg.Port)), "listen port (RPS_PORT)")
	dbPath := fs.String("db-path", envOr("RPS_DB_PATH", cfg.DBPath), "SQLite file path (RPS_DB_PATH)")
	origins := fs.String("allowed-origins", envOr("RPS_ALLOWED_ORIGINS", ""), "comma-separated allowed origins (RPS_ALLOWED_ORIGINS)")
	playerTTL := fs.String("player-ttl", envOr("RPS_PLAYER_TTL", cfg.PlayerTTL.String()), "player inactivity expiry (RPS_PLAYER_TTL)")
	gameTTL := fs.String("game-ttl", envOr("RPS_GAME_TTL", cfg.GameTTL.String()), "game inactivity expiry (RPS_GAME_TTL)")
	sweep := fs.String("sweep-interval", envOr("RPS_SWEEP_INTERVAL", cfg.SweepInterval.String()), "sweeper period (RPS_SWEEP_INTERVAL)")
	authTimeout := fs.String("auth-timeout", envOr("RPS_AUTH_TIMEOUT", cfg.AuthTimeout.String()), "WebSocket auth timeout (RPS_AUTH_TIMEOUT)")
	ping := fs.String("ping-interval", envOr("RPS_PING_INTERVAL", cfg.PingInterval.String()), "WebSocket ping interval (RPS_PING_INTERVAL)")
	logLevel := fs.String("log-level", envOr("RPS_LOG_LEVEL", "info"), "debug|info|warn|error (RPS_LOG_LEVEL)")

	if err := fs.Parse(args); err != nil {
		return Config{}, err
	}

	p, err := strconv.Atoi(*port)
	if err != nil || p < 1 || p > 65535 {
		return Config{}, fmt.Errorf("invalid port %q", *port)
	}
	cfg.Port = p

	if *dbPath == "" {
		return Config{}, fmt.Errorf("db path must not be empty")
	}
	cfg.DBPath = *dbPath
	cfg.AllowedOrigins = ParseOrigins(*origins)

	durations := []struct {
		name string
		raw  string
		dst  *time.Duration
	}{
		{"player-ttl", *playerTTL, &cfg.PlayerTTL},
		{"game-ttl", *gameTTL, &cfg.GameTTL},
		{"sweep-interval", *sweep, &cfg.SweepInterval},
		{"auth-timeout", *authTimeout, &cfg.AuthTimeout},
		{"ping-interval", *ping, &cfg.PingInterval},
	}
	for _, d := range durations {
		v, err := time.ParseDuration(d.raw)
		if err != nil || v <= 0 {
			return Config{}, fmt.Errorf("invalid %s %q", d.name, d.raw)
		}
		*d.dst = v
	}

	if err := cfg.LogLevel.UnmarshalText([]byte(*logLevel)); err != nil {
		return Config{}, fmt.Errorf("invalid log level %q", *logLevel)
	}
	return cfg, nil
}

// ParseOrigins splits a comma-separated origin list, trimming whitespace and
// trailing slashes and dropping empty entries.
func ParseOrigins(s string) []string {
	var out []string
	for _, o := range strings.Split(s, ",") {
		o = strings.TrimRight(strings.TrimSpace(o), "/")
		if o != "" {
			out = append(out, o)
		}
	}
	return out
}
