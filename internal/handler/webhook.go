package handler

import (
	"github.com/Dart147/SMC/deploy/internal/domain"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/go-playground/validator/v10"
	"github.com/google/uuid"
	"go.temporal.io/sdk/client"
	"go.uber.org/zap"
)

// WebhookHandler handles webhook requests
type WebhookHandler struct {
	temporalClient client.Client
	validator      *validator.Validate
	logger         *zap.Logger
}

// NewWebhookHandler creates a new webhook handler
func NewWebhookHandler(temporalClient client.Client, validator *validator.Validate, logger *zap.Logger) *WebhookHandler {
	return &WebhookHandler{
		temporalClient: temporalClient,
		validator:      validator,
		logger:         logger,
	}
}

// DeployRequest represents the webhook request payload
type DeployRequestPayload struct {
	Source   domain.SourceInfo   `json:"source" validate:"required"`
	Method   domain.DeployMethod `json:"method" validate:"required,oneof=deploy cleanup"`
	Metadata domain.MetadataInfo `json:"metadata" validate:"required"`
	Setup    domain.SetupConfig  `json:"setup"`
	Post     domain.PostActions  `json:"post"`
}

// DeployResponse represents the webhook response
type DeployResponse struct {
	WorkflowID string `json:"workflow_id"`
	RunID      string `json:"run_id"`
	TraceID    string `json:"trace_id"`
	Status     string `json:"status"`
}

// HandleDeploy handles the deployment webhook request
func (h *WebhookHandler) HandleDeploy(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	logger := h.logger.With(
		zap.String("method", r.Method),
		zap.String("path", r.URL.Path),
	)

	// Parse request body
	var payload DeployRequestPayload
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		logger.Error("Failed to decode request body", zap.Error(err))
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	// Validate request
	if err := h.validator.Struct(payload); err != nil {
		logger.Error("Request validation failed", zap.Error(err))
		http.Error(w, "Validation failed: "+err.Error(), http.StatusBadRequest)
		return
	}

	// Validate conditional required fields
	if err := h.validateConditionalFields(payload); err != nil {
		logger.Error("Conditional validation failed", zap.Error(err))
		http.Error(w, "Validation failed: "+err.Error(), http.StatusBadRequest)
		return
	}

	// Generate trace ID
	traceID := uuid.New().String()
	logger = logger.With(zap.String("trace_id", traceID))

	// Build deploy request
	deployReq := domain.DeployRequest{
		Source:   payload.Source,
		Method:   payload.Method,
		Metadata: payload.Metadata,
		Setup:    payload.Setup,
		Post:     payload.Post,
		TraceID:  traceID,
	}

	// Start workflow
	workflowOptions := client.StartWorkflowOptions{
		ID:        "deploy-" + traceID,
		TaskQueue: "cd-task-queue",
	}

	workflowRun, err := h.temporalClient.ExecuteWorkflow(ctx, workflowOptions, "CDWorkflow", deployReq)
	if err != nil {
		logger.Error("Failed to start workflow", zap.Error(err))
		http.Error(w, "Failed to start workflow", http.StatusInternalServerError)
		return
	}

	logger.Info("Workflow started",
		zap.String("workflow_id", workflowRun.GetID()),
		zap.String("run_id", workflowRun.GetRunID()),
	)

	// Return response
	response := DeployResponse{
		WorkflowID: workflowRun.GetID(),
		RunID:      workflowRun.GetRunID(),
		TraceID:    traceID,
		Status:     "started",
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	if err := json.NewEncoder(w).Encode(response); err != nil {
		logger.Error("Failed to encode response", zap.Error(err))
	}
}

// validateConditionalFields validates fields that are required conditionally
func (h *WebhookHandler) validateConditionalFields(payload DeployRequestPayload) error {
	// Validate InjectSecret: if enable=true, project, environment, and secrets are required
	if payload.Setup.InjectSecret.Enable {
		if payload.Setup.InjectSecret.Project == "" {
			return fmt.Errorf("project is required when inject_secret.enable is true")
		}
		if payload.Setup.InjectSecret.Environment == "" {
			return fmt.Errorf("environment is required when inject_secret.enable is true")
		}
		if len(payload.Setup.InjectSecret.Secrets) == 0 {
			return fmt.Errorf("secrets array is required when inject_secret.enable is true")
		}
		// Validate each secret mapping
		for i, secret := range payload.Setup.InjectSecret.Secrets {
			if secret.Path == "" {
				return fmt.Errorf("secrets[%d].path is required", i)
			}
			if secret.SecretName == "" {
				return fmt.Errorf("secrets[%d].secret_name is required", i)
			}
			if secret.EnvName == "" {
				return fmt.Errorf("secrets[%d].env_name is required", i)
			}
		}
	}

	// Validate SetupDomain: if enable=true, title, name, and value are required
	if payload.Post.SetupDomain.Enable {
		if payload.Post.SetupDomain.Title == "" {
			return fmt.Errorf("title is required when setup_domain.enable is true")
		}
		if payload.Post.SetupDomain.Name == "" {
			return fmt.Errorf("name is required when setup_domain.enable is true")
		}
		if payload.Post.SetupDomain.Value == "" {
			return fmt.Errorf("value is required when setup_domain.enable is true")
		}
	}

	// Validate CleanupDomain: if enable=true, name is required (title and value are optional for cleanup)
	if payload.Post.CleanupDomain.Enable {
		if payload.Post.CleanupDomain.Name == "" {
			return fmt.Errorf("name is required when cleanup_domain.enable is true")
		}
	}

	return nil
}
