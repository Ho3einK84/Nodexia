// Package notify delivers alert notifications to external destinations. The
// Notifier interface abstracts the transport so additional channel kinds
// (webhook, email) can be added later, and so handlers and the scheduler can be
// tested against a mock instead of a live service.
package notify

import "context"

// Notifier delivers a freeform text message to a destination identified by a
// channel-specific address — a Telegram chat id today. Implementations must be
// safe for concurrent use.
type Notifier interface {
	Send(ctx context.Context, chatID, text string) error
}
