package app

import (
	"os"
	"strings"
)

func xrayLogLevel(key, fallback string) string {
	switch level := strings.ToLower(strings.TrimSpace(os.Getenv(key))); level {
	case "debug", "info", "warning", "error", "none":
		return level
	default:
		return fallback
	}
}
