// Package config provides configuration management for NanoClaw.
package config

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"time"
)

// Config holds all application configuration.
type Config struct {
	// Assistant configuration
	AssistantName  string
	TriggerPattern *regexp.Regexp

	// Polling intervals
	PollInterval          time.Duration
	SchedulerPollInterval time.Duration
	IPCPollInterval       time.Duration

	// Container settings
	ContainerImage         string
	ContainerTimeout       time.Duration
	ContainerMaxOutputSize int64

	// Paths
	ProjectRoot        string
	StoreDir           string
	GroupsDir          string
	DataDir            string
	MainGroupFolder    string
	MountAllowlistPath string

	// Timezone
	Timezone string
}

// DefaultConfig returns a configuration with default values.
func DefaultConfig() *Config {
	projectRoot, _ := os.Getwd()
	homeDir, _ := os.UserHomeDir()

	assistantName := getEnvOrDefault("ASSISTANT_NAME", "Andy")
	triggerPattern := regexp.MustCompile(`(?i)^@` + regexp.QuoteMeta(assistantName) + `\b`)

	return &Config{
		AssistantName:  assistantName,
		TriggerPattern: triggerPattern,

		PollInterval:          getDurationEnv("POLL_INTERVAL", 2*time.Second),
		SchedulerPollInterval: getDurationEnv("SCHEDULER_POLL_INTERVAL", 60*time.Second),
		IPCPollInterval:       getDurationEnv("IPC_POLL_INTERVAL", 1*time.Second),

		ContainerImage:         getEnvOrDefault("CONTAINER_IMAGE", "nanoclaw-agent:latest"),
		ContainerTimeout:       getDurationEnv("CONTAINER_TIMEOUT", 5*time.Minute),
		ContainerMaxOutputSize: getInt64Env("CONTAINER_MAX_OUTPUT_SIZE", 10*1024*1024), // 10MB

		ProjectRoot:        projectRoot,
		StoreDir:           filepath.Join(projectRoot, "store"),
		GroupsDir:          filepath.Join(projectRoot, "groups"),
		DataDir:            filepath.Join(projectRoot, "data"),
		MainGroupFolder:    "main",
		MountAllowlistPath: filepath.Join(homeDir, ".config", "nanoclaw", "mount-allowlist.json"),

		Timezone: getEnvOrDefault("TZ", "UTC"),
	}
}

// Load loads configuration, applying environment variable overrides.
func Load() *Config {
	return DefaultConfig()
}

// AuthDir returns the path to the WhatsApp auth directory.
func (c *Config) AuthDir() string {
	return filepath.Join(c.StoreDir, "auth")
}

// SessionsDir returns the path to the sessions directory.
func (c *Config) SessionsDir() string {
	return filepath.Join(c.DataDir, "sessions")
}

// IPCDir returns the path to the IPC directory.
func (c *Config) IPCDir() string {
	return filepath.Join(c.DataDir, "ipc")
}

// EnvDir returns the path to the env directory.
func (c *Config) EnvDir() string {
	return filepath.Join(c.DataDir, "env")
}

// DatabasePath returns the path to the SQLite database.
func (c *Config) DatabasePath() string {
	return filepath.Join(c.DataDir, "messages.db")
}

// RouterStatePath returns the path to the router state file.
func (c *Config) RouterStatePath() string {
	return filepath.Join(c.DataDir, "router_state.json")
}

// SessionsPath returns the path to the sessions file.
func (c *Config) SessionsPath() string {
	return filepath.Join(c.DataDir, "sessions.json")
}

// RegisteredGroupsPath returns the path to the registered groups file.
func (c *Config) RegisteredGroupsPath() string {
	return filepath.Join(c.DataDir, "registered_groups.json")
}

// GroupDir returns the path to a specific group's directory.
func (c *Config) GroupDir(folder string) string {
	return filepath.Join(c.GroupsDir, folder)
}

// GroupIPCDir returns the path to a specific group's IPC directory.
func (c *Config) GroupIPCDir(folder string) string {
	return filepath.Join(c.IPCDir(), folder)
}

// GroupSessionDir returns the path to a specific group's session directory.
func (c *Config) GroupSessionDir(folder string) string {
	return filepath.Join(c.SessionsDir(), folder)
}

// GlobalDir returns the path to the global memory directory.
func (c *Config) GlobalDir() string {
	return filepath.Join(c.GroupsDir, "global")
}

// MainGroupDir returns the path to the main group directory.
func (c *Config) MainGroupDir() string {
	return filepath.Join(c.GroupsDir, c.MainGroupFolder)
}

// getEnvOrDefault returns the environment variable value or a default.
func getEnvOrDefault(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

// getDurationEnv returns a duration from environment variable (in milliseconds) or default.
func getDurationEnv(key string, defaultValue time.Duration) time.Duration {
	if value := os.Getenv(key); value != "" {
		if ms, err := strconv.ParseInt(value, 10, 64); err == nil {
			return time.Duration(ms) * time.Millisecond
		}
	}
	return defaultValue
}

// getInt64Env returns an int64 from environment variable or default.
func getInt64Env(key string, defaultValue int64) int64 {
	if value := os.Getenv(key); value != "" {
		if n, err := strconv.ParseInt(value, 10, 64); err == nil {
			return n
		}
	}
	return defaultValue
}
