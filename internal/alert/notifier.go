// Package alert handles sending notifications to various channels.
package alert

import (
	"fmt"

	"github.com/bwmarrin/discordgo"
	"github.com/your-org/obi-scalp-bot/internal/config"
)

// Notifier is the interface for sending alert messages.
type Notifier interface {
	Send(message string) error
}

// discordSession is an interface abstracting the discordgo.Session for testability.
type discordSession interface {
	UserChannelCreate(recipientID string, options ...discordgo.RequestOption) (*discordgo.Channel, error)
	ChannelMessageSend(channelID string, content string, options ...discordgo.RequestOption) (*discordgo.Message, error)
	Close() error
}

// DiscordNotifier sends messages to a Discord user via DM.
type DiscordNotifier struct {
	session discordSession
	userID  string
}

// NewDiscordNotifier creates a new DiscordNotifier.
// It requires a valid bot token and a user ID to send messages to.
func NewDiscordNotifier(cfg config.DiscordConfig) (*DiscordNotifier, error) {
	if !cfg.Enabled {
		return nil, nil
	}

	if cfg.BotToken == "" || cfg.UserID == "" {
		return nil, fmt.Errorf("discord bot token and user ID must be configured")
	}

	dg, err := discordgo.New("Bot " + cfg.BotToken)
	if err != nil {
		return nil, fmt.Errorf("failed to create discord session: %w", err)
	}

	return &DiscordNotifier{
		session: dg,
		userID:  cfg.UserID,
	}, nil
}

// Send sends a direct message to the configured user.
func (n *DiscordNotifier) Send(message string) error {
	// Create a DM channel with the user.
	channel, err := n.session.UserChannelCreate(n.userID)
	if err != nil {
		return fmt.Errorf("failed to create DM channel: %w", err)
	}

	// Send the message to the created channel.
	_, err = n.session.ChannelMessageSend(channel.ID, message)
	if err != nil {
		return fmt.Errorf("failed to send message to channel: %w", err)
	}

	return nil
}

// Close closes the underlying Discord session.
func (n *DiscordNotifier) Close() error {
	if n.session != nil {
		return n.session.Close()
	}
	return nil
}
