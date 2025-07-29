//go:build unit
// +build unit

package alert

import (
	"errors"
	"testing"

	"github.com/bwmarrin/discordgo"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/your-org/obi-scalp-bot/internal/config"
)

// MockDiscordSession is a mock for the discordSession interface.
type MockDiscordSession struct {
	mock.Mock
}

func (m *MockDiscordSession) UserChannelCreate(recipientID string, options ...discordgo.RequestOption) (*discordgo.Channel, error) {
	args := m.Called(recipientID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*discordgo.Channel), args.Error(1)
}

func (m *MockDiscordSession) ChannelMessageSend(channelID string, content string, options ...discordgo.RequestOption) (*discordgo.Message, error) {
	args := m.Called(channelID, content)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*discordgo.Message), args.Error(1)
}

func (m *MockDiscordSession) Close() error {
	args := m.Called()
	return args.Error(0)
}

func TestNewDiscordNotifier_Unit(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		cfg := config.DiscordConfig{
			Enabled:  config.FlexBool(true),
			BotToken: "fake-token",
			UserID:   "fake-user-id",
		}
		notifier, err := NewDiscordNotifier(cfg)
		assert.NoError(t, err)
		assert.NotNil(t, notifier)
		assert.Equal(t, cfg.UserID, notifier.userID)
		assert.NotNil(t, notifier.session)

		err = notifier.Close()
		assert.NoError(t, err)
	})

	t.Run("disabled", func(t *testing.T) {
		cfg := config.DiscordConfig{
			Enabled:  config.FlexBool(false),
			BotToken: "fake-token",
			UserID:   "fake-user-id",
		}
		notifier, err := NewDiscordNotifier(cfg)
		assert.NoError(t, err)
		assert.Nil(t, notifier)
	})

	t.Run("missing bot token", func(t *testing.T) {
		cfg := config.DiscordConfig{Enabled: config.FlexBool(true), UserID: "fake-user-id"}
		notifier, err := NewDiscordNotifier(cfg)
		assert.Error(t, err)
		assert.Nil(t, notifier)
		assert.EqualError(t, err, "discord bot token and user ID must be configured")
	})

	t.Run("missing user id", func(t *testing.T) {
		cfg := config.DiscordConfig{Enabled: config.FlexBool(true), BotToken: "fake-token"}
		notifier, err := NewDiscordNotifier(cfg)
		assert.Error(t, err)
		assert.Nil(t, notifier)
		assert.EqualError(t, err, "discord bot token and user ID must be configured")
	})
}

func TestDiscordNotifier_Send_Unit(t *testing.T) {
	const (
		testUserID    = "test-user-id"
		testChannelID = "test-channel-id"
		testMessage   = "hello discord"
	)

	t.Run("success", func(t *testing.T) {
		mockSession := new(MockDiscordSession)
		notifier := &DiscordNotifier{
			session: mockSession,
			userID:  testUserID,
		}

		mockChannel := &discordgo.Channel{ID: testChannelID}
		mockMessage := &discordgo.Message{}

		mockSession.On("UserChannelCreate", testUserID).Return(mockChannel, nil).Once()
		mockSession.On("ChannelMessageSend", testChannelID, testMessage).Return(mockMessage, nil).Once()

		err := notifier.Send(testMessage)

		assert.NoError(t, err)
		mockSession.AssertExpectations(t)
	})

	t.Run("channel create error", func(t *testing.T) {
		mockSession := new(MockDiscordSession)
		notifier := &DiscordNotifier{
			session: mockSession,
			userID:  testUserID,
		}
		expectedErr := errors.New("channel create failed")

		mockSession.On("UserChannelCreate", testUserID).Return(nil, expectedErr).Once()

		err := notifier.Send(testMessage)

		assert.Error(t, err)
		assert.Contains(t, err.Error(), "failed to create DM channel")
		mockSession.AssertExpectations(t)
	})

	t.Run("message send error", func(t *testing.T) {
		mockSession := new(MockDiscordSession)
		notifier := &DiscordNotifier{
			session: mockSession,
			userID:  testUserID,
		}
		mockChannel := &discordgo.Channel{ID: testChannelID}
		expectedErr := errors.New("send failed")

		mockSession.On("UserChannelCreate", testUserID).Return(mockChannel, nil).Once()
		mockSession.On("ChannelMessageSend", testChannelID, testMessage).Return(nil, expectedErr).Once()

		err := notifier.Send(testMessage)

		assert.Error(t, err)
		assert.Contains(t, err.Error(), "failed to send message to channel")
		mockSession.AssertExpectations(t)
	})
}
