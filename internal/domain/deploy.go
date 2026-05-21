package domain

import "time"

// DeployMethod represents the deployment method
type DeployMethod string

const (
	MethodDeploy  DeployMethod = "deploy"
	MethodCleanup DeployMethod = "cleanup"
)

// DeployRequest represents the deployment request payload
type DeployRequest struct {
	Source   SourceInfo   `json:"source" validate:"required"`
	Method   DeployMethod `json:"method" validate:"required,oneof=deploy cleanup"`
	Metadata MetadataInfo `json:"metadata" validate:"required"`
	Setup    SetupConfig  `json:"setup"`
	Post     PostActions  `json:"post"`
	TraceID  string       `json:"trace_id"`
}

// SourceInfo contains source code information
type SourceInfo struct {
	Title     string `json:"title" validate:"required"`
	Repo      string `json:"repo" validate:"required"`
	Branch    string `json:"branch" validate:"required"`
	Commit    string `json:"commit" validate:"required"`
	PRNumber  string `json:"pr_number,omitempty"`
	PRTitle   string `json:"pr_title,omitempty"`
	PRType    string `json:"pr_type,omitempty"`
	PRPurpose string `json:"pr_purpose,omitempty"`
}

// MetadataInfo contains deployment metadata
type MetadataInfo struct {
	ProjectName string `json:"project_name" validate:"required"`
	Component   string `json:"component" validate:"required"`
	Environment string `json:"environment" validate:"required,oneof=snapshot dev stage production"`
}

// SetupConfig contains setup configuration
type SetupConfig struct {
	InjectSecret InjectSecretConfig `json:"inject_secret"`
}

// SecretMapping represents a single secret mapping configuration
type SecretMapping struct {
	Path       string `json:"path" validate:"required"`
	SecretName string `json:"secret_name" validate:"required"`
	EnvName    string `json:"env_name" validate:"required"`
}

// InjectSecretConfig contains Infisical secret injection configuration
type InjectSecretConfig struct {
	Enable      bool            `json:"enable"`
	Project     string          `json:"project,omitempty"`
	Environment string          `json:"environment,omitempty"`
	Secrets     []SecretMapping `json:"secrets,omitempty"`
}

// PostActions contains post-deployment actions
type PostActions struct {
	SetupDomain   DomainConfig  `json:"setup_domain"`
	CleanupDomain DomainConfig  `json:"cleanup_domain"`
	NotifyDiscord DiscordConfig `json:"notify_discord"`
}

// DomainConfig contains DNS domain configuration
type DomainConfig struct {
	Enable bool   `json:"enable"`
	Title  string `json:"title,omitempty"`
	Name   string `json:"name,omitempty" validate:"omitempty,fqdn"`
	Value  string `json:"value,omitempty"`
}

// DiscordConfig contains Discord notification configuration
type DiscordConfig struct {
	Enable  bool   `json:"enable"`
	Channel string `json:"channel,omitempty"`
	// NotifyOnly skips secrets/SSH/DNS and only runs the Discord activity.
	NotifyOnly bool `json:"notify_only,omitempty"`
}

// DeployResult represents the result of a deployment
type DeployResult struct {
	Success   bool      `json:"success"`
	Output    string    `json:"output"`
	Error     string    `json:"error,omitempty"`
	Timestamp time.Time `json:"timestamp"`
}
