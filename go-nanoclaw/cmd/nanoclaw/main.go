// Package main is the entry point for NanoClaw.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/nanoclaw/go-nanoclaw/internal/config"
	"github.com/nanoclaw/go-nanoclaw/internal/container"
	"github.com/nanoclaw/go-nanoclaw/internal/db"
	"github.com/nanoclaw/go-nanoclaw/internal/ipc"
	"github.com/nanoclaw/go-nanoclaw/internal/logger"
	"github.com/nanoclaw/go-nanoclaw/internal/scheduler"
	"github.com/nanoclaw/go-nanoclaw/internal/types"
	"github.com/nanoclaw/go-nanoclaw/internal/utils"
	"github.com/nanoclaw/go-nanoclaw/internal/whatsapp"
)

var log = logger.WithComponent("main")

// App represents the NanoClaw application.
type App struct {
	cfg              *config.Config
	db               *db.DB
	wa               *whatsapp.Client
	runner           *container.Runner
	scheduler        *scheduler.Scheduler
	ipcWatcher       *ipc.Watcher
	registeredGroups map[string]*types.RegisteredGroup
	sessions         map[string]string
	lastTimestamp    time.Time
	lastAgentTS      map[string]time.Time
	mu               sync.RWMutex
	ctx              context.Context
	cancel           context.CancelFunc
}

func main() {
	// Initialize configuration
	cfg := config.Load()

	log.Info().
		Str("projectRoot", cfg.ProjectRoot).
		Str("assistantName", cfg.AssistantName).
		Msg("Starting NanoClaw")

	// Check container runtime
	if err := container.CheckContainerRuntime(); err != nil {
		log.Fatal().Err(err).Msg("Container runtime not available")
	}

	// Ensure required directories exist
	ensureDirectories(cfg)

	// Initialize database
	database, err := db.New(cfg.DatabasePath())
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to initialize database")
	}
	defer database.Close()

	// Initialize WhatsApp client
	wa, err := whatsapp.NewClient(cfg)
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to initialize WhatsApp client")
	}
	defer wa.Close()

	// Create app context
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Create app instance
	app := &App{
		cfg:              cfg,
		db:               database,
		wa:               wa,
		runner:           container.NewRunner(cfg),
		registeredGroups: make(map[string]*types.RegisteredGroup),
		sessions:         make(map[string]string),
		lastAgentTS:      make(map[string]time.Time),
		ctx:              ctx,
		cancel:           cancel,
	}

	// Initialize scheduler
	app.scheduler = scheduler.NewScheduler(cfg, database, app.runner)
	app.scheduler.SetGroupLookup(app.getGroupByFolder)
	app.scheduler.SetSessions(app.sessions)

	// Initialize IPC watcher
	app.ipcWatcher = ipc.NewWatcher(cfg, database, app.scheduler)
	app.ipcWatcher.SetMessageHandler(app.handleIPCMessage)
	app.ipcWatcher.SetGroupRegisterHandler(app.handleGroupRegister)
	app.ipcWatcher.SetGroupRefreshHandler(app.handleGroupRefresh)

	// Load state
	if err := app.loadState(); err != nil {
		log.Error().Err(err).Msg("Failed to load state")
	}

	// Set up WhatsApp handlers
	wa.SetMessageHandler(app.handleMessage)
	wa.SetConnectionHandler(app.handleConnection)

	// Connect to WhatsApp
	if err := wa.Connect(ctx); err != nil {
		log.Fatal().Err(err).Msg("Failed to connect to WhatsApp")
	}

	// Wait for connection
	time.Sleep(2 * time.Second)

	// Start background workers
	go app.scheduler.Start(ctx)
	go app.ipcWatcher.Start(ctx)
	go app.messageLoop(ctx)

	// Set up signal handling
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	log.Info().Msg("NanoClaw is running. Press Ctrl+C to stop.")

	// Wait for shutdown signal
	<-sigChan
	log.Info().Msg("Shutting down...")

	// Clean shutdown
	cancel()
	app.scheduler.Stop()
	app.ipcWatcher.Stop()
	app.saveState()

	log.Info().Msg("NanoClaw stopped")
}

// ensureDirectories creates required directories.
func ensureDirectories(cfg *config.Config) {
	dirs := []string{
		cfg.StoreDir,
		cfg.GroupsDir,
		cfg.DataDir,
		cfg.SessionsDir(),
		cfg.IPCDir(),
		cfg.EnvDir(),
		cfg.MainGroupDir(),
		cfg.GlobalDir(),
	}
	for _, dir := range dirs {
		utils.EnsureDir(dir)
	}
}

// loadState loads persisted state from disk.
func (app *App) loadState() error {
	// Load router state
	var routerState types.RouterState
	if err := utils.LoadJSON(app.cfg.RouterStatePath(), &routerState); err != nil {
		return fmt.Errorf("failed to load router state: %w", err)
	}
	if routerState.LastTimestamp != "" {
		app.lastTimestamp, _ = utils.ParseTimestamp(routerState.LastTimestamp)
	}
	if routerState.LastAgentTimestamp != nil {
		for jid, ts := range routerState.LastAgentTimestamp {
			if t, err := utils.ParseTimestamp(ts); err == nil {
				app.lastAgentTS[jid] = t
			}
		}
	}

	// Load sessions
	if err := utils.LoadJSON(app.cfg.SessionsPath(), &app.sessions); err != nil {
		log.Debug().Err(err).Msg("No existing sessions file")
	}

	// Load registered groups
	if err := utils.LoadJSON(app.cfg.RegisteredGroupsPath(), &app.registeredGroups); err != nil {
		log.Debug().Err(err).Msg("No existing registered groups file")
	}

	// Ensure main group exists
	app.ensureMainGroup()

	// Update IPC watcher with registered groups
	app.ipcWatcher.SetRegisteredGroups(app.registeredGroups)

	log.Info().
		Int("groups", len(app.registeredGroups)).
		Int("sessions", len(app.sessions)).
		Time("lastTimestamp", app.lastTimestamp).
		Msg("State loaded")

	return nil
}

// saveState persists state to disk.
func (app *App) saveState() error {
	app.mu.RLock()
	defer app.mu.RUnlock()

	// Save router state
	routerState := types.RouterState{
		LastTimestamp:      utils.FormatTimestamp(app.lastTimestamp),
		LastAgentTimestamp: make(map[string]string),
	}
	for jid, ts := range app.lastAgentTS {
		routerState.LastAgentTimestamp[jid] = utils.FormatTimestamp(ts)
	}
	if err := utils.SaveJSON(app.cfg.RouterStatePath(), routerState); err != nil {
		log.Error().Err(err).Msg("Failed to save router state")
	}

	// Save sessions
	if err := utils.SaveJSON(app.cfg.SessionsPath(), app.sessions); err != nil {
		log.Error().Err(err).Msg("Failed to save sessions")
	}

	// Save registered groups
	if err := utils.SaveJSON(app.cfg.RegisteredGroupsPath(), app.registeredGroups); err != nil {
		log.Error().Err(err).Msg("Failed to save registered groups")
	}

	return nil
}

// ensureMainGroup ensures the main group is registered.
func (app *App) ensureMainGroup() {
	// Get self JID
	myJID := app.wa.GetMyJID()
	if myJID == "" {
		return
	}

	// Convert to phone number format for main group
	phone := app.wa.GetMyPhoneNumber()
	mainJID := phone + "@s.whatsapp.net"

	// Check if main group already registered
	for jid, group := range app.registeredGroups {
		if group.Folder == app.cfg.MainGroupFolder {
			// Already registered
			return
		}
		// If registered with different JID, update
		if jid != mainJID && group.Folder == app.cfg.MainGroupFolder {
			delete(app.registeredGroups, jid)
			break
		}
	}

	// Register main group
	app.registeredGroups[mainJID] = &types.RegisteredGroup{
		Name:    "Main",
		Folder:  app.cfg.MainGroupFolder,
		Trigger: "", // Main group always triggers
		AddedAt: utils.FormatTimestamp(time.Now()),
	}

	// Create main group directory
	utils.EnsureDir(app.cfg.MainGroupDir())

	log.Info().Str("jid", mainJID).Msg("Main group registered")
}

// handleMessage handles incoming WhatsApp messages.
func (app *App) handleMessage(msg *whatsapp.Message) {
	// Store chat metadata
	if err := app.db.StoreChatMetadata(msg.ChatJID, msg.Timestamp, msg.SenderName); err != nil {
		log.Error().Err(err).Str("chatJid", msg.ChatJID).Msg("Failed to store chat metadata")
	}

	// Check if this is a registered group
	group, ok := app.registeredGroups[msg.ChatJID]
	if !ok {
		log.Debug().Str("chatJid", msg.ChatJID).Msg("Message from unregistered chat")
		return
	}

	// Store message
	if err := app.db.StoreMessage(msg.ToMessageContext()); err != nil {
		log.Error().Err(err).Str("chatJid", msg.ChatJID).Msg("Failed to store message")
	}

	// Skip own messages
	if msg.IsFromMe {
		return
	}

	log.Debug().
		Str("chatJid", msg.ChatJID).
		Str("group", group.Folder).
		Str("sender", msg.SenderName).
		Str("content", utils.TruncateString(msg.Content, 50)).
		Msg("Message received")
}

// messageLoop polls for new messages and processes them.
func (app *App) messageLoop(ctx context.Context) {
	ticker := time.NewTicker(app.cfg.PollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			app.processNewMessages(ctx)
		}
	}
}

// processNewMessages processes new messages from registered groups.
func (app *App) processNewMessages(ctx context.Context) {
	app.mu.RLock()
	jids := make([]string, 0, len(app.registeredGroups))
	for jid := range app.registeredGroups {
		jids = append(jids, jid)
	}
	since := app.lastTimestamp
	app.mu.RUnlock()

	// Get new messages
	messages, err := app.db.GetNewMessages(jids, since, app.cfg.AssistantName)
	if err != nil {
		log.Error().Err(err).Msg("Failed to get new messages")
		return
	}

	for _, msg := range messages {
		// Update last timestamp
		msgTime, _ := utils.ParseTimestamp(msg.Timestamp)
		app.mu.Lock()
		if msgTime.After(app.lastTimestamp) {
			app.lastTimestamp = msgTime
		}
		app.mu.Unlock()

		// Check if message should trigger agent
		if app.shouldTrigger(msg) {
			app.processMessage(ctx, msg)
		}
	}
}

// shouldTrigger checks if a message should trigger the agent.
func (app *App) shouldTrigger(msg types.NewMessage) bool {
	group, ok := app.registeredGroups[msg.ChatJID]
	if !ok {
		return false
	}

	// Main group always triggers
	if group.Folder == app.cfg.MainGroupFolder {
		return true
	}

	// Check trigger pattern
	trigger := group.Trigger
	if trigger == "" {
		trigger = "@" + app.cfg.AssistantName
	}

	pattern := regexp.MustCompile(`(?i)^` + regexp.QuoteMeta(trigger) + `\b`)
	return pattern.MatchString(msg.Content)
}

// processMessage processes a single message by running the agent.
func (app *App) processMessage(ctx context.Context, msg types.NewMessage) {
	group, ok := app.registeredGroups[msg.ChatJID]
	if !ok {
		return
	}

	log.Info().
		Str("group", group.Folder).
		Str("sender", msg.SenderName).
		Msg("Processing message")

	// Get message context
	app.mu.RLock()
	contextSince := app.lastAgentTS[msg.ChatJID]
	app.mu.RUnlock()
	if contextSince.IsZero() {
		contextSince = time.Now().Add(-24 * time.Hour) // Default to last 24 hours
	}

	contextMsgs, err := app.db.GetMessagesSince(msg.ChatJID, contextSince, app.cfg.AssistantName, 50)
	if err != nil {
		log.Error().Err(err).Msg("Failed to get message context")
		contextMsgs = nil
	}

	// Build prompt
	prompt := app.buildPrompt(msg, contextMsgs)

	// Build input
	isMain := group.Folder == app.cfg.MainGroupFolder
	input := types.ContainerInput{
		Prompt:      prompt,
		GroupFolder: group.Folder,
		ChatJID:     msg.ChatJID,
		IsMain:      isMain,
	}

	// Use existing session if available
	if sessionID, ok := app.sessions[group.Folder]; ok {
		input.SessionID = sessionID
	}

	// Write tasks snapshot
	allTasks, _ := app.db.GetAllTasks()
	app.runner.WriteTasksSnapshot(group.Folder, isMain, allTasks)

	// Write groups snapshot for main
	if isMain {
		groups := app.buildAvailableGroups()
		app.runner.WriteGroupsSnapshot(group.Folder, isMain, groups)
	}

	// Run agent
	output, err := app.runner.RunAgent(ctx, group, input)
	if err != nil {
		log.Error().Err(err).Msg("Agent execution failed")
		return
	}

	// Update session
	if output.NewSessionID != "" {
		app.mu.Lock()
		app.sessions[group.Folder] = output.NewSessionID
		app.mu.Unlock()
	}

	// Update last agent timestamp
	app.mu.Lock()
	app.lastAgentTS[msg.ChatJID] = time.Now()
	app.mu.Unlock()

	// Send response
	if output.Status == "success" && output.Result != nil && *output.Result != "" {
		if err := app.wa.SendMessage(ctx, msg.ChatJID, *output.Result); err != nil {
			log.Error().Err(err).Msg("Failed to send response")
		}
	} else if output.Status == "error" {
		log.Error().Str("error", output.Error).Msg("Agent returned error")
	}

	// Save state
	app.saveState()
}

// buildPrompt builds the agent prompt from a message and context.
func (app *App) buildPrompt(msg types.NewMessage, context []types.MessageContext) string {
	var sb strings.Builder

	// Add context if available
	if len(context) > 0 {
		sb.WriteString("<recent_messages>\n")
		for _, m := range context {
			role := "user"
			if m.IsFromMe {
				role = "assistant"
			}
			sb.WriteString(fmt.Sprintf("<%s name=\"%s\" time=\"%s\">\n%s\n</%s>\n",
				role, m.SenderName, m.Timestamp.Format(time.RFC3339), m.Content, role))
		}
		sb.WriteString("</recent_messages>\n\n")
	}

	// Add current message
	sb.WriteString(fmt.Sprintf("<current_message from=\"%s\">\n%s\n</current_message>",
		msg.SenderName, msg.Content))

	return sb.String()
}

// buildAvailableGroups builds the list of available groups.
func (app *App) buildAvailableGroups() []types.AvailableGroup {
	chats, err := app.db.GetAllChats()
	if err != nil {
		log.Error().Err(err).Msg("Failed to get chats")
		return nil
	}

	var groups []types.AvailableGroup
	for _, chat := range chats {
		_, isRegistered := app.registeredGroups[chat.JID]
		groups = append(groups, types.AvailableGroup{
			JID:          chat.JID,
			Name:         chat.Name,
			LastActivity: utils.FormatTimestamp(chat.LastMessageTime),
			IsRegistered: isRegistered,
		})
	}
	return groups
}

// getGroupByFolder returns a registered group by folder name.
func (app *App) getGroupByFolder(folder string) *types.RegisteredGroup {
	app.mu.RLock()
	defer app.mu.RUnlock()

	for _, group := range app.registeredGroups {
		if group.Folder == folder {
			return group
		}
	}
	return nil
}

// handleConnection handles WhatsApp connection state changes.
func (app *App) handleConnection(connected bool) {
	if connected {
		log.Info().Msg("WhatsApp connected")
		app.ensureMainGroup()
	} else {
		log.Warn().Msg("WhatsApp disconnected")
	}
}

// handleIPCMessage handles IPC message requests.
func (app *App) handleIPCMessage(chatJID, text, fromGroup string) error {
	// If no specific chat, use the source group's chat
	if chatJID == "" {
		for jid, group := range app.registeredGroups {
			if group.Folder == fromGroup {
				chatJID = jid
				break
			}
		}
	}

	if chatJID == "" {
		return fmt.Errorf("no target chat for message")
	}

	return app.wa.SendMessage(app.ctx, chatJID, text)
}

// handleGroupRegister handles IPC group registration requests.
func (app *App) handleGroupRegister(jid, name, folder, trigger string) error {
	app.mu.Lock()
	defer app.mu.Unlock()

	// Create folder name if not specified
	if folder == "" {
		folder = utils.SafeFileName(strings.ToLower(name))
	}

	// Create trigger if not specified
	if trigger == "" {
		trigger = "@" + app.cfg.AssistantName
	}

	// Register group
	app.registeredGroups[jid] = &types.RegisteredGroup{
		Name:    name,
		Folder:  folder,
		Trigger: trigger,
		AddedAt: utils.FormatTimestamp(time.Now()),
	}

	// Create group directory
	utils.EnsureDir(filepath.Join(app.cfg.GroupsDir, folder))

	// Update IPC watcher
	app.ipcWatcher.SetRegisteredGroups(app.registeredGroups)

	log.Info().Str("jid", jid).Str("name", name).Str("folder", folder).Msg("Group registered")
	return app.saveState()
}

// handleGroupRefresh handles IPC group refresh requests.
func (app *App) handleGroupRefresh() error {
	groups, err := app.wa.GetGroups()
	if err != nil {
		return err
	}

	for _, group := range groups {
		if err := app.db.UpdateChatName(group.JID.String(), group.Name); err != nil {
			log.Error().Err(err).Str("jid", group.JID.String()).Msg("Failed to update group name")
		}
	}

	log.Info().Int("count", len(groups)).Msg("Groups refreshed")
	return nil
}
