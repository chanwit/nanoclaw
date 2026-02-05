// Package scheduler provides task scheduling for NanoClaw.
package scheduler

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/robfig/cron/v3"

	"github.com/nanoclaw/go-nanoclaw/internal/config"
	"github.com/nanoclaw/go-nanoclaw/internal/container"
	"github.com/nanoclaw/go-nanoclaw/internal/db"
	"github.com/nanoclaw/go-nanoclaw/internal/logger"
	"github.com/nanoclaw/go-nanoclaw/internal/types"
	"github.com/nanoclaw/go-nanoclaw/internal/utils"
)

var log = logger.WithComponent("scheduler")

// TaskRunner is the interface for running tasks.
type TaskRunner interface {
	RunTask(ctx context.Context, task *types.ScheduledTask) (*types.ContainerOutput, error)
}

// Scheduler manages scheduled task execution.
type Scheduler struct {
	cfg             *config.Config
	db              *db.DB
	runner          *container.Runner
	registeredGroup func(folder string) *types.RegisteredGroup
	sessions        map[string]string
	stopCh          chan struct{}
	running         bool
}

// NewScheduler creates a new task scheduler.
func NewScheduler(cfg *config.Config, database *db.DB, runner *container.Runner) *Scheduler {
	return &Scheduler{
		cfg:      cfg,
		db:       database,
		runner:   runner,
		sessions: make(map[string]string),
		stopCh:   make(chan struct{}),
	}
}

// SetGroupLookup sets the function to look up registered groups.
func (s *Scheduler) SetGroupLookup(fn func(folder string) *types.RegisteredGroup) {
	s.registeredGroup = fn
}

// SetSessions sets the session map.
func (s *Scheduler) SetSessions(sessions map[string]string) {
	s.sessions = sessions
}

// Start starts the scheduler loop.
func (s *Scheduler) Start(ctx context.Context) {
	if s.running {
		return
	}
	s.running = true

	log.Info().Dur("interval", s.cfg.SchedulerPollInterval).Msg("Starting scheduler")

	ticker := time.NewTicker(s.cfg.SchedulerPollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			log.Info().Msg("Scheduler stopping (context cancelled)")
			return
		case <-s.stopCh:
			log.Info().Msg("Scheduler stopping (stop signal)")
			return
		case <-ticker.C:
			s.processDueTasks(ctx)
		}
	}
}

// Stop stops the scheduler.
func (s *Scheduler) Stop() {
	if s.running {
		close(s.stopCh)
		s.running = false
	}
}

// processDueTasks processes all tasks that are due to run.
func (s *Scheduler) processDueTasks(ctx context.Context) {
	tasks, err := s.db.GetDueTasks()
	if err != nil {
		log.Error().Err(err).Msg("Failed to get due tasks")
		return
	}

	if len(tasks) == 0 {
		return
	}

	log.Info().Int("count", len(tasks)).Msg("Processing due tasks")

	for _, task := range tasks {
		select {
		case <-ctx.Done():
			return
		default:
			s.runTask(ctx, &task)
		}
	}
}

// runTask executes a single task.
func (s *Scheduler) runTask(ctx context.Context, task *types.ScheduledTask) {
	startTime := time.Now()

	log.Info().
		Str("taskId", task.ID).
		Str("group", task.GroupFolder).
		Str("scheduleType", string(task.ScheduleType)).
		Msg("Running scheduled task")

	// Find the registered group
	group := s.registeredGroup(task.GroupFolder)
	if group == nil {
		log.Warn().Str("taskId", task.ID).Str("folder", task.GroupFolder).Msg("Group not found for task")
		return
	}

	// Build container input
	input := types.ContainerInput{
		Prompt:          task.Prompt,
		GroupFolder:     task.GroupFolder,
		ChatJID:         task.ChatJID,
		IsMain:          task.GroupFolder == s.cfg.MainGroupFolder,
		IsScheduledTask: true,
	}

	// Use session context if configured
	if task.ContextMode == types.ContextModeGroup {
		if sessionID, ok := s.sessions[task.GroupFolder]; ok {
			input.SessionID = sessionID
		}
	}

	// Write tasks snapshot
	allTasks, _ := s.db.GetAllTasks()
	s.runner.WriteTasksSnapshot(task.GroupFolder, input.IsMain, allTasks)

	// Run the task
	output, err := s.runner.RunAgent(ctx, group, input)

	duration := time.Since(startTime)

	// Build log entry
	runLog := types.TaskRunLog{
		TaskID:     task.ID,
		RunAt:      utils.FormatTimestamp(startTime),
		DurationMs: duration.Milliseconds(),
	}

	var result string
	var nextRun *time.Time
	var newStatus types.TaskStatus = task.Status

	if err != nil {
		runLog.Status = "error"
		errMsg := err.Error()
		runLog.Error = &errMsg
		result = "Error: " + err.Error()
	} else if output.Status == "error" {
		runLog.Status = "error"
		runLog.Error = &output.Error
		result = "Error: " + output.Error
	} else {
		runLog.Status = "success"
		if output.Result != nil {
			runLog.Result = output.Result
			result = *output.Result
		}
	}

	// Calculate next run time
	if task.ScheduleType == types.ScheduleTypeOnce {
		newStatus = types.TaskStatusCompleted
		nextRun = nil
	} else {
		next := s.calculateNextRun(task)
		if next != nil {
			nextRun = next
		}
	}

	// Update task in database
	if err := s.db.UpdateTaskAfterRun(task.ID, startTime, result, nextRun, newStatus); err != nil {
		log.Error().Err(err).Str("taskId", task.ID).Msg("Failed to update task after run")
	}

	// Log the run
	if err := s.db.LogTaskRun(runLog); err != nil {
		log.Error().Err(err).Str("taskId", task.ID).Msg("Failed to log task run")
	}

	log.Info().
		Str("taskId", task.ID).
		Str("status", runLog.Status).
		Dur("duration", duration).
		Msg("Task completed")
}

// calculateNextRun calculates the next run time for a task.
func (s *Scheduler) calculateNextRun(task *types.ScheduledTask) *time.Time {
	now := time.Now()

	switch task.ScheduleType {
	case types.ScheduleTypeCron:
		parser := cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)
		schedule, err := parser.Parse(task.ScheduleValue)
		if err != nil {
			log.Error().Err(err).Str("taskId", task.ID).Str("cron", task.ScheduleValue).Msg("Failed to parse cron expression")
			return nil
		}
		next := schedule.Next(now)
		return &next

	case types.ScheduleTypeInterval:
		intervalMs, err := strconv.ParseInt(task.ScheduleValue, 10, 64)
		if err != nil {
			log.Error().Err(err).Str("taskId", task.ID).Str("interval", task.ScheduleValue).Msg("Failed to parse interval")
			return nil
		}
		next := now.Add(time.Duration(intervalMs) * time.Millisecond)
		return &next

	case types.ScheduleTypeOnce:
		return nil
	}

	return nil
}

// CreateTask creates a new scheduled task.
func (s *Scheduler) CreateTask(groupFolder, chatJID, prompt string, scheduleType types.ScheduleType, scheduleValue string, contextMode types.ContextMode) (*types.ScheduledTask, error) {
	// Validate schedule
	if err := ValidateSchedule(scheduleType, scheduleValue); err != nil {
		return nil, err
	}

	task := &types.ScheduledTask{
		ID:            utils.GenerateID("task"),
		GroupFolder:   groupFolder,
		ChatJID:       chatJID,
		Prompt:        prompt,
		ScheduleType:  scheduleType,
		ScheduleValue: scheduleValue,
		ContextMode:   contextMode,
		Status:        types.TaskStatusActive,
		CreatedAt:     utils.FormatTimestamp(time.Now()),
	}

	// Calculate initial next_run
	nextRun := s.calculateNextRun(task)
	if nextRun != nil {
		ts := utils.FormatTimestamp(*nextRun)
		task.NextRun = &ts
	}

	if err := s.db.CreateTask(task); err != nil {
		return nil, err
	}

	log.Info().
		Str("taskId", task.ID).
		Str("group", groupFolder).
		Str("scheduleType", string(scheduleType)).
		Msg("Task created")

	return task, nil
}

// PauseTask pauses a task.
func (s *Scheduler) PauseTask(taskID string) error {
	return s.db.UpdateTaskStatus(taskID, types.TaskStatusPaused)
}

// ResumeTask resumes a paused task.
func (s *Scheduler) ResumeTask(taskID string) error {
	task, err := s.db.GetTask(taskID)
	if err != nil {
		return err
	}
	if task == nil {
		return nil
	}

	// Recalculate next_run
	nextRun := s.calculateNextRun(task)

	return s.db.UpdateTaskAfterRun(taskID, time.Now(), "", nextRun, types.TaskStatusActive)
}

// CancelTask cancels (deletes) a task.
func (s *Scheduler) CancelTask(taskID string) error {
	return s.db.DeleteTask(taskID)
}

// ValidateSchedule validates a schedule configuration.
func ValidateSchedule(scheduleType types.ScheduleType, scheduleValue string) error {
	switch scheduleType {
	case types.ScheduleTypeCron:
		parser := cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)
		if _, err := parser.Parse(scheduleValue); err != nil {
			return err
		}
	case types.ScheduleTypeInterval:
		ms, err := strconv.ParseInt(scheduleValue, 10, 64)
		if err != nil {
			return err
		}
		if ms < 60000 { // Minimum 1 minute
			return err
		}
	case types.ScheduleTypeOnce:
		if _, err := time.Parse(time.RFC3339, scheduleValue); err != nil {
			return err
		}
	default:
		return nil
	}
	return nil
}

// ParseHumanSchedule parses human-readable schedule strings.
func ParseHumanSchedule(input string) (types.ScheduleType, string, error) {
	input = strings.ToLower(strings.TrimSpace(input))

	// Check for common patterns
	if strings.HasPrefix(input, "every ") {
		// "every 5 minutes", "every hour", "every day at 9am"
		remainder := strings.TrimPrefix(input, "every ")

		// "every N minutes/hours"
		if strings.Contains(remainder, "minute") {
			var n int
			if err := parseNumber(remainder, &n); err == nil {
				return types.ScheduleTypeInterval, strconv.FormatInt(int64(n)*60000, 10), nil
			}
		}

		if strings.Contains(remainder, "hour") {
			var n int = 1
			_ = parseNumber(remainder, &n)
			return types.ScheduleTypeInterval, strconv.FormatInt(int64(n)*3600000, 10), nil
		}

		// "every day at HH:MM"
		if strings.Contains(remainder, "day at ") {
			// Parse time and convert to cron
			// Simplified: "0 9 * * *" for "every day at 9:00"
		}
	}

	// Check for cron expression
	if strings.Contains(input, " ") && !strings.Contains(input, "at") {
		return types.ScheduleTypeCron, input, nil
	}

	// Default to cron
	return types.ScheduleTypeCron, input, nil
}

func parseNumber(s string, n *int) error {
	for _, word := range strings.Fields(s) {
		if i, err := strconv.Atoi(word); err == nil {
			*n = i
			return nil
		}
	}
	return nil
}
