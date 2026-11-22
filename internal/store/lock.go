package store

import (
	"os"
	"strings"
)

// lockHolder is the " (pid N)" note for a lock conflict, or "" when the
// holder recorded no pid.
func lockHolder(lockPath string) string {
	data, err := os.ReadFile(lockPath)
	if err != nil {
		return ""
	}
	pid := strings.TrimSpace(string(data))
	if pid == "" {
		return ""
	}
	return " (pid " + pid + ")"
}
