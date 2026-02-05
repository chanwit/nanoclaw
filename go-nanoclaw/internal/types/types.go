// Package types provides shared type definitions for NanoClaw.
package types

import (
	"time"
)

// AdditionalMount represents a user-configured mount for a container.
type AdditionalMount struct {
	HostPath      string `json:"hostPath"`
	ContainerPath string `json:"containerPath"`
	ReadOnly      bool   `json:"readonly,omitempty"`
}

// ContainerConfig holds container-specific configuration for a group.
type ContainerConfig struct {
	AdditionalMounts []AdditionalMount `json:"additionalMounts,omitempty"`
	Timeout          int64             `json:"timeout,omitempty"` // milliseconds
	Env              map[string]string `json:"env,omitempty"`
}

// RegisteredGroup represents a registered WhatsApp group.
type RegisteredGroup struct {
	Name            string           `json:"name"`
	Folder          string           `json:"folder"`
	Trigger         string           `json:"trigger"`
	AddedAt         string           `json:"added_at"`
	ContainerConfig *ContainerConfig `json:"containerConfig,omitempty"`
}

// NewMessage represents a message retrieved from the database.
type NewMessage struct {
	ID         string `json:"id"`
	ChatJID    string `json:"chat_jid"`
	Sender     string `json:"sender"`
	SenderName string `json:"sender_name"`
	Content    string `json:"content"`
	Timestamp  string `json:"timestamp"`
}

// ScheduleType represents the type of schedule for a task.
type ScheduleType string

const (
	ScheduleTypeCron     ScheduleType = "cron"
	ScheduleTypeInterval ScheduleType = "interval"
	ScheduleTypeOnce     ScheduleType = "once"
)

// ContextMode represents the context mode for task execution.
type ContextMode string

const (
	ContextModeGroup    ContextMode = "group"
	ContextModeIsolated ContextMode = "isolated"
)

// TaskStatus represents the status of a scheduled task.
type TaskStatus string

const (
	TaskStatusActive    TaskStatus = "active"
	TaskStatusPaused    TaskStatus = "paused"
	TaskStatusCompleted TaskStatus = "completed"
)

// ScheduledTask represents a scheduled task in the database.
type ScheduledTask struct {
	ID            string       `json:"id"`
	GroupFolder   string       `json:"group_folder"`
	ChatJID       string       `json:"chat_jid"`
	Prompt        string       `json:"prompt"`
	ScheduleType  ScheduleType `json:"schedule_type"`
	ScheduleValue string       `json:"schedule_value"`
	ContextMode   ContextMode  `json:"context_mode"`
	NextRun       *string      `json:"next_run"`
	LastRun       *string      `json:"last_run"`
	LastResult    *string      `json:"last_result"`
	Status        TaskStatus   `json:"status"`
	CreatedAt     string       `json:"created_at"`
}

// TaskRunLog represents a task execution log entry.
type TaskRunLog struct {
	ID         int64   `json:"id"`
	TaskID     string  `json:"task_id"`
	RunAt      string  `json:"run_at"`
	DurationMs int64   `json:"duration_ms"`
	Status     string  `json:"status"` // "success" or "error"
	Result     *string `json:"result"`
	Error      *string `json:"error"`
}

// ContainerInput represents the input sent to the container agent.
type ContainerInput struct {
	Prompt          string `json:"prompt"`
	SessionID       string `json:"sessionId,omitempty"`
	GroupFolder     string `json:"groupFolder"`
	ChatJID         string `json:"chatJid"`
	IsMain          bool   `json:"isMain"`
	IsScheduledTask bool   `json:"isScheduledTask,omitempty"`
}

// ContainerOutput represents the output received from the container agent.
type ContainerOutput struct {
	Status       string  `json:"status"` // "success" or "error"
	Result       *string `json:"result"`
	NewSessionID string  `json:"newSessionId,omitempty"`
	Error        string  `json:"error,omitempty"`
}

// VolumeMount represents a container volume mount.
type VolumeMount struct {
	HostPath      string
	ContainerPath string
	ReadOnly      bool
}

// AvailableGroup represents a group available for registration.
type AvailableGroup struct {
	JID          string `json:"jid"`
	Name         string `json:"name"`
	LastActivity string `json:"lastActivity"`
	IsRegistered bool   `json:"isRegistered"`
}

// IPCMessage represents an IPC message from the container.
type IPCMessage struct {
	Type        string `json:"type"`
	ChatJID     string `json:"chatJid,omitempty"`
	Text        string `json:"text,omitempty"`
	GroupFolder string `json:"groupFolder"`
	Timestamp   string `json:"timestamp"`
}

// IPCTaskOperation represents an IPC task operation from the container.
type IPCTaskOperation struct {
	Type          string       `json:"type"`
	TaskID        string       `json:"taskId,omitempty"`
	GroupFolder   string       `json:"groupFolder"`
	ChatJID       string       `json:"chatJid,omitempty"`
	Prompt        string       `json:"prompt,omitempty"`
	ScheduleType  ScheduleType `json:"scheduleType,omitempty"`
	ScheduleValue string       `json:"scheduleValue,omitempty"`
	ContextMode   ContextMode  `json:"contextMode,omitempty"`
	Timestamp     string       `json:"timestamp"`
}

// RouterState represents the persisted router state.
type RouterState struct {
	LastTimestamp      string            `json:"last_timestamp"`
	LastAgentTimestamp map[string]string `json:"last_agent_timestamp"`
}

// ChatMetadata represents metadata about a WhatsApp chat.
type ChatMetadata struct {
	JID             string
	Name            string
	LastMessageTime time.Time
}

// MessageContext represents a message with context for the agent.
type MessageContext struct {
	ID         string
	ChatJID    string
	Sender     string
	SenderName string
	Content    string
	Timestamp  time.Time
	IsFromMe   bool
}
