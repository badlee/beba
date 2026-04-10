package config

import (
	"os"
	"strings"
)

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func parseBool(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "y", "true", "yes", "on", "enabled":
		return true
	}
	return false
}
