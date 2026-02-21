// Package agent orchestrates the inference agent lifecycle.
//
// Lifecycle:
//
//  1. Initialize: Load config, create 0G clients, create HCS handler
//  2. Register: Connect to daemon client, register as inference agent
//  3. Subscribe: Start HCS subscription for task assignments
//  4. Run: Enter main loop — wait for tasks, execute, report
//  5. Shutdown: Graceful shutdown on context cancellation or signal
//
// Task processing pipeline (sequential per task):
//
//	Receive TaskAssignment from HCS
//	→ Submit inference job to 0G Compute
//	→ Poll for result (context-aware)
//	→ Store result on 0G Storage
//	→ Mint iNFT with result metadata on 0G Chain
//	→ Publish audit event to 0G DA
//	→ Report TaskResult back via HCS
package agent

import (
	"context"
	"fmt"
	"log/slog"
	"sync/atomic"
	"time"

	"github.com/lancekrogers/agent-inference-ethden-2026/internal/hcs"
	"github.com/lancekrogers/agent-inference-ethden-2026/internal/zerog/compute"
	"github.com/lancekrogers/agent-inference-ethden-2026/internal/zerog/da"
	"github.com/lancekrogers/agent-inference-ethden-2026/internal/zerog/inft"
	"github.com/lancekrogers/agent-inference-ethden-2026/internal/zerog/storage"
)

// Agent orchestrates the inference agent's full lifecycle.
// All dependencies are injected at construction time.
type Agent struct {
	cfg     Config
	log     *slog.Logger
	compute compute.ComputeBroker
	storage storage.StorageClient
	minter  inft.INFTMinter
	audit   da.AuditPublisher
	handler *hcs.Handler

	startTime      time.Time
	completedTasks atomic.Int64
	failedTasks    atomic.Int64
}

// New creates an Agent with all required dependencies.
func New(
	cfg Config,
	log *slog.Logger,
	comp compute.ComputeBroker,
	store storage.StorageClient,
	mint inft.INFTMinter,
	aud da.AuditPublisher,
	h *hcs.Handler,
) *Agent {
	return &Agent{
		cfg:     cfg,
		log:     log,
		compute: comp,
		storage: store,
		minter:  mint,
		audit:   aud,
		handler: h,
	}
}

// Run starts the agent and blocks until the context is cancelled.
func (a *Agent) Run(ctx context.Context) error {
	a.startTime = time.Now()
	a.log.Info("starting inference agent", "agent_id", a.cfg.AgentID)

	// Start HCS subscription in background
	go func() {
		if err := a.handler.StartSubscription(ctx); err != nil && ctx.Err() == nil {
			a.log.Error("HCS subscription failed", "error", err)
		}
	}()

	// Start health reporter in background
	go a.healthLoop(ctx)

	// Process tasks from HCS
	for {
		select {
		case <-ctx.Done():
			a.log.Info("shutting down inference agent",
				"completed", a.completedTasks.Load(),
				"failed", a.failedTasks.Load(),
				"uptime", time.Since(a.startTime))
			return ctx.Err()
		case task := <-a.handler.Tasks():
			if err := a.processTask(ctx, task); err != nil {
				a.log.Error("task processing failed", "task_id", task.TaskID, "error", err)
				a.reportFailure(ctx, task, err)
				a.failedTasks.Add(1)
			}
		}
	}
}

// processTask executes the full inference pipeline for a single task.
func (a *Agent) processTask(ctx context.Context, task hcs.TaskAssignment) error {
	a.log.Info("processing task", "task_id", task.TaskID, "model", task.ModelID)
	start := time.Now()

	// 1. Audit: task received
	a.audit.Publish(ctx, da.AuditEvent{
		Type:      da.EventTypeTaskReceived,
		AgentID:   a.cfg.AgentID,
		TaskID:    task.TaskID,
		Timestamp: time.Now(),
	})

	// 2. Submit inference job to 0G Compute
	jobID, err := a.compute.SubmitJob(ctx, compute.JobRequest{
		ModelID:   task.ModelID,
		Input:     task.Input,
		MaxTokens: task.MaxTokens,
	})
	if err != nil {
		return fmt.Errorf("agent: compute submit failed for task %s: %w", task.TaskID, err)
	}

	// 3. Poll for result
	result, err := a.compute.GetResult(ctx, jobID)
	if err != nil {
		return fmt.Errorf("agent: compute result failed for job %s: %w", jobID, err)
	}

	// 4. Store result on 0G Storage
	contentID, err := a.storage.Upload(ctx, []byte(result.Output), storage.Metadata{
		Name:        fmt.Sprintf("inference-%s", task.TaskID),
		ContentType: "application/json",
		Tags:        map[string]string{"task_id": task.TaskID, "model": task.ModelID},
	})
	if err != nil {
		return fmt.Errorf("agent: storage upload failed for task %s: %w", task.TaskID, err)
	}

	// 5. Mint iNFT with encrypted metadata
	tokenID, err := a.minter.Mint(ctx, inft.MintRequest{
		Name:             fmt.Sprintf("Inference Result: %s", task.TaskID),
		InferenceJobID:   jobID,
		StorageContentID: contentID,
		PlaintextMeta: map[string]string{
			"task_id":  task.TaskID,
			"model_id": task.ModelID,
			"agent_id": a.cfg.AgentID,
		},
	})
	if err != nil {
		return fmt.Errorf("agent: iNFT mint failed for task %s: %w", task.TaskID, err)
	}

	// 6. Audit: inference completed
	auditID, _ := a.audit.Publish(ctx, da.AuditEvent{
		Type:       da.EventTypeJobCompleted,
		AgentID:    a.cfg.AgentID,
		TaskID:     task.TaskID,
		JobID:      jobID,
		StorageRef: contentID,
		INFTRef:    tokenID,
		Timestamp:  time.Now(),
	})

	// 7. Report result back via HCS
	duration := time.Since(start)
	err = a.handler.PublishResult(ctx, hcs.TaskResult{
		TaskID:            task.TaskID,
		Status:            "completed",
		Output:            result.Output,
		DurationMs:        duration.Milliseconds(),
		TokensUsed:        result.TokensUsed,
		StorageContentID:  contentID,
		INFTTokenID:       tokenID,
		AuditSubmissionID: auditID,
	})
	if err != nil {
		return fmt.Errorf("agent: result publish failed for task %s: %w", task.TaskID, err)
	}

	a.completedTasks.Add(1)
	a.log.Info("task completed", "task_id", task.TaskID, "duration", duration)
	return nil
}

func (a *Agent) reportFailure(ctx context.Context, task hcs.TaskAssignment, taskErr error) {
	a.handler.PublishResult(ctx, hcs.TaskResult{
		TaskID: task.TaskID,
		Status: "failed",
		Error:  taskErr.Error(),
	})
}

func (a *Agent) healthLoop(ctx context.Context) {
	ticker := time.NewTicker(a.cfg.HealthInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			a.handler.PublishHealth(ctx, hcs.HealthStatus{
				AgentID:        a.cfg.AgentID,
				Status:         "idle",
				UptimeSeconds:  int64(time.Since(a.startTime).Seconds()),
				CompletedTasks: int(a.completedTasks.Load()),
				FailedTasks:    int(a.failedTasks.Load()),
			})
		}
	}
}
