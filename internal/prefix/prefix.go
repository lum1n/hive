package prefix

import (
	"fmt"
	"strings"
)

// Parse maps a config name to the byte Hive steals as the client chord.
func Parse(name string) (byte, error) {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "", "ctrl-space", "ctrl+space":
		return 0, nil
	case "ctrl-g", "ctrl+g":
		return 0x07, nil
	case "ctrl-\\", "ctrl+\\":
		return 0x1c, nil
	case "ctrl-]", "ctrl+]":
		return 0x1d, nil
	case "ctrl-^", "ctrl+^":
		return 0x1e, nil
	case "ctrl-_", "ctrl+_":
		return 0x1f, nil
	}
	if strings.HasPrefix(strings.ToLower(name), "ctrl-") || strings.HasPrefix(strings.ToLower(name), "ctrl+") {
		rest := name[5:]
		if len(rest) == 1 {
			c := rest[0]
			if c >= 'a' && c <= 'z' {
				return c - 'a' + 1, nil
			}
			if c >= 'A' && c <= 'Z' {
				return c - 'A' + 1, nil
			}
		}
	}
	return 0, fmt.Errorf("unknown prefix %q (try ctrl-space or ctrl-g)", name)
}

func Label(name string) string {
	if strings.TrimSpace(name) == "" {
		return "ctrl-space"
	}
	return strings.ToLower(strings.TrimSpace(name))
}
