package discord

import (
	"github.com/Dart147/SMC/deploy/internal/domain"
	// "bytes"
	"context"
	// "encoding/json"
	"fmt"
	// "net/http"
	"time"

	"github.com/bwmarrin/discordgo"
	"go.uber.org/zap"
)

// Client implements domain.Notifier interface
type Client struct {
	// webhookURL string
	// httpClient *http.Client
	botToken         string
	defaultChannelID string
	logger           *zap.Logger
}

// NewClient creates a new Discord bot client
func NewClient(botToken, defaultChannelID string, logger *zap.Logger) *Client {
	return &Client{
		// webhookURL: webhookURL,
		// httpClient: &http.Client{Timeout: 10 * time.Second},
		botToken:         botToken,
		defaultChannelID: defaultChannelID,
		logger:           logger,
	}
}

// Embed represents a Discord embed
type Embed struct {
	Title       string  `json:"title"`
	Description string  `json:"description,omitempty"`
	Color       int     `json:"color"`
	Fields      []Field `json:"fields,omitempty"`
	Timestamp   string  `json:"timestamp,omitempty"`
}

// Field represents a Discord embed field
type Field struct {
	Name   string `json:"name"`
	Value  string `json:"value"`
	Inline bool   `json:"inline,omitempty"`
}

// WebhookPayload represents the Discord webhook payload
type WebhookPayload struct {
	Embeds []Embed `json:"embeds"`
}

// SendNotification sends a notification to Discord
func (c *Client) SendNotification(ctx context.Context, title, message string, state domain.NotificationState, metadata map[string]string) error {
	if c.botToken == "" {
		return fmt.Errorf("discord bot token is empty (set DISCORD_BOT_TOKEN)")
	}

	if c.defaultChannelID == "" {
		return fmt.Errorf("discord default channel id is empty (set DISCORD_DEFAULT_CHANNEL_ID)")
	}

	var color int
	switch state {
	case domain.NotificationStateCleanup:
		color = 0x3498DB // Blue — cleanup completed (distinct from a real deploy)
	case domain.NotificationStateFailure:
		color = 0xFF0000 // Red
	default:
		color = 0x00FF00 // Green for success
	}

	// fields := make([]Field, 0, len(metadata))
	fields := make([]*discordgo.MessageEmbedField, 0, len(metadata))
	for key, value := range metadata {
		// fields = append(fields, Field{
		// 	Name:   key,
		// 	Value:  value,
		// 	Inline: true,
		// })
		fields = append(fields, &discordgo.MessageEmbedField{
			Name:   key,
			Value:  value,
			Inline: true,
		})
	}

	// embed := Embed{
	// 	Title:       title,
	// 	Description: message,
	// 	Color:       color,
	// 	Fields:      fields,
	// 	Timestamp:   time.Now().UTC().Format(time.RFC3339),
	// }
	embed := &discordgo.MessageEmbed{
		Title:       title,
		Description: message,
		Color:       color,
		Fields:      fields,
		Timestamp:   time.Now().UTC().Format(time.RFC3339),
	}

	session, err := discordgo.New("Bot " + c.botToken)
	if err != nil {
		return fmt.Errorf("failed to create discord session: %w", err)
	}
	session.Client.Timeout = 10 * time.Second

	// payload := WebhookPayload{
	// 	Embeds: []Embed{embed},
	// }
	// jsonData, err := json.Marshal(payload)
	// if err != nil {
	// 	return fmt.Errorf("failed to marshal payload: %w", err)
	// }
	// req, err := http.NewRequestWithContext(ctx, "POST", c.webhookURL, bytes.NewBuffer(jsonData))
	// if err != nil {
	// 	return fmt.Errorf("failed to create request: %w", err)
	// }
	// req.Header.Set("Content-Type", "application/json")
	// resp, err := c.httpClient.Do(req)
	_, err = session.ChannelMessageSendComplex(c.defaultChannelID, &discordgo.MessageSend{
		Embeds: []*discordgo.MessageEmbed{embed},
	})
	if err != nil {
		return fmt.Errorf("failed to send discord bot message: %w", err)
	}

	_ = ctx

	c.logger.Info("Discord notification sent",
		zap.String("title", title),
		zap.String("state", string(state)),
		zap.String("channel_id", c.defaultChannelID),
	)

	return nil
}

// Ensure Client implements domain.Notifier
var _ domain.Notifier = (*Client)(nil)
