// Package container provides container execution for NanoClaw agents.
package container

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/nanoclaw/go-nanoclaw/internal/config"
	"github.com/nanoclaw/go-nanoclaw/internal/logger"
	"github.com/nanoclaw/go-nanoclaw/internal/security"
	"github.com/nanoclaw/go-nanoclaw/internal/types"
	"github.com/nanoclaw/go-nanoclaw/internal/utils"
)

var log = logger.WithComponent("container")

// Output markers for parsing container output.
const (
	OutputStartMarker = "---NANOCLAW_OUTPUT_START---"
	OutputEndMarker   = "---NANOCLAW_OUTPUT_END---"
)

// Runner manages container execution.
type Runner struct {
	cfg       *config.Config
	validator *security.MountValidator
}

// NewRunner creates a new container runner.
func NewRunner(cfg *config.Config) *Runner {
	validator := security.NewMountValidator(cfg.MountAllowlistPath)
	validator.LoadAllowlist()

	return &Runner{
		cfg:       cfg,
		validator: validator,
	}
}

// RunAgent runs the container agent for a group.
func (r *Runner) RunAgent(ctx context.Context, group *types.RegisteredGroup, input types.ContainerInput) (*types.ContainerOutput, error) {
	isMain := group.Folder == r.cfg.MainGroupFolder

	// Build volume mounts
	mounts := r.buildVolumeMounts(group, isMain)

	// Add additional mounts if configured
	if group.ContainerConfig != nil && len(group.ContainerConfig.AdditionalMounts) > 0 {
		validated, errors := r.validator.ValidateAdditionalMounts(
			group.ContainerConfig.AdditionalMounts,
			group.Name,
			isMain,
		)
		if len(errors) > 0 {
			log.Warn().Int("errorCount", len(errors)).Msg("Some additional mounts failed validation")
		}
		mounts = append(mounts, validated...)
	}

	// Build container arguments
	args := r.buildContainerArgs(mounts)

	// Determine timeout
	timeout := r.cfg.ContainerTimeout
	if group.ContainerConfig != nil && group.ContainerConfig.Timeout > 0 {
		timeout = time.Duration(group.ContainerConfig.Timeout) * time.Millisecond
	}

	// Create context with timeout
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// Serialize input
	inputJSON, err := json.Marshal(input)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal input: %w", err)
	}

	// Run container
	cmd := exec.CommandContext(ctx, "container", args...)
	cmd.Stdin = bytes.NewReader(inputJSON)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	startTime := time.Now()
	log.Info().
		Str("group", group.Folder).
		Str("chatJid", input.ChatJID).
		Bool("isMain", isMain).
		Msg("Starting container agent")

	err = cmd.Run()
	duration := time.Since(startTime)

	// Log container execution
	r.writeContainerLog(group.Folder, startTime, &stdout, &stderr)

	if ctx.Err() == context.DeadlineExceeded {
		return &types.ContainerOutput{
			Status: "error",
			Error:  "container execution timed out",
		}, nil
	}

	if err != nil {
		log.Error().
			Err(err).
			Str("stderr", stderr.String()).
			Dur("duration", duration).
			Msg("Container execution failed")
		return &types.ContainerOutput{
			Status: "error",
			Error:  fmt.Sprintf("container execution failed: %v", err),
		}, nil
	}

	// Parse output
	output, err := r.parseOutput(stdout.String())
	if err != nil {
		log.Error().
			Err(err).
			Str("stdout", stdout.String()).
			Msg("Failed to parse container output")
		return &types.ContainerOutput{
			Status: "error",
			Error:  fmt.Sprintf("failed to parse output: %v", err),
		}, nil
	}

	log.Info().
		Str("group", group.Folder).
		Str("status", output.Status).
		Dur("duration", duration).
		Msg("Container agent completed")

	return output, nil
}

// buildVolumeMounts builds the volume mounts for a container.
func (r *Runner) buildVolumeMounts(group *types.RegisteredGroup, isMain bool) []types.VolumeMount {
	var mounts []types.VolumeMount

	groupDir := r.cfg.GroupDir(group.Folder)
	sessionDir := r.cfg.GroupSessionDir(group.Folder)
	ipcDir := r.cfg.GroupIPCDir(group.Folder)

	// Ensure directories exist
	utils.EnsureDir(groupDir)
	utils.EnsureDir(sessionDir)
	utils.EnsureDir(ipcDir)
	utils.EnsureDir(filepath.Join(ipcDir, "messages"))
	utils.EnsureDir(filepath.Join(ipcDir, "tasks"))

	if isMain {
		// Main group gets access to entire project
		mounts = append(mounts, types.VolumeMount{
			HostPath:      r.cfg.ProjectRoot,
			ContainerPath: "/workspace/project",
			ReadOnly:      false,
		})
	}

	// Group's own directory (read-write)
	mounts = append(mounts, types.VolumeMount{
		HostPath:      groupDir,
		ContainerPath: "/workspace/group",
		ReadOnly:      false,
	})

	// Global memory (read-only for non-main)
	mounts = append(mounts, types.VolumeMount{
		HostPath:      r.cfg.GlobalDir(),
		ContainerPath: "/workspace/global",
		ReadOnly:      !isMain,
	})

	// Session directory
	mounts = append(mounts, types.VolumeMount{
		HostPath:      sessionDir,
		ContainerPath: "/home/node/.claude",
		ReadOnly:      false,
	})

	// IPC directory
	mounts = append(mounts, types.VolumeMount{
		HostPath:      ipcDir,
		ContainerPath: "/workspace/ipc",
		ReadOnly:      false,
	})

	// Env directory (read-only)
	envDir := r.cfg.EnvDir()
	if utils.DirExists(envDir) {
		mounts = append(mounts, types.VolumeMount{
			HostPath:      envDir,
			ContainerPath: "/workspace/env-dir",
			ReadOnly:      true,
		})
	}

	return mounts
}

// buildContainerArgs builds the container command arguments.
func (r *Runner) buildContainerArgs(mounts []types.VolumeMount) []string {
	args := []string{"run", "-i", "--rm"}

	for _, mount := range mounts {
		mountArg := fmt.Sprintf("%s:%s", mount.HostPath, mount.ContainerPath)
		if mount.ReadOnly {
			mountArg += ":ro"
		}
		args = append(args, "-v", mountArg)
	}

	args = append(args, r.cfg.ContainerImage)
	return args
}

// parseOutput parses the container output.
func (r *Runner) parseOutput(stdout string) (*types.ContainerOutput, error) {
	// Try to find output between markers
	startIdx := strings.Index(stdout, OutputStartMarker)
	endIdx := strings.Index(stdout, OutputEndMarker)

	var jsonStr string
	if startIdx != -1 && endIdx != -1 && endIdx > startIdx {
		jsonStr = strings.TrimSpace(stdout[startIdx+len(OutputStartMarker) : endIdx])
	} else {
		// Fallback: try to find JSON in the last line
		lines := strings.Split(strings.TrimSpace(stdout), "\n")
		if len(lines) > 0 {
			jsonStr = lines[len(lines)-1]
		}
	}

	if jsonStr == "" {
		return nil, fmt.Errorf("no output found")
	}

	var output types.ContainerOutput
	if err := json.Unmarshal([]byte(jsonStr), &output); err != nil {
		return nil, fmt.Errorf("failed to parse JSON: %w", err)
	}

	return &output, nil
}

// writeContainerLog writes the container execution log.
func (r *Runner) writeContainerLog(groupFolder string, startTime time.Time, stdout, stderr *bytes.Buffer) {
	logDir := filepath.Join(r.cfg.GroupDir(groupFolder), "logs")
	utils.EnsureDir(logDir)

	logFile := filepath.Join(logDir, fmt.Sprintf("container-%s.log", startTime.Format("2006-01-02T15-04-05")))

	var content strings.Builder
	content.WriteString(fmt.Sprintf("=== Container Log - %s ===\n\n", startTime.Format(time.RFC3339)))
	content.WriteString("=== STDOUT ===\n")
	content.WriteString(stdout.String())
	content.WriteString("\n\n=== STDERR ===\n")
	content.WriteString(stderr.String())

	os.WriteFile(logFile, []byte(content.String()), 0644)
}

// WriteTasksSnapshot writes the current tasks snapshot for the container.
func (r *Runner) WriteTasksSnapshot(groupFolder string, isMain bool, tasks []types.ScheduledTask) error {
	ipcDir := r.cfg.GroupIPCDir(groupFolder)

	// Filter tasks for non-main groups
	var filteredTasks []types.ScheduledTask
	for _, task := range tasks {
		if isMain || task.GroupFolder == groupFolder {
			filteredTasks = append(filteredTasks, task)
		}
	}

	return utils.SaveJSON(filepath.Join(ipcDir, "current_tasks.json"), filteredTasks)
}

// WriteGroupsSnapshot writes the available groups snapshot for the container.
func (r *Runner) WriteGroupsSnapshot(groupFolder string, isMain bool, groups []types.AvailableGroup) error {
	if !isMain {
		return nil // Only main group gets this
	}

	ipcDir := r.cfg.GroupIPCDir(groupFolder)
	return utils.SaveJSON(filepath.Join(ipcDir, "available_groups.json"), groups)
}

// CheckContainerRuntime checks if the container runtime is available.
func CheckContainerRuntime() error {
	// Try 'container' command first (Apple Container)
	if err := exec.Command("container", "--version").Run(); err == nil {
		return nil
	}

	// Try 'docker' as fallback
	if err := exec.Command("docker", "--version").Run(); err == nil {
		return nil
	}

	return fmt.Errorf("no container runtime found (tried 'container' and 'docker')")
}

// DockerRunner is an alternative runner that uses Docker directly.
type DockerRunner struct {
	*Runner
}

// NewDockerRunner creates a runner that uses Docker.
func NewDockerRunner(cfg *config.Config) *DockerRunner {
	return &DockerRunner{Runner: NewRunner(cfg)}
}

// RunAgent runs the container agent using Docker.
func (r *DockerRunner) RunAgent(ctx context.Context, group *types.RegisteredGroup, input types.ContainerInput) (*types.ContainerOutput, error) {
	isMain := group.Folder == r.cfg.MainGroupFolder

	// Build volume mounts
	mounts := r.buildVolumeMounts(group, isMain)

	// Add additional mounts if configured
	if group.ContainerConfig != nil && len(group.ContainerConfig.AdditionalMounts) > 0 {
		validated, _ := r.validator.ValidateAdditionalMounts(
			group.ContainerConfig.AdditionalMounts,
			group.Name,
			isMain,
		)
		mounts = append(mounts, validated...)
	}

	// Build docker arguments
	args := []string{"run", "-i", "--rm"}
	for _, mount := range mounts {
		mountArg := fmt.Sprintf("%s:%s", mount.HostPath, mount.ContainerPath)
		if mount.ReadOnly {
			mountArg += ":ro"
		}
		args = append(args, "-v", mountArg)
	}
	args = append(args, r.cfg.ContainerImage)

	// Determine timeout
	timeout := r.cfg.ContainerTimeout
	if group.ContainerConfig != nil && group.ContainerConfig.Timeout > 0 {
		timeout = time.Duration(group.ContainerConfig.Timeout) * time.Millisecond
	}

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	inputJSON, _ := json.Marshal(input)

	cmd := exec.CommandContext(ctx, "docker", args...)
	cmd.Stdin = bytes.NewReader(inputJSON)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	startTime := time.Now()
	err := cmd.Run()

	r.writeContainerLog(group.Folder, startTime, &stdout, &stderr)

	if ctx.Err() == context.DeadlineExceeded {
		return &types.ContainerOutput{Status: "error", Error: "container execution timed out"}, nil
	}

	if err != nil {
		return &types.ContainerOutput{Status: "error", Error: fmt.Sprintf("docker execution failed: %v", err)}, nil
	}

	return r.parseOutput(stdout.String())
}

// SizeReader wraps an io.Reader to track and limit bytes read.
type SizeReader struct {
	r       io.Reader
	maxSize int64
	size    int64
}

// NewSizeReader creates a new size-limited reader.
func NewSizeReader(r io.Reader, maxSize int64) *SizeReader {
	return &SizeReader{r: r, maxSize: maxSize}
}

// Read implements io.Reader with size limit.
func (sr *SizeReader) Read(p []byte) (n int, err error) {
	if sr.size >= sr.maxSize {
		return 0, fmt.Errorf("output exceeded max size of %d bytes", sr.maxSize)
	}
	n, err = sr.r.Read(p)
	sr.size += int64(n)
	return n, err
}

// SanitizeOutput removes ANSI escape codes and other noise from container output.
func SanitizeOutput(output string) string {
	// Remove ANSI escape codes
	ansiRegex := regexp.MustCompile(`\x1b\[[0-9;]*[a-zA-Z]`)
	return ansiRegex.ReplaceAllString(output, "")
}
