package domain

import "context"

// SSHExecutor interface for executing SSH operations
type SSHExecutor interface {
	// Execute executes a command on a remote host via SSH
	Execute(ctx context.Context, host string, user string, privateKey []byte, command string, envVars map[string]string) (string, error)
}

// Notifier interface for sending notifications
type Notifier interface {
	// SendNotification sends a notification with the given message and status
	SendNotification(ctx context.Context, title, message string, success bool, metadata map[string]string) error
}
