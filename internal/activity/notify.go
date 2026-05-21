package activity

import (
	"github.com/Dart147/SMC/deploy/internal/domain"
	"context"
	"fmt"

	"go.temporal.io/sdk/activity"
	"go.uber.org/zap"
)

// NotifyActivity handles notification activities
type NotifyActivity struct {
	notifier domain.Notifier
	logger   *zap.Logger
}

// NewNotifyActivity creates a new notification activity
func NewNotifyActivity(notifier domain.Notifier, logger *zap.Logger) *NotifyActivity {
	return &NotifyActivity{
		notifier: notifier,
		logger:   logger,
	}
}

// SendDiscordNotification sends a Discord notification.
// errMsg is the error message string (empty string means success).
func (a *NotifyActivity) SendDiscordNotification(ctx context.Context, req domain.DeployRequest, status string, errMsg string) error {
	logger := activity.GetLogger(ctx)

	state := domain.NotificationStateSuccess
	switch {
	case errMsg != "":
		state = domain.NotificationStateFailure
	case req.Method == domain.MethodCleanup:
		state = domain.NotificationStateCleanup
	}

	title := status
	message := fmt.Sprintf("%s for %s", status, req.Metadata.ProjectName)

	if errMsg != "" {
		message = fmt.Sprintf("%s\nError: %s", message, errMsg)
	}

	metadata := map[string]string{
		"Project":     req.Metadata.ProjectName,
		"Component":   req.Metadata.Component,
		"Environment": req.Metadata.Environment,
		"Method":      string(req.Method),
		"Repo":        req.Source.Repo,
		"Commit":      req.Source.Commit,
	}

	if req.TraceID != "" {
		metadata["Trace ID"] = req.TraceID
	}

	logger.Info("Sending Discord notification",
		zap.String("title", title),
		zap.String("state", string(state)),
		zap.String("project", req.Metadata.ProjectName),
		zap.String("environment", req.Metadata.Environment),
		zap.String("component", req.Metadata.Component),
	)

	if notifyErr := a.notifier.SendNotification(ctx, title, message, state, metadata); notifyErr != nil {
		logger.Error("Failed to send Discord notification",
			zap.Error(notifyErr),
			zap.String("title", title),
			zap.String("project", req.Metadata.ProjectName),
		)
		// Return error so workflow knows notification failed
		// Workflow can decide whether to fail or just log
		return fmt.Errorf("failed to send Discord notification: %w", notifyErr)
	}

	logger.Info("Discord notification sent successfully",
		zap.String("title", title),
		zap.String("state", string(state)),
		zap.String("project", req.Metadata.ProjectName),
	)

	return nil
}
