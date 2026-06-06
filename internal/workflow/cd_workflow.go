package workflow

import (
	"github.com/Dart147/SMC/deploy/internal/activity"
	"github.com/Dart147/SMC/deploy/internal/domain"
	"errors"
	"time"

	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

// extractRootCause unwraps a Temporal activity error to get the human-readable root cause.
func extractRootCause(err error) string {
	var appErr *temporal.ApplicationError
	current := err
	for current != nil {
		if errors.As(current, &appErr) {
			if unwrapped := appErr.Unwrap(); unwrapped != nil {
				current = unwrapped
				continue
			}
			return appErr.Message()
		}
		if unwrapped := errors.Unwrap(current); unwrapped != nil {
			current = unwrapped
			continue
		}
		return current.Error()
	}
	return err.Error()
}

// CDWorkflow orchestrates the CD deployment process
func CDWorkflow(ctx workflow.Context, req domain.DeployRequest) error {
	logger := workflow.GetLogger(ctx)
	logger.Info("CD Workflow started",
		"project", req.Metadata.ProjectName,
		"environment", req.Metadata.Environment,
		"method", string(req.Method),
		"trace_id", req.TraceID,
	)

	// Configure Activity Options
	ao := workflow.ActivityOptions{
		StartToCloseTimeout: 10 * time.Minute,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:    time.Second,
			BackoffCoefficient: 2.0,
			MaximumInterval:    time.Minute,
			MaximumAttempts:    3,
		},
	}
	ctx = workflow.WithActivityOptions(ctx, ao)

	// Temporary short-circuit: exercise the Discord path before SSH is ready.
	if req.Post.NotifyDiscord.NotifyOnly {
		logger.Info("notify_only=true → skipping secrets/SSH/DNS, running Discord only")
		title := "Deployment Successful"
		if req.Method == domain.MethodCleanup {
			title = "Cleanup Complete"
		}
		if err := workflow.ExecuteActivity(ctx, activity.ActivitySendDiscordNotification, req, title, "").Get(ctx, nil); err != nil {
			logger.Error("notify-only discord activity failed", "error", err)
			return err
		}
		logger.Info("CD Workflow completed successfully (notify-only)")
		return nil
	}

	// Step 1: Secret injection (Infisical) — todo. Payload field
	// setup.inject_secret is accepted for forward-compatibility but no-op.
	if req.Setup.InjectSecret.Enable {
		logger.Info("setup.inject_secret.enable=true ignored — Infisical adapter not in POC scope")
	}
	var secrets map[string]string

	// Step 2: Execute SSH Deployment/Cleanup
	var deployOutput string
	err := workflow.ExecuteActivity(ctx, activity.ActivityRunSSHDeploy, req, secrets).Get(ctx, &deployOutput)
	if err != nil {
		logger.Error("SSH deployment failed", "error", err)
		failTitle := "Deployment Failed"
		if req.Method == domain.MethodCleanup {
			failTitle = "Cleanup Failed"
		}
		if notifyErr := workflow.ExecuteActivity(ctx, activity.ActivitySendDiscordNotification, req, failTitle, extractRootCause(err)).Get(ctx, nil); notifyErr != nil {
			logger.Error("Failed to send failure notification", "error", notifyErr)
		}
		return err
	}
	logger.Info("SSH deployment completed successfully")

	// Step 3: Handle DNS (if enabled)
	if req.Method == domain.MethodDeploy && req.Post.SetupDomain.Enable {
		if req.Post.SetupDomain.Name != "" && req.Post.SetupDomain.Value != "" {
			logger.Info("Setting up DNS record",
				"name", req.Post.SetupDomain.Name,
				"value", req.Post.SetupDomain.Value,
			)
			// Extract IP from value (if it's a service:port format, we'll need to resolve it)
			// For now, assume value is an IP address
			ip := req.Post.SetupDomain.Value
			err := workflow.ExecuteActivity(ctx, activity.ActivityEnsureDNSRecord,
				req.Post.SetupDomain.Name,
				ip,
			).Get(ctx, nil)
			if err != nil {
				// Fail the deploy: the DNS record the preview has to work
				logger.Error("Failed to setup DNS record", "error", err)
				if notifyErr := workflow.ExecuteActivity(ctx, activity.ActivitySendDiscordNotification, req, "Deployment Failed", extractRootCause(err)).Get(ctx, nil); notifyErr != nil {
					logger.Error("Failed to send failure notification", "error", notifyErr)
				}
				return err
			}
		}
	} else if req.Method == domain.MethodCleanup && req.Post.CleanupDomain.Enable {
		if req.Post.CleanupDomain.Name != "" {
			logger.Info("Cleaning up DNS record", "name", req.Post.CleanupDomain.Name)
			err := workflow.ExecuteActivity(ctx, activity.ActivityRemoveDNSRecord, req.Post.CleanupDomain.Name).Get(ctx, nil)
			if err != nil {
				logger.Error("Failed to cleanup DNS record", "error", err)
				// Don't fail the workflow if DNS cleanup fails, but log it
			}
		}
	}

	// Step 4: Send success notification
	if req.Post.NotifyDiscord.Enable {
		logger.Info("Sending success notification")
		successTitle := "Deployment Successful"
		if req.Method == domain.MethodCleanup {
			successTitle = "Cleanup Complete"
		}
		if err := workflow.ExecuteActivity(ctx, activity.ActivitySendDiscordNotification, req, successTitle, "").Get(ctx, nil); err != nil {
			logger.Error("Failed to send success notification", "error", err)
			// Don't fail the workflow if notification fails, but log it
		}
	}

	logger.Info("CD Workflow completed successfully")
	return nil
}
