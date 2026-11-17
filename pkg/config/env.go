// Package config holds tiny helpers for reading configuration from env vars.
// Twelve-factor style: the same binary runs locally, in Docker and in CI;
// only the environment changes.
package config

import (
	"os"
	"strconv"
	"time"
)

// String returns the env var or def when unset/empty.
func String(key, def string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return def
}

// Int returns the env var parsed as int, or def when unset/invalid.
func Int(key string, def int) int {
	if v, err := strconv.Atoi(os.Getenv(key)); err == nil {
		return v
	}
	return def
}

// Duration returns the env var parsed by time.ParseDuration (e.g. "5s"), or def.
func Duration(key string, def time.Duration) time.Duration {
	if v, err := time.ParseDuration(os.Getenv(key)); err == nil {
		return v
	}
	return def
}
