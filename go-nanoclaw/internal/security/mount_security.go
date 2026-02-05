// Package security provides mount validation and security checks for NanoClaw containers.
package security

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/nanoclaw/go-nanoclaw/internal/logger"
	"github.com/nanoclaw/go-nanoclaw/internal/types"
)

var log = logger.WithComponent("security")

// DefaultBlockedPatterns are paths that should never be mounted.
var DefaultBlockedPatterns = []string{
	".ssh", ".gnupg", ".gpg", ".aws", ".azure", ".gcloud", ".kube", ".docker",
	"credentials", ".env", ".netrc", ".npmrc", ".pypirc", "id_rsa", "id_ed25519",
	"private_key", ".secret",
}

// AllowedRoot represents an allowed mount root directory.
type AllowedRoot struct {
	Path           string `json:"path"`
	AllowReadWrite bool   `json:"allowReadWrite"`
	Description    string `json:"description,omitempty"`
}

// MountAllowlist defines the mount security policy.
type MountAllowlist struct {
	AllowedRoots    []AllowedRoot `json:"allowedRoots"`
	BlockedPatterns []string      `json:"blockedPatterns"`
	NonMainReadOnly bool          `json:"nonMainReadOnly"`
}

// MountValidator validates container mounts against security policy.
type MountValidator struct {
	allowlist     *MountAllowlist
	allowlistPath string
	mu            sync.RWMutex
}

// NewMountValidator creates a new mount validator.
func NewMountValidator(allowlistPath string) *MountValidator {
	return &MountValidator{
		allowlistPath: allowlistPath,
	}
}

// LoadAllowlist loads or reloads the mount allowlist.
func (v *MountValidator) LoadAllowlist() error {
	v.mu.Lock()
	defer v.mu.Unlock()

	data, err := os.ReadFile(v.allowlistPath)
	if err != nil {
		if os.IsNotExist(err) {
			// Use default allowlist
			v.allowlist = v.defaultAllowlist()
			log.Info().Msg("Using default mount allowlist (no allowlist file found)")
			return nil
		}
		return fmt.Errorf("failed to read allowlist: %w", err)
	}

	var allowlist MountAllowlist
	if err := json.Unmarshal(data, &allowlist); err != nil {
		return fmt.Errorf("failed to parse allowlist: %w", err)
	}

	// Expand and resolve all allowed roots
	for i := range allowlist.AllowedRoots {
		expanded, err := expandPath(allowlist.AllowedRoots[i].Path)
		if err != nil {
			log.Warn().Str("path", allowlist.AllowedRoots[i].Path).Err(err).Msg("Failed to expand path")
			continue
		}
		allowlist.AllowedRoots[i].Path = expanded
	}

	// Use default blocked patterns if none specified
	if len(allowlist.BlockedPatterns) == 0 {
		allowlist.BlockedPatterns = DefaultBlockedPatterns
	}

	v.allowlist = &allowlist
	log.Info().Int("roots", len(allowlist.AllowedRoots)).Msg("Loaded mount allowlist")
	return nil
}

// defaultAllowlist returns the default mount allowlist.
func (v *MountValidator) defaultAllowlist() *MountAllowlist {
	return &MountAllowlist{
		AllowedRoots:    []AllowedRoot{},
		BlockedPatterns: DefaultBlockedPatterns,
		NonMainReadOnly: true,
	}
}

// ValidateMount validates a single mount against the security policy.
func (v *MountValidator) ValidateMount(mount types.AdditionalMount, isMain bool) (*types.VolumeMount, error) {
	v.mu.RLock()
	defer v.mu.RUnlock()

	if v.allowlist == nil {
		return nil, fmt.Errorf("allowlist not loaded")
	}

	// Expand the host path
	hostPath, err := expandPath(mount.HostPath)
	if err != nil {
		return nil, fmt.Errorf("failed to expand path: %w", err)
	}

	// Resolve symlinks
	realPath, err := filepath.EvalSymlinks(hostPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("path does not exist: %s", hostPath)
		}
		return nil, fmt.Errorf("failed to resolve path: %w", err)
	}

	// Check against blocked patterns
	for _, pattern := range v.allowlist.BlockedPatterns {
		if strings.Contains(strings.ToLower(realPath), strings.ToLower(pattern)) {
			return nil, fmt.Errorf("path contains blocked pattern '%s': %s", pattern, realPath)
		}
	}

	// Check if path is under an allowed root
	var matchedRoot *AllowedRoot
	for i := range v.allowlist.AllowedRoots {
		root := &v.allowlist.AllowedRoots[i]
		if strings.HasPrefix(realPath, root.Path) || realPath == root.Path {
			matchedRoot = root
			break
		}
	}

	if matchedRoot == nil {
		return nil, fmt.Errorf("path not under any allowed root: %s", realPath)
	}

	// Determine effective readonly status
	readOnly := mount.ReadOnly
	if !isMain && v.allowlist.NonMainReadOnly {
		readOnly = true
	}
	if !matchedRoot.AllowReadWrite {
		readOnly = true
	}

	return &types.VolumeMount{
		HostPath:      realPath,
		ContainerPath: mount.ContainerPath,
		ReadOnly:      readOnly,
	}, nil
}

// ValidateAdditionalMounts validates all additional mounts for a group.
func (v *MountValidator) ValidateAdditionalMounts(mounts []types.AdditionalMount, groupName string, isMain bool) ([]types.VolumeMount, []error) {
	var validated []types.VolumeMount
	var errors []error

	for _, mount := range mounts {
		vm, err := v.ValidateMount(mount, isMain)
		if err != nil {
			errors = append(errors, fmt.Errorf("mount %s -> %s: %w", mount.HostPath, mount.ContainerPath, err))
			log.Warn().
				Str("group", groupName).
				Str("hostPath", mount.HostPath).
				Err(err).
				Msg("Mount validation failed")
		} else {
			validated = append(validated, *vm)
			log.Debug().
				Str("group", groupName).
				Str("hostPath", vm.HostPath).
				Str("containerPath", vm.ContainerPath).
				Bool("readOnly", vm.ReadOnly).
				Msg("Mount validated")
		}
	}

	return validated, errors
}

// GenerateAllowlistTemplate generates a template allowlist file.
func GenerateAllowlistTemplate() *MountAllowlist {
	homeDir, _ := os.UserHomeDir()
	return &MountAllowlist{
		AllowedRoots: []AllowedRoot{
			{
				Path:           filepath.Join(homeDir, "projects"),
				AllowReadWrite: true,
				Description:    "Development projects directory",
			},
			{
				Path:           filepath.Join(homeDir, "Documents"),
				AllowReadWrite: false,
				Description:    "Documents (read-only)",
			},
		},
		BlockedPatterns: DefaultBlockedPatterns,
		NonMainReadOnly: true,
	}
}

// expandPath expands ~ to home directory.
func expandPath(path string) (string, error) {
	if strings.HasPrefix(path, "~") {
		homeDir, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		path = filepath.Join(homeDir, path[1:])
	}
	return filepath.Clean(path), nil
}
