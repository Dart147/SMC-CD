package activity

import (
	"github.com/Dart147/SMC/deploy/internal/config"
	"github.com/Dart147/SMC/deploy/internal/domain"
	"context"
	"fmt"
	"strings"

	"go.temporal.io/sdk/activity"
	"go.uber.org/zap"
)

// SSHActivity handles SSH deployment activities
type SSHActivity struct {
	sshExecutor domain.SSHExecutor
	sshConfig   config.SSHConfig
	logger      *zap.Logger
}

// NewSSHActivity creates a new SSH activity
func NewSSHActivity(sshExecutor domain.SSHExecutor, sshConfig config.SSHConfig, logger *zap.Logger) *SSHActivity {
	return &SSHActivity{
		sshExecutor: sshExecutor,
		sshConfig:   sshConfig,
		logger:      logger,
	}
}

// RunSSHDeploy executes deployment via SSH
func (a *SSHActivity) RunSSHDeploy(ctx context.Context, req domain.DeployRequest, secrets map[string]string) (string, error) {
	logger := activity.GetLogger(ctx)

	// Validate request early to provide better error messages
	if req.Source.Repo == "" {
		return "", fmt.Errorf("Source.Repo is required but was empty")
	}
	if req.Metadata.Environment == "" {
		return "", fmt.Errorf("Metadata.Environment is required but was empty")
	}
	if req.Source.Branch == "" {
		return "", fmt.Errorf("Source.Branch is required but was empty")
	}
	if req.Source.Commit == "" {
		return "", fmt.Errorf("Source.Commit is required but was empty")
	}
	if a.sshConfig.BasePath == "" {
		return "", fmt.Errorf("SSH BasePath is required but was empty")
	}

	logger.Info("Starting SSH deployment",
		zap.String("repo", req.Source.Repo),
		zap.String("commit", req.Source.Commit),
		zap.String("method", string(req.Method)),
		zap.String("environment", req.Metadata.Environment),
		zap.String("branch", req.Source.Branch),
	)

	// Build host address with port
	host := fmt.Sprintf("%s:%d", a.sshConfig.Host, a.sshConfig.Port)
	user := a.sshConfig.User

	logger.Info("Using SSH configuration",
		zap.String("host", host),
		zap.String("user", user),
		zap.String("base_path", a.sshConfig.BasePath),
	)

	// Build deployment command
	var command string
	if req.Method == domain.MethodDeploy {
		command = a.buildDeployCommand(req, secrets)
	} else {
		command = a.buildCleanupCommand(req, secrets)
	}

	logger.Info("Built deployment command",
		zap.String("method", string(req.Method)),
		zap.String("command_preview", a.sanitizeCommand(command)),
	)

	// Get SSH private key from config
	privateKey, err := a.getSSHPrivateKey()
	if err != nil {
		logger.Error("Failed to get SSH private key",
			zap.Error(err),
		)
		return "", fmt.Errorf("failed to get SSH private key: %w", err)
	}

	// Execute command via SSH
	output, err := a.sshExecutor.Execute(ctx, host, user, privateKey, command, secrets)
	if err != nil {
		// Enhanced error logging with command output
		logger.Error("SSH deployment failed",
			zap.Error(err),
			zap.String("repo", req.Source.Repo),
			zap.String("commit", req.Source.Commit),
			zap.String("method", string(req.Method)),
			zap.String("host", host),
			zap.String("user", user),
			zap.String("command_output", output),
			zap.String("command_preview", a.sanitizeCommand(command)),
		)

		// Provide more specific error message based on common Git errors
		if strings.Contains(output, "fatal:") {
			// Extract Git fatal error message
			lines := strings.Split(output, "\n")
			for _, line := range lines {
				if strings.Contains(line, "fatal:") {
					return output, fmt.Errorf("Git operation failed: %s. Full error: %w", strings.TrimSpace(line), err)
				}
			}
		} else if strings.Contains(output, "Permission denied") {
			return output, fmt.Errorf("SSH authentication failed (Permission denied). Check SSH key permissions and repository access. Error: %w", err)
		} else if strings.Contains(output, "Host key verification failed") {
			return output, fmt.Errorf("SSH host key verification failed. Add host to known_hosts or disable strict checking. Error: %w", err)
		}

		return output, fmt.Errorf("SSH deployment failed: %w", err)
	}

	logger.Info("SSH deployment completed successfully",
		zap.String("repo", req.Source.Repo),
		zap.String("method", string(req.Method)),
		zap.String("output_length", fmt.Sprintf("%d", len(output))),
	)

	return output, nil
}

func (a *SSHActivity) buildDeployCommand(req domain.DeployRequest, secrets map[string]string) string {
	// Validate required fields to prevent slice bounds errors
	if req.Source.Repo == "" {
		return "echo 'Error: Source.Repo is required but was empty' && exit 1"
	}
	if req.Metadata.Environment == "" {
		return "echo 'Error: Metadata.Environment is required but was empty' && exit 1"
	}
	if req.Source.Branch == "" {
		return "echo 'Error: Source.Branch is required but was empty' && exit 1"
	}
	if req.Source.Commit == "" {
		return "echo 'Error: Source.Commit is required but was empty' && exit 1"
	}
	if a.sshConfig.BasePath == "" {
		return "echo 'Error: SSH BasePath is required but was empty' && exit 1"
	}

	// Build directory structure: /tmp/${ENVIRONMENT}/${REPO_NAME}
	tmpDir := fmt.Sprintf("%s/%s/%s", a.sshConfig.BasePath, req.Metadata.Environment, req.Source.Repo)
	repoDir := fmt.Sprintf("%s/repo", tmpDir)
	deployDir := fmt.Sprintf("%s/.deploy/%s", repoDir, req.Metadata.Environment)

	// Determine if this is a private repo
	hasPrivateKey := secrets["REPO_PRIVATE_KEY"] != ""

	// Build repo URL
	repoURL := a.buildRepoURL(req.Source.Repo, hasPrivateKey)

	// Build commands
	var commands []string

	// Clean up existing directory
	commands = append(commands, fmt.Sprintf("rm -rf %s", tmpDir))
	commands = append(commands, fmt.Sprintf("mkdir -p %s", tmpDir))
	commands = append(commands, fmt.Sprintf("cd %s", tmpDir))

	// Setup SSH config for private repo if needed
	if hasPrivateKey {
		sshDir := fmt.Sprintf("%s/.ssh", tmpDir)
		sshConfig := a.buildPrivateRepoSSHConfig(sshDir, secrets["REPO_PRIVATE_KEY"])
		commands = append(commands, sshConfig...)
	}

	// Build clone commands with fallback
	cloneCommands := a.buildCloneCommands(repoURL, repoDir, req.Source.Branch, req.Source.Commit, hasPrivateKey, tmpDir)
	commands = append(commands, cloneCommands)

	// Build script execution command
	scriptCmd := a.buildScriptExecutionCommand(deployDir, "deploy", req, secrets)
	commands = append(commands, scriptCmd)

	// Cleanup
	commands = append(commands, fmt.Sprintf("rm -rf %s", tmpDir))

	return strings.Join(commands, " && ")
}

func (a *SSHActivity) buildCleanupCommand(req domain.DeployRequest, secrets map[string]string) string {
	// Validate required fields to prevent slice bounds errors
	if req.Source.Repo == "" {
		return "echo 'Error: Source.Repo is required but was empty' && exit 1"
	}
	if req.Metadata.Environment == "" {
		return "echo 'Error: Metadata.Environment is required but was empty' && exit 1"
	}
	if a.sshConfig.BasePath == "" {
		return "echo 'Error: SSH BasePath is required but was empty' && exit 1"
	}

	// Build directory structure: /tmp/${ENVIRONMENT}/${REPO_NAME}
	tmpDir := fmt.Sprintf("%s/%s/%s", a.sshConfig.BasePath, req.Metadata.Environment, req.Source.Repo)
	repoDir := fmt.Sprintf("%s/repo", tmpDir)
	deployDir := fmt.Sprintf("%s/.deploy/%s", repoDir, req.Metadata.Environment)

	var commands []string

	// Build script execution command
	scriptCmd := a.buildScriptExecutionCommand(deployDir, "cleanup", req, secrets)

	// Check if deploy directory exists before cleanup
	// Use semicolons inside the if statement, then && to connect with other commands
	ifBlock := fmt.Sprintf("if [ -d %s ]; then cd %s && %s; fi", deployDir, deployDir, scriptCmd)
	commands = append(commands, ifBlock)

	// Cleanup
	commands = append(commands, fmt.Sprintf("rm -rf %s", tmpDir))

	return strings.Join(commands, " && ")
}

// getSSHPrivateKey retrieves SSH private key from config
func (a *SSHActivity) getSSHPrivateKey() ([]byte, error) {
	if a.sshConfig.PrivateKey == "" {
		return nil, fmt.Errorf("no SSH private key configured (set via SSH_PRIVATE_KEY environment variable or config file)")
	}

	privateKeyStr := strings.TrimSpace(a.sshConfig.PrivateKey)

	// Check if it looks like a valid SSH private key (should contain BEGIN/END markers)
	if !strings.Contains(privateKeyStr, "BEGIN") || !strings.Contains(privateKeyStr, "END") {
		return nil, fmt.Errorf("invalid SSH private key format: key must contain BEGIN and END markers. Current value appears to be a placeholder or invalid format")
	}

	return []byte(privateKeyStr), nil
}

// buildRepoURL builds the repository URL based on whether it's private or public
func (a *SSHActivity) buildRepoURL(repo string, isPrivate bool) string {
	if isPrivate {
		return fmt.Sprintf("git@github.com:%s.git", repo)
	}
	return fmt.Sprintf("https://github.com/%s", repo)
}

// buildPrivateRepoSSHConfig builds SSH config commands for private repository
func (a *SSHActivity) buildPrivateRepoSSHConfig(sshDir, privateKey string) []string {
	// Validate inputs
	if sshDir == "" {
		return []string{"echo 'Error: sshDir is required but was empty' && exit 1"}
	}
	if privateKey == "" {
		return []string{"echo 'Error: privateKey is required but was empty' && exit 1"}
	}

	// Use base64 encoding to safely pass private key through shell
	// This avoids issues with special characters in the key
	keyFile := fmt.Sprintf("%s/repo_private_key", sshDir)
	configFile := fmt.Sprintf("%s/config", sshDir)
	tmpDir := strings.TrimSuffix(sshDir, "/.ssh")

	commands := []string{
		fmt.Sprintf("mkdir -p %s", sshDir),
		fmt.Sprintf("cd %s", sshDir),
		// Write private key using printf to handle special characters safely
		fmt.Sprintf("printf '%%s\\n' %s > %s", a.quoteShell(privateKey), keyFile),
		fmt.Sprintf("chmod 600 %s", keyFile),
		// Write SSH config
		fmt.Sprintf("cat > %s <<'SSHCONFIG'\nHost github.com\n    HostName github.com\n    User git\n    IdentityFile %s\n    IdentitiesOnly yes\n    StrictHostKeyChecking accept-new\nSSHCONFIG", configFile, keyFile),
		fmt.Sprintf("cd %s", tmpDir),
	}
	return commands
}

// buildCloneCommands builds git clone commands with fallback strategy
func (a *SSHActivity) buildCloneCommands(repoURL, repoDir, branch, commit string, hasPrivateKey bool, tmpDir string) string {
	sshDir := fmt.Sprintf("%s/.ssh", tmpDir)

	// Build git command prefix for private repo
	gitPrefix := ""
	if hasPrivateKey {
		gitPrefix = fmt.Sprintf("GIT_SSH_COMMAND=\"ssh -F %s/config\" ", sshDir)
	}

	// Main strategy: shallow clone with branch
	mainClone := fmt.Sprintf("%sgit clone --depth=1 --branch %s %s repo", gitPrefix, a.quoteShell(branch), repoURL)

	// Fallback strategy: full clone + checkout commit
	fallbackClone := fmt.Sprintf(
		"%sgit clone %s repo --no-checkout && cd repo && git fetch origin %s && git checkout %s && cd ..",
		gitPrefix, repoURL, a.quoteShell(commit), a.quoteShell(commit),
	)

	// Try main strategy first, fallback if it fails
	// Using shell function to implement try_chain logic
	return fmt.Sprintf(
		"(%s) || (%s)",
		mainClone,
		fallbackClone,
	)
}

// buildScriptExecutionCommand builds the command to execute deploy.sh or cleanup.sh
func (a *SSHActivity) buildScriptExecutionCommand(deployDir, scriptType string, req domain.DeployRequest, secrets map[string]string) string {
	scriptName := "deploy.sh"
	if scriptType == "cleanup" {
		scriptName = "cleanup.sh"
	}

	// Build environment variables
	envVars := []string{
		fmt.Sprintf("REPO_NAME=%s", a.quoteShell(req.Source.Repo)),
		fmt.Sprintf("PR_NUMBER=%s", a.quoteShell(req.Source.PRNumber)),
		fmt.Sprintf("TRACE_ID=%s", a.quoteShell(req.TraceID)),
		fmt.Sprintf("ENVIRONMENT=%s", a.quoteShell(req.Metadata.Environment)),
	}

	// Point deploy.sh at the host's stable .env. Path lives in gitignored
	// config (ssh.env_file), so it's never committed to the deployed repo.
	if a.sshConfig.EnvFile != "" {
		envVars = append(envVars, fmt.Sprintf("SMC_ENV_FILE=%s", a.quoteShell(a.sshConfig.EnvFile)))
	}

	// Add secrets as environment variables
	for key, value := range secrets {
		// Skip REPO_PRIVATE_KEY as it's handled separately
		if key != "REPO_PRIVATE_KEY" {
			envVars = append(envVars, fmt.Sprintf("%s=%s", key, a.quoteShell(value)))
		}
	}

	// Build command
	envPrefix := strings.Join(envVars, " ")
	return fmt.Sprintf(
		"cd %s && chmod +x %s && %s bash ./%s",
		deployDir,
		scriptName,
		envPrefix,
		scriptName,
	)
}

// quoteShell properly quotes a string for shell command
func (a *SSHActivity) quoteShell(s string) string {
	// Escape single quotes by replacing ' with '\''
	escaped := strings.ReplaceAll(s, "'", "'\"'\"'")
	return fmt.Sprintf("'%s'", escaped)
}

// sanitizeCommand removes sensitive information from command for logging
func (a *SSHActivity) sanitizeCommand(cmd string) string {
	// Truncate long commands
	sanitized := cmd
	if len(sanitized) > 500 {
		sanitized = sanitized[:500] + "... (truncated)"
	}
	// Remove private key content from logs
	sanitized = strings.ReplaceAll(sanitized, "REPO_PRIVATE_KEY", "REPO_PRIVATE_KEY=[REDACTED]")
	return sanitized
}
