package config

import (
	"flag"
	"fmt"
	"os"
	"strconv"

	"github.com/joho/godotenv"
	"gopkg.in/yaml.v3"
)

// Infisical and Cloudflare config blocks are todo
type Config struct {
	Server   ServerConfig   `yaml:"server"`
	Temporal TemporalConfig `yaml:"temporal"`
	Auth     AuthConfig     `yaml:"auth"`
	Discord  DiscordConfig  `yaml:"discord"`
	OTEL     OTELConfig     `yaml:"otel"`
	Logger   LoggerConfig   `yaml:"logger"`
	SSH      SSHConfig      `yaml:"ssh"`
}

type ServerConfig struct {
	Host string `yaml:"host" envconfig:"HOST"`
	Port string `yaml:"port" envconfig:"PORT"`
}

type TemporalConfig struct {
	Address   string `yaml:"address" envconfig:"TEMPORAL_ADDRESS"`
	Namespace string `yaml:"namespace" envconfig:"TEMPORAL_NAMESPACE"`
}

type AuthConfig struct {
	DeployToken string `yaml:"deploy_token" envconfig:"DEPLOY_TOKEN"`
}

type DiscordConfig struct {
	WebhookURL       string `yaml:"webhook_url" envconfig:"DISCORD_WEBHOOK_URL"`
	BotToken         string `yaml:"bot_token" envconfig:"DISCORD_BOT_TOKEN"`
	DefaultChannelID string `yaml:"default_channel_id" envconfig:"DISCORD_DEFAULT_CHANNEL_ID"`
}

type OTELConfig struct {
	CollectorURL string `yaml:"collector_url" envconfig:"OTEL_COLLECTOR_URL"`
}

type LoggerConfig struct {
	Level  string `yaml:"level" envconfig:"LOG_LEVEL"`
	Format string `yaml:"format" envconfig:"LOG_FORMAT"`
}

type SSHConfig struct {
	Host                  string `yaml:"host" envconfig:"SSH_HOST"`
	User                  string `yaml:"user" envconfig:"SSH_USER"`
	BasePath              string `yaml:"base_path" envconfig:"SSH_BASE_PATH"`
	Port                  int    `yaml:"port" envconfig:"SSH_PORT"`
	PrivateKey            string `yaml:"private_key" envconfig:"SSH_PRIVATE_KEY"`
	KnownHostsFile        string `yaml:"known_hosts_file" envconfig:"SSH_KNOWN_HOSTS_FILE"`
	StrictHostKeyChecking bool   `yaml:"strict_host_key_checking" envconfig:"SSH_STRICT_HOST_KEY_CHECKING"`
}

func Load() (*Config, error) {
	config := &Config{
		Server: ServerConfig{
			Host: "localhost",
			Port: "7082",
		},
		Temporal: TemporalConfig{
			Address:   "localhost:7233",
			Namespace: "default",
		},
		Logger: LoggerConfig{
			Level:  "info",
			Format: "json",
		},
		SSH: SSHConfig{
			Host:                  "",
			User:                  "git",
			BasePath:              "/tmp",
			Port:                  22,
			PrivateKey:            "",
			KnownHostsFile:        "",
			StrictHostKeyChecking: true,
		},
	}

	// Load from file
	if err := loadFromFile("config.yaml", config); err != nil {
		if !os.IsNotExist(err) {
			return nil, fmt.Errorf("failed to load config from file: %w", err)
		}
	}

	// Load from .env
	if err := godotenv.Overload(); err != nil {
		if !os.IsNotExist(err) {
			return nil, fmt.Errorf("failed to load .env file: %w", err)
		}
	}

	// Load from environment variables
	loadFromEnv(config)

	// Load from flags
	loadFromFlags(config)

	return config, nil
}

func loadFromFile(filePath string, config *Config) error {
	file, err := os.Open(filePath)
	if err != nil {
		return err
	}
	defer file.Close()

	fileConfig := &Config{}
	if err := yaml.NewDecoder(file).Decode(fileConfig); err != nil {
		return err
	}

	// Merge configs
	if fileConfig.Server.Host != "" {
		config.Server.Host = fileConfig.Server.Host
	}
	if fileConfig.Server.Port != "" {
		config.Server.Port = fileConfig.Server.Port
	}
	if fileConfig.Temporal.Address != "" {
		config.Temporal.Address = fileConfig.Temporal.Address
	}
	if fileConfig.Temporal.Namespace != "" {
		config.Temporal.Namespace = fileConfig.Temporal.Namespace
	}
	if fileConfig.Auth.DeployToken != "" {
		config.Auth.DeployToken = fileConfig.Auth.DeployToken
	}
	if fileConfig.Discord.WebhookURL != "" {
		config.Discord.WebhookURL = fileConfig.Discord.WebhookURL
	}
	if fileConfig.Discord.BotToken != "" {
		config.Discord.BotToken = fileConfig.Discord.BotToken
	}
	if fileConfig.Discord.DefaultChannelID != "" {
		config.Discord.DefaultChannelID = fileConfig.Discord.DefaultChannelID
	}
	if fileConfig.OTEL.CollectorURL != "" {
		config.OTEL.CollectorURL = fileConfig.OTEL.CollectorURL
	}
	if fileConfig.Logger.Level != "" {
		config.Logger.Level = fileConfig.Logger.Level
	}
	if fileConfig.Logger.Format != "" {
		config.Logger.Format = fileConfig.Logger.Format
	}
	if fileConfig.SSH.Host != "" {
		config.SSH.Host = fileConfig.SSH.Host
	}
	if fileConfig.SSH.User != "" {
		config.SSH.User = fileConfig.SSH.User
	}
	if fileConfig.SSH.BasePath != "" {
		config.SSH.BasePath = fileConfig.SSH.BasePath
	}
	if fileConfig.SSH.Port != 0 {
		config.SSH.Port = fileConfig.SSH.Port
	}
	if fileConfig.SSH.PrivateKey != "" {
		config.SSH.PrivateKey = fileConfig.SSH.PrivateKey
	}
	if fileConfig.SSH.KnownHostsFile != "" {
		config.SSH.KnownHostsFile = fileConfig.SSH.KnownHostsFile
	}
	// StrictHostKeyChecking: check if SSH config exists (non-zero value struct)
	// If SSH config exists in file, use its value
	if fileConfig.SSH.Host != "" || fileConfig.SSH.User != "" {
		config.SSH.StrictHostKeyChecking = fileConfig.SSH.StrictHostKeyChecking
	}

	return nil
}

func loadFromEnv(config *Config) {
	if host := os.Getenv("HOST"); host != "" {
		config.Server.Host = host
	}
	if port := os.Getenv("PORT"); port != "" {
		config.Server.Port = port
	}
	if address := os.Getenv("TEMPORAL_ADDRESS"); address != "" {
		config.Temporal.Address = address
	}
	if namespace := os.Getenv("TEMPORAL_NAMESPACE"); namespace != "" {
		config.Temporal.Namespace = namespace
	}
	if token := os.Getenv("DEPLOY_TOKEN"); token != "" {
		config.Auth.DeployToken = token
	}
	if webhookURL := os.Getenv("DISCORD_WEBHOOK_URL"); webhookURL != "" {
		config.Discord.WebhookURL = webhookURL
	}
	if botToken := os.Getenv("DISCORD_BOT_TOKEN"); botToken != "" {
		config.Discord.BotToken = botToken
	}
	if channelID := os.Getenv("DISCORD_DEFAULT_CHANNEL_ID"); channelID != "" {
		config.Discord.DefaultChannelID = channelID
	}
	if collectorURL := os.Getenv("OTEL_COLLECTOR_URL"); collectorURL != "" {
		config.OTEL.CollectorURL = collectorURL
	}
	if level := os.Getenv("LOG_LEVEL"); level != "" {
		config.Logger.Level = level
	}
	if format := os.Getenv("LOG_FORMAT"); format != "" {
		config.Logger.Format = format
	}
	if host := os.Getenv("SSH_HOST"); host != "" {
		config.SSH.Host = host
	}
	if user := os.Getenv("SSH_USER"); user != "" {
		config.SSH.User = user
	}
	if basePath := os.Getenv("SSH_BASE_PATH"); basePath != "" {
		config.SSH.BasePath = basePath
	}
	if portStr := os.Getenv("SSH_PORT"); portStr != "" {
		if port, err := strconv.Atoi(portStr); err == nil {
			config.SSH.Port = port
		}
	}
	if knownHostsFile := os.Getenv("SSH_KNOWN_HOSTS_FILE"); knownHostsFile != "" {
		config.SSH.KnownHostsFile = knownHostsFile
	}
	if privateKey := os.Getenv("SSH_PRIVATE_KEY"); privateKey != "" {
		config.SSH.PrivateKey = privateKey
	}
	if strictStr := os.Getenv("SSH_STRICT_HOST_KEY_CHECKING"); strictStr != "" {
		config.SSH.StrictHostKeyChecking = strictStr == "true" || strictStr == "1"
	}
}

func loadFromFlags(config *Config) {
	flag.StringVar(&config.Server.Host, "host", config.Server.Host, "server host")
	flag.StringVar(&config.Server.Port, "port", config.Server.Port, "server port")
	flag.StringVar(&config.Temporal.Address, "temporal-address", config.Temporal.Address, "temporal server address")
	flag.StringVar(&config.Temporal.Namespace, "temporal-namespace", config.Temporal.Namespace, "temporal namespace")
	flag.StringVar(&config.Auth.DeployToken, "deploy-token", config.Auth.DeployToken, "deploy token")
	flag.StringVar(&config.OTEL.CollectorURL, "otel-collector-url", config.OTEL.CollectorURL, "OpenTelemetry collector URL")
	flag.StringVar(&config.Logger.Level, "log-level", config.Logger.Level, "log level")
	flag.StringVar(&config.Logger.Format, "log-format", config.Logger.Format, "log format")

	flag.Parse()
}

func (c *Config) Validate() error {
	if c.Auth.DeployToken == "" {
		return fmt.Errorf("deploy_token is required")
	}
	if c.SSH.Host == "" {
		return fmt.Errorf("ssh.host is required")
	}
	if c.SSH.User == "" {
		return fmt.Errorf("ssh.user is required")
	}
	if c.SSH.Port <= 0 || c.SSH.Port > 65535 {
		return fmt.Errorf("ssh.port must be between 1 and 65535")
	}
	// Validate SSH private key: PrivateKey must be set
	if c.SSH.PrivateKey == "" {
		return fmt.Errorf("ssh.private_key is required (set via SSH_PRIVATE_KEY environment variable or config file)")
	}
	// Note: KnownHostsFile can be empty if using default ~/.ssh/known_hosts
	// Only validate if StrictHostKeyChecking is enabled and a custom file is specified
	if c.SSH.StrictHostKeyChecking && c.SSH.KnownHostsFile != "" {
		// File existence will be checked at runtime
	}
	return nil
}
