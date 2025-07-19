// Package alert handles sending notifications to various channels.
package alert

import (
	"bytes"
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/your-org/obi-scalp-bot/internal/config"
	"go.uber.org/zap"
)

// Notifier is the interface for sending alert messages.
type Notifier interface {
	Send(message string) error
	Close() error
}

// discordSession is an interface abstracting the discordgo.Session for testability.
type discordSession interface {
	UserChannelCreate(recipientID string, options ...discordgo.RequestOption) (*discordgo.Channel, error)
	ChannelMessageSend(channelID string, content string, options ...discordgo.RequestOption) (*discordgo.Message, error)
	Close() error
}

// DiscordNotifier sends messages to a Discord user via DM, buffering them to avoid spam.
type DiscordNotifier struct {
	session         discordSession
	userID          string
	messageQueue    chan string
	ctx             context.Context
	cancel          context.CancelFunc
	wg              sync.WaitGroup
	bufferInterval  time.Duration
	logger          *zap.Logger
}

// NewDiscordNotifier creates a new DiscordNotifier with message buffering.
func NewDiscordNotifier(cfg config.DiscordConfig, logger *zap.Logger) (*DiscordNotifier, error) {
	if cfg.BotToken == "" || cfg.UserID == "" {
		return nil, fmt.Errorf("discord bot token and user ID must be configured")
	}

	dg, err := discordgo.New("Bot " + cfg.BotToken)
	if err != nil {
		return nil, fmt.Errorf("failed to create discord session: %w", err)
	}

	ctx, cancel := context.WithCancel(context.Background())

	notifier := &DiscordNotifier{
		session:         dg,
		userID:          cfg.UserID,
		messageQueue:    make(chan string, 100), // Buffered channel
		ctx:             ctx,
		cancel:          cancel,
		bufferInterval:  time.Duration(cfg.BufferIntervalMinutes) * time.Minute,
		logger:          logger.Named("discord_notifier"),
	}

	notifier.wg.Add(1)
	go notifier.run()

	return notifier, nil
}

// run is the background worker that collects and sends messages.
func (n *DiscordNotifier) run() {
	defer n.wg.Done()
	ticker := time.NewTicker(n.bufferInterval)
	defer ticker.Stop()

	var messages []string

	for {
		select {
		case <-n.ctx.Done():
			// Context is cancelled, meaning Close() was called.
			// We need to drain the messageQueue one last time.
			// We stop the ticker and loop until the queue is empty.
			ticker.Stop()
			for {
				select {
				case msg := <-n.messageQueue:
					messages = append(messages, msg)
				default:
					// Queue is empty, send the final batch and exit.
					if len(messages) > 0 {
						n.sendBatch(messages)
					}
					return
				}
			}
		case msg := <-n.messageQueue:
			messages = append(messages, msg)
		case <-ticker.C:
			if len(messages) > 0 {
				n.sendBatch(messages)
				messages = nil // Reset buffer
			}
		}
	}
}

// sendBatch sends a batch of collected messages to Discord.
func (n *DiscordNotifier) sendBatch(messages []string) {
	if len(messages) == 0 {
		return
	}

	// Create a single formatted message
	var builder bytes.Buffer
	builder.WriteString(fmt.Sprintf("--- **Error Report (%s)** ---\n", time.Now().Format(time.RFC1123)))
	for _, msg := range messages {
		builder.WriteString(fmt.Sprintf("- %s\n", msg))
	}
	// Discord has a 2000 character limit per message. Truncate if necessary.
	fullMessage := builder.String()
	if len(fullMessage) > 2000 {
		fullMessage = fullMessage[:1990] + "... (truncated)"
	}

	channel, err := n.session.UserChannelCreate(n.userID)
	if err != nil {
		n.logger.Error("Failed to create DM channel", zap.Error(err))
		return
	}

	if _, err := n.session.ChannelMessageSend(channel.ID, fullMessage); err != nil {
		n.logger.Error("Failed to send message to channel", zap.Error(err))
	}
}


// Send queues a message to be sent in the next batch.
func (n *DiscordNotifier) Send(message string) error {
	select {
	case n.messageQueue <- message:
		return nil
	case <-n.ctx.Done():
		return fmt.Errorf("notifier is closed")
	default:
		return fmt.Errorf("message queue is full, dropping message")
	}
}

// Close gracefully shuts down the notifier, sending any buffered messages.
func (n *DiscordNotifier) Close() error {
	n.cancel()    // Signal the run loop to stop
	n.wg.Wait()   // Wait for the run loop to finish processing messages

	// Close the underlying session
	if n.session != nil {
		return n.session.Close()
	}
	return nil
}
