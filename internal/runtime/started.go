package runtime

import "time"

// StartedAt records process start time for diagnostics and uptime reporting.
var StartedAt = time.Now()
