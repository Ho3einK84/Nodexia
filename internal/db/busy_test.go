package db

import (
	"fmt"
	"testing"
)

type fakeSQLiteErr struct{ code int }

func (e fakeSQLiteErr) Error() string { return fmt.Sprintf("sqlite error %d", e.code) }
func (e fakeSQLiteErr) Code() int     { return e.code }

func TestIsBusyDetectsExtendedSQLiteCodes(t *testing.T) {
	cases := []struct {
		code int
		want bool
	}{
		{5, true},    // SQLITE_BUSY
		{261, true},  // SQLITE_BUSY_RECOVERY (5 | (1<<8))
		{517, true},  // SQLITE_BUSY_SNAPSHOT (5 | (2<<8))
		{6, true},    // SQLITE_LOCKED
		{262, true},  // SQLITE_LOCKED_SHAREDCACHE (6 | (1<<8))
		{19, false},  // SQLITE_CONSTRAINT
		{275, false}, // SQLITE_CONSTRAINT_CHECK
	}
	for _, tc := range cases {
		if got := IsBusy(fakeSQLiteErr{code: tc.code}); got != tc.want {
			t.Errorf("IsBusy(code=%d) = %v, want %v", tc.code, got, tc.want)
		}
	}
}
