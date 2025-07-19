package alert

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/your-org/obi-scalp-bot/internal/config"
	"go.uber.org/zap"
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

// newTestNotifier creates a notifier with a mocked session for testing.
func newTestNotifier(t *testing.T, cfg config.DiscordConfig) (*DiscordNotifier, *MockDiscordSession) {
	mockSession := new(MockDiscordSession)
	logger := zap.NewNop()

	// Replace the real session with the mock
	notifier, err := NewDiscordNotifier(cfg, logger)
	assert.NoError(t, err)
	// Manually close the real session as it's not needed
	if realSession, ok := notifier.session.(*discordgo.Session); ok {
		realSession.Close()
	}
	notifier.session = mockSession
	return notifier, mockSession
}

func TestNewDiscordNotifier_Unit(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		cfg := config.DiscordConfig{
			BotToken:             "fake-token",
			UserID:               "fake-user-id",
			BufferIntervalMinutes: 1,
		}
		// We can't easily test the goroutine startup here, so we just check creation
		notifier, err := NewDiscordNotifier(cfg, zap.NewNop())
		assert.NoError(t, err)
		assert.NotNil(t, notifier)
		assert.Equal(t, cfg.UserID, notifier.userID)
		assert.NotNil(t, notifier.session)
		assert.Equal(t, time.Minute, notifier.bufferInterval)

	})

	t.Run("missing bot token", func(t *testing.T) {
		cfg := config.DiscordConfig{UserID: "fake-user-id"}
		notifier, err := NewDiscordNotifier(cfg, zap.NewNop())
		assert.Error(t, err)
		assert.Nil(t, notifier)
		assert.EqualError(t, err, "discord bot token and user ID must be configured")
	})
}

func TestDiscordNotifier_Buffering_Unit(t *testing.T) {
	const (
		testUserID    = "test-user-id"
		testChannelID = "test-channel-id"
	)

	cfg := config.DiscordConfig{
		BotToken:             "fake-token",
		UserID:               testUserID,
		BufferIntervalMinutes: 1, // Using a minute, but we will control time manually
	}

	// We need a way to control the ticker, which is tricky.
	// A more advanced approach would be to inject a time provider.
	// For this test, we'll use a short interval and sleep.
	cfg.BufferIntervalMinutes = 0 // use a very small interval for testing

	// Create a new notifier with a very short buffer interval for the test
	notifier, mockSession := newTestNotifier(t, cfg)
	notifier.bufferInterval = 50 * time.Millisecond // Override for testability

	mockChannel := &discordgo.Channel{ID: testChannelID}
	mockMessage := &discordgo.Message{}

	// --- Test Scenario ---
	// 1. Send two messages.
	// 2. Assert they are NOT sent immediately.
	// 3. Wait for the buffer interval to pass.
	// 4. Assert that ONE combined message is sent.

	// Mock the expected Discord calls. This should only be called ONCE.
	mockSession.On("UserChannelCreate", testUserID).Return(mockChannel, nil).Once()
	mockSession.On("ChannelMessageSend", testChannelID, mock.AnythingOfType("string")).
		Run(func(args mock.Arguments) {
			// Check that the content contains both messages
			content := args.String(1)
			assert.Contains(t, content, "message 1")
			assert.Contains(t, content, "message 2")
			assert.True(t, strings.HasPrefix(content, "--- **Error Report"))
		}).
		Return(mockMessage, nil).
		Once()

	// Send messages
	err := notifier.Send("message 1")
	assert.NoError(t, err)
	err = notifier.Send("message 2")
	assert.NoError(t, err)

	// At this point, no message should have been sent
	mockSession.AssertNotCalled(t, "ChannelMessageSend", mock.Anything, mock.Anything)

	// Wait for the buffer interval to elapse
	time.Sleep(100 * time.Millisecond)

	// Now, the message should have been sent
	mockSession.AssertExpectations(t)

	// Close the notifier
	mockSession.On("Close").Return(nil).Once()
	err = notifier.Close()
	assert.NoError(t, err)
}

func TestDiscordNotifier_Close_SendsRemaining_Unit(t *testing.T) {
	const (
		testUserID    = "test-user-id"
		testChannelID = "test-channel-id"
	)

	cfg := config.DiscordConfig{
		BotToken:             "fake-token",
		UserID:               testUserID,
		BufferIntervalMinutes: 60, // A long interval that won't trigger
	}
	notifier, mockSession := newTestNotifier(t, cfg)

	mockChannel := &discordgo.Channel{ID: testChannelID}
	mockMessage := &discordgo.Message{}

	// Mock the expected calls that happen on Close()
	mockSession.On("UserChannelCreate", testUserID).Return(mockChannel, nil).Once()
	mockSession.On("ChannelMessageSend", testChannelID, mock.AnythingOfType("string")).
		Run(func(args mock.Arguments) {
			content := args.String(1)
			assert.Contains(t, content, "final message")
		}).
		Return(mockMessage, nil).
		Once()
	mockSession.On("Close").Return(nil).Once()

	// Send a message
	err := notifier.Send("final message")
	assert.NoError(t, err)

	// Immediately close the notifier
	err = notifier.Close()
	assert.NoError(t, err)
	notifier.wg.Wait() // Wait for the run loop to finish

	// Assert that the message was sent during the close process
	mockSession.AssertExpectations(t)
}

func TestDiscordNotifier_Send_ErrorHandling_Unit(t *testing.T) {
	cfg := config.DiscordConfig{
		BotToken:             "fake-token",
		UserID:               "test-user-id",
		BufferIntervalMinutes: 1,
	}

	t.Run("send on closed notifier", func(t *testing.T) {
		notifier, mockSession := newTestNotifier(t, cfg)

		// Close the notifier first
		mockSession.On("Close").Return(nil).Once()
		err := notifier.Close()
		assert.NoError(t, err)

		// Try to send again, it should return an error
		err = notifier.Send("should fail")
		assert.Error(t, err)
		assert.EqualError(t, err, "notifier is closed")
	})

	t.Run("channel create error on send", func(t *testing.T) {
		notifier, mockSession := newTestNotifier(t, cfg)
		notifier.bufferInterval = 10 * time.Millisecond // short interval

		expectedErr := errors.New("channel create failed")
		mockSession.On("UserChannelCreate", cfg.UserID).Return(nil, expectedErr).Once()

		err := notifier.Send("test")
		assert.NoError(t, err)

		time.Sleep(50 * time.Millisecond) // wait for send attempt

		mockSession.AssertExpectations(t)

		// Close the notifier
		mockSession.On("Close").Return(nil).Once()
		err = notifier.Close()
		assert.NoError(t, err)
	})
}
