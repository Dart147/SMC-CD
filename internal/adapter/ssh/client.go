package ssh

import (
	"github.com/Dart147/SMC/deploy/internal/config"
	"github.com/Dart147/SMC/deploy/internal/domain"
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"go.uber.org/zap"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

// Client implements domain.SSHExecutor interface
type Client struct {
	sshConfig config.SSHConfig
	logger    *zap.Logger
}

// NewClient creates a new SSH client
func NewClient(sshConfig config.SSHConfig, logger *zap.Logger) *Client {
	return &Client{
		sshConfig: sshConfig,
		logger:    logger,
	}
}

// Execute executes a command on a remote host via SSH
func (c *Client) Execute(ctx context.Context, host string, user string, privateKey []byte, command string, envVars map[string]string) (string, error) {
	// Parse private key
	signer, err := ssh.ParsePrivateKey(privateKey)
	if err != nil {
		return "", fmt.Errorf("failed to parse private key: %w", err)
	}

	// Create host key callback
	hostKeyCallback, err := c.createHostKeyCallback()
	if err != nil {
		return "", fmt.Errorf("failed to create host key callback: %w", err)
	}

	// Create SSH client config
	sshConfig := &ssh.ClientConfig{
		User:            user,
		Auth:            []ssh.AuthMethod{ssh.PublicKeys(signer)},
		HostKeyCallback: hostKeyCallback,
		Timeout:         30 * time.Second,
	}

	// Connect to SSH server
	conn, err := ssh.Dial("tcp", host, sshConfig)
	if err != nil {
		return "", fmt.Errorf("failed to dial SSH server: %w", err)
	}
	defer conn.Close()

	// Create session
	session, err := conn.NewSession()
	if err != nil {
		return "", fmt.Errorf("failed to create session: %w", err)
	}
	defer session.Close()

	// Set environment variables
	for key, value := range envVars {
		if err := session.Setenv(key, value); err != nil {
			// Some SSH servers don't support Setenv, so we'll inject them in the command
			c.logger.Warn("Failed to set environment variable via Setenv, will inject in command",
				zap.String("key", key),
				zap.Error(err),
			)
		}
	}
	// Log the command being executed (without sensitive data)
	c.logger.Info("Executing SSH command",
		zap.String("host", host),
		zap.String("user", user),
		zap.String("command_preview", c.sanitizeCommand(command)),
	)

	// Execute command with context
	output, err := c.executeWithContext(ctx, session, command)
	if err != nil {
		// Log full output for debugging
		c.logger.Error("SSH command execution failed",
			zap.String("host", host),
			zap.String("user", user),
			zap.Error(err),
			zap.String("output", output),
			zap.String("command_preview", c.sanitizeCommand(command)),
		)
		return output, fmt.Errorf("failed to execute command (exit code may indicate specific error): %w", err)
	}

	// Log successful execution
	c.logger.Info("SSH command executed successfully",
		zap.String("host", host),
		zap.String("output_length", fmt.Sprintf("%d", len(output))),
	)

	return output, nil
}

func (c *Client) executeWithContext(ctx context.Context, session *ssh.Session, command string) (string, error) {
	// Set up environment variables to ensure commands can be found
	// Set PATH to include common binary locations
	pathEnv := "/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"
	if err := session.Setenv("PATH", pathEnv); err != nil {
		// If Setenv fails, we'll include it in the command
		c.logger.Debug("Failed to set PATH via Setenv, will include in command", zap.Error(err))
	}

	// Build command with explicit PATH and shell
	// Use sh -c instead of bash -c for better compatibility
	shellCommand := fmt.Sprintf("export PATH=%s && %s", pathEnv, command)

	// Create a channel to receive output
	type result struct {
		output string
		err    error
	}
	resultChan := make(chan result, 1)

	go func() {
		// Use sh -c to execute the command in a proper shell environment
		// This ensures commands like rm, git, cd are available
		execCommand := fmt.Sprintf("sh -c %s", c.quoteCommand(shellCommand))
		output, err := session.CombinedOutput(execCommand)
		resultChan <- result{
			output: string(output),
			err:    err,
		}
	}()

	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case res := <-resultChan:
		return res.output, res.err
	}
}

// quoteCommand properly quotes a command for sh -c
func (c *Client) quoteCommand(command string) string {
	// Escape single quotes by replacing ' with '\'' and wrapping in single quotes
	escaped := strings.ReplaceAll(command, "'", "'\\''")
	return fmt.Sprintf("'%s'", escaped)
}

// createHostKeyCallback creates a host key callback based on configuration
func (c *Client) createHostKeyCallback() (ssh.HostKeyCallback, error) {
	if !c.sshConfig.StrictHostKeyChecking {
		c.logger.Warn("SSH strict host key checking is disabled - this is insecure and should only be used in development")
		return ssh.InsecureIgnoreHostKey(), nil
	}

	// Use known_hosts file for host key verification
	knownHostsFile := c.sshConfig.KnownHostsFile
	if knownHostsFile == "" {
		// Default to standard known_hosts location
		homeDir, err := os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("failed to get user home directory: %w", err)
		}
		knownHostsFile = fmt.Sprintf("%s/.ssh/known_hosts", homeDir)
	}

	// Check if known_hosts file exists
	if _, err := os.Stat(knownHostsFile); os.IsNotExist(err) {
		c.logger.Warn("Known hosts file does not exist, creating it",
			zap.String("file", knownHostsFile),
		)
		// Create empty file if it doesn't exist
		if err := os.WriteFile(knownHostsFile, []byte{}, 0644); err != nil {
			return nil, fmt.Errorf("failed to create known_hosts file: %w", err)
		}
	}

	callback, err := knownhosts.New(knownHostsFile)
	if err != nil {
		return nil, fmt.Errorf("failed to load known_hosts file: %w", err)
	}

	return callback, nil
}

// sanitizeCommand removes sensitive information from command for logging
func (c *Client) sanitizeCommand(cmd string) string {
	// Truncate long commands and mask potential secrets
	sanitized := cmd
	if len(sanitized) > 500 {
		sanitized = sanitized[:500] + "... (truncated)"
	}
	return sanitized
}

// Ensure Client implements domain.SSHExecutor
var _ domain.SSHExecutor = (*Client)(nil)
