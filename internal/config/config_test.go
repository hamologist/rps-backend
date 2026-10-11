package config

import (
	"log/slog"
	"testing"
	"time"
)

func env(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func TestDefaults(t *testing.T) {
	cfg, err := Load("test", nil, env(nil))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Port != 8080 || cfg.DBPath != "./rps.db" || cfg.PlayerTTL != 24*time.Hour ||
		cfg.SweepInterval != time.Minute || cfg.LogLevel != slog.LevelInfo || len(cfg.AllowedOrigins) != 0 {
		t.Fatalf("unexpected defaults: %+v", cfg)
	}
}

func TestEnvAndFlagOverride(t *testing.T) {
	e := env(map[string]string{
		"RPS_PORT":            "9000",
		"RPS_DB_PATH":         "/tmp/x.db",
		"RPS_ALLOWED_ORIGINS": "http://a.com, http://b.com/",
		"RPS_LOG_LEVEL":       "debug",
		"RPS_GAME_TTL":        "1h",
	})
	cfg, err := Load("test", []string{"-port", "9100", "-game-ttl", "2h"}, e)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Port != 9100 {
		t.Errorf("flag should win: port=%d", cfg.Port)
	}
	if cfg.GameTTL != 2*time.Hour {
		t.Errorf("flag should win: game ttl=%v", cfg.GameTTL)
	}
	if cfg.DBPath != "/tmp/x.db" || cfg.LogLevel != slog.LevelDebug {
		t.Errorf("env not applied: %+v", cfg)
	}
	if len(cfg.AllowedOrigins) != 2 || cfg.AllowedOrigins[1] != "http://b.com" {
		t.Errorf("origins: %v", cfg.AllowedOrigins)
	}
}

func TestInvalid(t *testing.T) {
	for _, args := range [][]string{
		{"-port", "abc"},
		{"-player-ttl", "forever"},
		{"-log-level", "loud"},
		{"-sweep-interval", "0s"},
	} {
		if _, err := Load("test", args, env(nil)); err == nil {
			t.Errorf("expected error for %v", args)
		}
	}
}
