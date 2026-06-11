package testutil

import (
	"os"
	"strings"
	"testing"
)

// ClearNodexiaEnv unsets NODEXIA_* variables so config tests start from a clean slate.
func ClearNodexiaEnv(t *testing.T) {
	t.Helper()
	for _, entry := range os.Environ() {
		key, _, ok := strings.Cut(entry, "=")
		if !ok {
			continue
		}
		if strings.HasPrefix(key, "NODEXIA_") {
			_ = os.Unsetenv(key)
		}
	}
}
