// Package utils provides utility functions for NanoClaw.
package utils

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// LoadJSON loads JSON from a file into the given interface.
func LoadJSON(path string, v interface{}) error {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // Return nil for missing files
		}
		return err
	}
	return json.Unmarshal(data, v)
}

// SaveJSON saves the given interface as JSON to a file.
func SaveJSON(path string, v interface{}) error {
	// Ensure directory exists
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create directory: %w", err)
	}

	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}

	// Atomic write: write to temp file then rename
	tmpPath := path + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0644); err != nil {
		return err
	}

	return os.Rename(tmpPath, path)
}

// EnsureDir ensures a directory exists.
func EnsureDir(path string) error {
	return os.MkdirAll(path, 0755)
}

// FileExists checks if a file exists.
func FileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// DirExists checks if a directory exists.
func DirExists(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return info.IsDir()
}

// GenerateID generates a unique ID with timestamp prefix.
func GenerateID(prefix string) string {
	timestamp := time.Now().UnixMilli()
	randomBytes := make([]byte, 4)
	rand.Read(randomBytes)
	return fmt.Sprintf("%s-%d-%s", prefix, timestamp, hex.EncodeToString(randomBytes))
}

// TruncateString truncates a string to the given length.
func TruncateString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}

// ParseTimestamp parses an RFC3339 timestamp.
func ParseTimestamp(s string) (time.Time, error) {
	return time.Parse(time.RFC3339, s)
}

// FormatTimestamp formats a time as RFC3339.
func FormatTimestamp(t time.Time) string {
	return t.Format(time.RFC3339)
}

// SafeFileName converts a string to a safe filename.
func SafeFileName(s string) string {
	// Replace unsafe characters
	safe := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '-' || c == '_' || c == '.' {
			safe = append(safe, c)
		} else if c == ' ' {
			safe = append(safe, '-')
		}
	}
	return string(safe)
}

// CleanupOldFiles removes files older than the given duration from a directory.
func CleanupOldFiles(dir string, maxAge time.Duration) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	cutoff := time.Now().Add(-maxAge)
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		if info.ModTime().Before(cutoff) {
			os.Remove(filepath.Join(dir, entry.Name()))
		}
	}
	return nil
}

// CopyFile copies a file from src to dst.
func CopyFile(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, data, 0644)
}

// MoveFile moves a file from src to dst.
func MoveFile(src, dst string) error {
	// Try rename first (atomic if on same filesystem)
	if err := os.Rename(src, dst); err == nil {
		return nil
	}

	// Fall back to copy and delete
	if err := CopyFile(src, dst); err != nil {
		return err
	}
	return os.Remove(src)
}
