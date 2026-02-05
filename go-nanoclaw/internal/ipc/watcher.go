// Package ipc provides inter-process communication handling for NanoClaw.
package ipc

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/nanoclaw/go-nanoclaw/internal/config"
	"github.com/nanoclaw/go-nanoclaw/internal/db"
	"github.com/nanoclaw/go-nanoclaw/internal/logger"
	"github.com/nanoclaw/go-nanoclaw/internal/scheduler"
	"github.com/nanoclaw/go-nanoclaw/internal/types"
	"github.com/nanoclaw/go-nanoclaw/internal/utils"
)

var log = logger.WithComponent("ipc")

// MessageHandler is called when a message IPC is received.
type MessageHandler func(chatJID, text, fromGroup string) error

// GroupRegisterHandler is called when a group registration IPC is received.
type GroupRegisterHandler func(jid, name, folder, trigger string) error

// GroupRefreshHandler is called when a group refresh IPC is received.
type GroupRefreshHandler func() error

// Watcher monitors IPC directories for commands from containers.
type Watcher struct {
	cfg                  *config.Config
	db                   *db.DB
	scheduler            *scheduler.Scheduler
	onMessage            MessageHandler
	onGroupRegister      GroupRegisterHandler
	onGroupRefresh       GroupRefreshHandler
	registeredGroups     map[string]*types.RegisteredGroup
	stopCh               chan struct{}
	running              bool
}

// NewWatcher creates a new IPC watcher.
func NewWatcher(cfg *config.Config, database *db.DB, sched *scheduler.Scheduler) *Watcher {
	return &Watcher{
		cfg:              cfg,
		db:               database,
		scheduler:        sched,
		registeredGroups: make(map[string]*types.RegisteredGroup),
		stopCh:           make(chan struct{}),
	}
}

// SetMessageHandler sets the handler for message IPCs.
func (w *Watcher) SetMessageHandler(handler MessageHandler) {
	w.onMessage = handler
}

// SetGroupRegisterHandler sets the handler for group registration IPCs.
func (w *Watcher) SetGroupRegisterHandler(handler GroupRegisterHandler) {
	w.onGroupRegister = handler
}

// SetGroupRefreshHandler sets the handler for group refresh IPCs.
func (w *Watcher) SetGroupRefreshHandler(handler GroupRefreshHandler) {
	w.onGroupRefresh = handler
}

// SetRegisteredGroups updates the registered groups map.
func (w *Watcher) SetRegisteredGroups(groups map[string]*types.RegisteredGroup) {
	w.registeredGroups = groups
}

// Start starts the IPC watcher loop.
func (w *Watcher) Start(ctx context.Context) {
	if w.running {
		return
	}
	w.running = true

	log.Info().Dur("interval", w.cfg.IPCPollInterval).Msg("Starting IPC watcher")

	ticker := time.NewTicker(w.cfg.IPCPollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			log.Info().Msg("IPC watcher stopping (context cancelled)")
			return
		case <-w.stopCh:
			log.Info().Msg("IPC watcher stopping (stop signal)")
			return
		case <-ticker.C:
			w.processIPCDirectories(ctx)
		}
	}
}

// Stop stops the IPC watcher.
func (w *Watcher) Stop() {
	if w.running {
		close(w.stopCh)
		w.running = false
	}
}

// processIPCDirectories processes all IPC directories for all groups.
func (w *Watcher) processIPCDirectories(ctx context.Context) {
	ipcDir := w.cfg.IPCDir()

	entries, err := os.ReadDir(ipcDir)
	if err != nil {
		if !os.IsNotExist(err) {
			log.Error().Err(err).Msg("Failed to read IPC directory")
		}
		return
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		groupFolder := entry.Name()
		groupIPCDir := filepath.Join(ipcDir, groupFolder)

		// Process messages
		w.processIPCFiles(ctx, filepath.Join(groupIPCDir, "messages"), groupFolder, w.processMessageIPC)

		// Process tasks
		w.processIPCFiles(ctx, filepath.Join(groupIPCDir, "tasks"), groupFolder, w.processTaskIPC)
	}
}

// processIPCFiles processes all files in an IPC subdirectory.
func (w *Watcher) processIPCFiles(ctx context.Context, dir, groupFolder string, processor func(context.Context, string, []byte, string) error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if !os.IsNotExist(err) {
			log.Debug().Str("dir", dir).Err(err).Msg("Failed to read IPC subdirectory")
		}
		return
	}

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}

		filePath := filepath.Join(dir, entry.Name())
		data, err := os.ReadFile(filePath)
		if err != nil {
			log.Error().Str("file", filePath).Err(err).Msg("Failed to read IPC file")
			continue
		}

		if err := processor(ctx, filePath, data, groupFolder); err != nil {
			log.Error().Str("file", filePath).Err(err).Msg("Failed to process IPC file")
			w.moveToErrors(filePath, err)
		} else {
			os.Remove(filePath)
		}
	}
}

// processMessageIPC processes a message IPC file.
func (w *Watcher) processMessageIPC(ctx context.Context, filePath string, data []byte, groupFolder string) error {
	var msg types.IPCMessage
	if err := json.Unmarshal(data, &msg); err != nil {
		return fmt.Errorf("failed to parse message IPC: %w", err)
	}

	// Authorization check
	isMain := groupFolder == w.cfg.MainGroupFolder
	if !isMain {
		// Non-main groups can only send to their own chat
		group := w.findGroupByFolder(groupFolder)
		if group == nil {
			return fmt.Errorf("group not found: %s", groupFolder)
		}
		if msg.ChatJID != "" && !w.isAuthorizedTarget(groupFolder, msg.ChatJID) {
			return fmt.Errorf("unauthorized target chat: %s", msg.ChatJID)
		}
	}

	if w.onMessage == nil {
		return fmt.Errorf("no message handler configured")
	}

	return w.onMessage(msg.ChatJID, msg.Text, groupFolder)
}

// processTaskIPC processes a task IPC file.
func (w *Watcher) processTaskIPC(ctx context.Context, filePath string, data []byte, groupFolder string) error {
	var op types.IPCTaskOperation
	if err := json.Unmarshal(data, &op); err != nil {
		return fmt.Errorf("failed to parse task IPC: %w", err)
	}

	isMain := groupFolder == w.cfg.MainGroupFolder

	switch op.Type {
	case "schedule_task":
		return w.handleScheduleTask(op, groupFolder, isMain)
	case "pause_task":
		return w.handlePauseTask(op, groupFolder, isMain)
	case "resume_task":
		return w.handleResumeTask(op, groupFolder, isMain)
	case "cancel_task":
		return w.handleCancelTask(op, groupFolder, isMain)
	case "register_group":
		return w.handleRegisterGroup(op, isMain)
	case "refresh_groups":
		return w.handleRefreshGroups(isMain)
	default:
		return fmt.Errorf("unknown task operation type: %s", op.Type)
	}
}

// handleScheduleTask handles a schedule_task IPC operation.
func (w *Watcher) handleScheduleTask(op types.IPCTaskOperation, groupFolder string, isMain bool) error {
	// Non-main groups can only schedule tasks for themselves
	targetGroup := op.GroupFolder
	if !isMain && targetGroup != groupFolder {
		return fmt.Errorf("unauthorized: cannot schedule task for other group")
	}

	if targetGroup == "" {
		targetGroup = groupFolder
	}

	chatJID := op.ChatJID
	if chatJID == "" {
		// Use the group's own chat
		group := w.findGroupByFolder(targetGroup)
		if group != nil {
			for jid, g := range w.registeredGroups {
				if g.Folder == targetGroup {
					chatJID = jid
					break
				}
			}
		}
	}

	contextMode := op.ContextMode
	if contextMode == "" {
		contextMode = types.ContextModeIsolated
	}

	_, err := w.scheduler.CreateTask(
		targetGroup,
		chatJID,
		op.Prompt,
		op.ScheduleType,
		op.ScheduleValue,
		contextMode,
	)
	return err
}

// handlePauseTask handles a pause_task IPC operation.
func (w *Watcher) handlePauseTask(op types.IPCTaskOperation, groupFolder string, isMain bool) error {
	if !isMain {
		// Check authorization
		task, err := w.db.GetTask(op.TaskID)
		if err != nil || task == nil {
			return fmt.Errorf("task not found: %s", op.TaskID)
		}
		if task.GroupFolder != groupFolder {
			return fmt.Errorf("unauthorized: cannot pause other group's task")
		}
	}
	return w.scheduler.PauseTask(op.TaskID)
}

// handleResumeTask handles a resume_task IPC operation.
func (w *Watcher) handleResumeTask(op types.IPCTaskOperation, groupFolder string, isMain bool) error {
	if !isMain {
		task, err := w.db.GetTask(op.TaskID)
		if err != nil || task == nil {
			return fmt.Errorf("task not found: %s", op.TaskID)
		}
		if task.GroupFolder != groupFolder {
			return fmt.Errorf("unauthorized: cannot resume other group's task")
		}
	}
	return w.scheduler.ResumeTask(op.TaskID)
}

// handleCancelTask handles a cancel_task IPC operation.
func (w *Watcher) handleCancelTask(op types.IPCTaskOperation, groupFolder string, isMain bool) error {
	if !isMain {
		task, err := w.db.GetTask(op.TaskID)
		if err != nil || task == nil {
			return fmt.Errorf("task not found: %s", op.TaskID)
		}
		if task.GroupFolder != groupFolder {
			return fmt.Errorf("unauthorized: cannot cancel other group's task")
		}
	}
	return w.scheduler.CancelTask(op.TaskID)
}

// handleRegisterGroup handles a register_group IPC operation (main only).
func (w *Watcher) handleRegisterGroup(op types.IPCTaskOperation, isMain bool) error {
	if !isMain {
		return fmt.Errorf("unauthorized: only main group can register groups")
	}

	if w.onGroupRegister == nil {
		return fmt.Errorf("no group register handler configured")
	}

	// Extract fields from the operation
	// Note: We're reusing IPCTaskOperation; in practice you might want a separate type
	jid := op.ChatJID
	name := op.Prompt // Reusing prompt field for name
	folder := op.GroupFolder
	trigger := op.ScheduleValue // Reusing scheduleValue for trigger

	return w.onGroupRegister(jid, name, folder, trigger)
}

// handleRefreshGroups handles a refresh_groups IPC operation (main only).
func (w *Watcher) handleRefreshGroups(isMain bool) error {
	if !isMain {
		return fmt.Errorf("unauthorized: only main group can refresh groups")
	}

	if w.onGroupRefresh == nil {
		return fmt.Errorf("no group refresh handler configured")
	}

	return w.onGroupRefresh()
}

// findGroupByFolder finds a registered group by folder name.
func (w *Watcher) findGroupByFolder(folder string) *types.RegisteredGroup {
	for _, group := range w.registeredGroups {
		if group.Folder == folder {
			return group
		}
	}
	return nil
}

// isAuthorizedTarget checks if a group can send to a target chat.
func (w *Watcher) isAuthorizedTarget(sourceFolder, targetJID string) bool {
	// Find the source group's JID
	var sourceJID string
	for jid, group := range w.registeredGroups {
		if group.Folder == sourceFolder {
			sourceJID = jid
			break
		}
	}

	// Can send to own chat
	if sourceJID == targetJID {
		return true
	}

	// Can send to main group
	for jid, group := range w.registeredGroups {
		if group.Folder == w.cfg.MainGroupFolder && jid == targetJID {
			return true
		}
	}

	return false
}

// moveToErrors moves a failed IPC file to the errors directory.
func (w *Watcher) moveToErrors(filePath string, err error) {
	errorsDir := filepath.Join(w.cfg.IPCDir(), "errors")
	utils.EnsureDir(errorsDir)

	// Add error info to filename
	base := filepath.Base(filePath)
	errorFile := filepath.Join(errorsDir, fmt.Sprintf("%s.error", base))

	// Write error info
	errorInfo := fmt.Sprintf("Error: %v\nOriginal file: %s\nTime: %s\n", err, filePath, time.Now().Format(time.RFC3339))
	os.WriteFile(errorFile+".info", []byte(errorInfo), 0644)

	// Move the original file
	utils.MoveFile(filePath, errorFile)
}
