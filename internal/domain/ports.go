package domain

import "context"

// SSHExecutor interface for executing SSH operations
type SSHExecutor interface {
	// Execute executes a command on a remote host via SSH
	Execute(ctx context.Context, host string, user string, privateKey []byte, command string, envVars map[string]string) (string, error)
}

// DNSProvider manages DNS records for per-PR preview hostnames.
type DNSProvider interface {
	// EnsureRecord ensures a DNS A record exists for domain pointing at ip.
	EnsureRecord(ctx context.Context, domain, ip string) error

	// RemoveRecord removes the DNS A record for domain (no-op if absent).
	RemoveRecord(ctx context.Context, domain string) error
}

// NotificationState classifies a notification for color/styling purposes.
// "success" → deploy succeeded (green)
// "cleanup" → cleanup workflow finished (blue, distinct from a real deploy)
// "failure" → any failed run (red)
type NotificationState string

const (
	NotificationStateSuccess NotificationState = "success"
	NotificationStateCleanup NotificationState = "cleanup"
	NotificationStateFailure NotificationState = "failure"
)

// Notifier interface for sending notifications
type Notifier interface {
	// SendNotification sends a notification. state drives color/icon choice.
	SendNotification(ctx context.Context, title, message string, state NotificationState, metadata map[string]string) error
}
