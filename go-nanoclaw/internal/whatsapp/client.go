// Package whatsapp provides WhatsApp connectivity using whatsmeow.
package whatsapp

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/store"
	"go.mau.fi/whatsmeow/store/sqlstore"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	waproto "go.mau.fi/whatsmeow/binary/proto"
	waLog "go.mau.fi/whatsmeow/util/log"

	"github.com/nanoclaw/go-nanoclaw/internal/config"
	"github.com/nanoclaw/go-nanoclaw/internal/logger"
	intTypes "github.com/nanoclaw/go-nanoclaw/internal/types"
)

var log = logger.WithComponent("whatsapp")

// MessageHandler is called when a new message is received.
type MessageHandler func(msg *Message)

// ConnectionHandler is called when connection state changes.
type ConnectionHandler func(connected bool)

// Message represents a WhatsApp message.
type Message struct {
	ID         string
	ChatJID    string
	Sender     string
	SenderName string
	Content    string
	Timestamp  time.Time
	IsFromMe   bool
	IsGroup    bool
}

// Client wraps the whatsmeow client.
type Client struct {
	cfg           *config.Config
	client        *whatsmeow.Client
	container     *sqlstore.Container
	onMessage     MessageHandler
	onConnection  ConnectionHandler
	connected     bool
	mu            sync.RWMutex
	lidToPhoneMap map[string]string
}

// NewClient creates a new WhatsApp client.
func NewClient(cfg *config.Config) (*Client, error) {
	// Set up logging
	dbLog := waLog.Noop

	// Create store directory
	storeDir := cfg.AuthDir()
	if err := os.MkdirAll(storeDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create store directory: %w", err)
	}

	// Open SQLite store for credentials
	dbPath := filepath.Join(storeDir, "whatsapp.db")
	container, err := sqlstore.New("sqlite3", "file:"+dbPath+"?_foreign_keys=on", dbLog)
	if err != nil {
		return nil, fmt.Errorf("failed to create store: %w", err)
	}

	// Get or create device store
	deviceStore, err := container.GetFirstDevice()
	if err != nil {
		return nil, fmt.Errorf("failed to get device: %w", err)
	}

	// Create client
	client := whatsmeow.NewClient(deviceStore, dbLog)

	return &Client{
		cfg:           cfg,
		client:        client,
		container:     container,
		lidToPhoneMap: make(map[string]string),
	}, nil
}

// SetMessageHandler sets the message handler.
func (c *Client) SetMessageHandler(handler MessageHandler) {
	c.onMessage = handler
}

// SetConnectionHandler sets the connection handler.
func (c *Client) SetConnectionHandler(handler ConnectionHandler) {
	c.onConnection = handler
}

// Connect connects to WhatsApp.
func (c *Client) Connect(ctx context.Context) error {
	// Add event handler
	c.client.AddEventHandler(c.handleEvent)

	// Check if already logged in
	if c.client.Store.ID == nil {
		return fmt.Errorf("not logged in - run auth first")
	}

	// Connect
	if err := c.client.Connect(); err != nil {
		return fmt.Errorf("failed to connect: %w", err)
	}

	log.Info().Msg("Connected to WhatsApp")
	return nil
}

// Disconnect disconnects from WhatsApp.
func (c *Client) Disconnect() {
	c.client.Disconnect()
	c.setConnected(false)
	log.Info().Msg("Disconnected from WhatsApp")
}

// Close closes the client and releases resources.
func (c *Client) Close() error {
	c.Disconnect()
	return c.container.Close()
}

// IsConnected returns whether the client is connected.
func (c *Client) IsConnected() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.connected
}

func (c *Client) setConnected(connected bool) {
	c.mu.Lock()
	c.connected = connected
	c.mu.Unlock()

	if c.onConnection != nil {
		c.onConnection(connected)
	}
}

// SendMessage sends a text message to a chat.
func (c *Client) SendMessage(ctx context.Context, chatJID, text string) error {
	jid, err := types.ParseJID(chatJID)
	if err != nil {
		return fmt.Errorf("invalid JID: %w", err)
	}

	_, err = c.client.SendMessage(ctx, jid, &waproto.Message{
		Conversation: &text,
	})
	if err != nil {
		return fmt.Errorf("failed to send message: %w", err)
	}

	log.Debug().Str("chatJid", chatJID).Msg("Message sent")
	return nil
}

// GetGroups returns all joined groups.
func (c *Client) GetGroups() ([]*types.GroupInfo, error) {
	groups, err := c.client.GetJoinedGroups()
	if err != nil {
		return nil, fmt.Errorf("failed to get groups: %w", err)
	}
	return groups, nil
}

// GetGroupInfo returns information about a group.
func (c *Client) GetGroupInfo(jid string) (*types.GroupInfo, error) {
	groupJID, err := types.ParseJID(jid)
	if err != nil {
		return nil, fmt.Errorf("invalid JID: %w", err)
	}

	info, err := c.client.GetGroupInfo(groupJID)
	if err != nil {
		return nil, fmt.Errorf("failed to get group info: %w", err)
	}
	return info, nil
}

// GetMyJID returns the client's own JID.
func (c *Client) GetMyJID() string {
	if c.client.Store.ID == nil {
		return ""
	}
	return c.client.Store.ID.String()
}

// GetMyPhoneNumber returns the client's phone number.
func (c *Client) GetMyPhoneNumber() string {
	if c.client.Store.ID == nil {
		return ""
	}
	return c.client.Store.ID.User
}

// BuildLIDMapping builds the LID to phone number mapping.
func (c *Client) BuildLIDMapping() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Get own JID
	if c.client.Store.ID != nil {
		phone := c.client.Store.ID.User
		// LID format varies, but we can build a basic mapping
		c.lidToPhoneMap[c.client.Store.ID.String()] = phone
	}

	log.Debug().Int("mappings", len(c.lidToPhoneMap)).Msg("Built LID to phone mapping")
	return nil
}

// TranslateLID translates a LID JID to phone JID if possible.
func (c *Client) TranslateLID(jid string) string {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if phone, ok := c.lidToPhoneMap[jid]; ok {
		return phone + "@s.whatsapp.net"
	}
	return jid
}

// handleEvent handles WhatsApp events.
func (c *Client) handleEvent(evt interface{}) {
	switch v := evt.(type) {
	case *events.Connected:
		log.Info().Msg("WhatsApp connected")
		c.setConnected(true)
		c.BuildLIDMapping()

	case *events.Disconnected:
		log.Warn().Msg("WhatsApp disconnected")
		c.setConnected(false)

	case *events.LoggedOut:
		log.Warn().Str("reason", v.Reason.String()).Msg("WhatsApp logged out")
		c.setConnected(false)

	case *events.Message:
		c.handleMessage(v)

	case *events.Receipt:
		// Message receipts (delivered, read, etc.)
		log.Debug().Str("type", string(v.Type)).Msg("Receipt received")

	case *events.Presence:
		// Presence updates
		log.Debug().Str("from", v.From.String()).Bool("unavailable", v.Unavailable).Msg("Presence update")

	case *events.HistorySync:
		// History sync events
		log.Debug().Int("conversations", len(v.Data.Conversations)).Msg("History sync received")
	}
}

// handleMessage handles incoming messages.
func (c *Client) handleMessage(evt *events.Message) {
	if c.onMessage == nil {
		return
	}

	// Extract message content
	var content string
	if evt.Message.Conversation != nil {
		content = *evt.Message.Conversation
	} else if evt.Message.ExtendedTextMessage != nil && evt.Message.ExtendedTextMessage.Text != nil {
		content = *evt.Message.ExtendedTextMessage.Text
	} else {
		// Skip non-text messages for now
		return
	}

	// Determine chat JID
	chatJID := evt.Info.Chat.String()

	// Determine sender
	sender := evt.Info.Sender.String()
	if evt.Info.IsFromMe {
		sender = c.GetMyJID()
	}

	// Get sender name
	senderName := evt.Info.PushName
	if senderName == "" {
		senderName = sender
	}

	// Check if it's a group message
	isGroup := evt.Info.IsGroup

	msg := &Message{
		ID:         evt.Info.ID,
		ChatJID:    chatJID,
		Sender:     sender,
		SenderName: senderName,
		Content:    content,
		Timestamp:  evt.Info.Timestamp,
		IsFromMe:   evt.Info.IsFromMe,
		IsGroup:    isGroup,
	}

	// Translate LID if needed (for self-chat)
	if strings.Contains(msg.ChatJID, "@lid") {
		msg.ChatJID = c.TranslateLID(msg.ChatJID)
	}

	c.onMessage(msg)
}

// ToMessageContext converts a Message to MessageContext.
func (m *Message) ToMessageContext() intTypes.MessageContext {
	return intTypes.MessageContext{
		ID:         m.ID,
		ChatJID:    m.ChatJID,
		Sender:     m.Sender,
		SenderName: m.SenderName,
		Content:    m.Content,
		Timestamp:  m.Timestamp,
		IsFromMe:   m.IsFromMe,
	}
}

// DeviceStore provides access to device information.
type DeviceStore struct {
	store *store.Device
}

// NewDeviceStore creates a device store wrapper.
func NewDeviceStore(s *store.Device) *DeviceStore {
	return &DeviceStore{store: s}
}

// IsLoggedIn returns whether the device is logged in.
func (d *DeviceStore) IsLoggedIn() bool {
	return d.store.ID != nil
}
