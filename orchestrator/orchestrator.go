package orchestrator

import (
	"context"
	"fmt"
	"sync"
	"time"

	"x10/cell"
	"x10/index"
	"x10/providers"
	"x10/tools"
)

// Task is a unit of work assigned to a cell.
type Task struct {
	ID     string
	Prompt string
}

// CellEvent is a streaming event from any cell, tagged with its source.
type CellEvent struct {
	cell.Event
	CellID string
	TaskID string
}

// Result is the final outcome of a cell's execution.
type Result struct {
	CellID  string
	TaskID  string
	Workdir string
	Err     error
}

// Orchestrator manages multiple cells working in parallel on a workspace.
type Orchestrator struct {
	workspaceDir string
	model        string
	provider     providers.Provider
	systemPrompt string
	useWorktree  bool
	registry     *tools.Registry
	index        *index.Index // optional; enables pre-injection of context
}

func New(workspaceDir, model string, provider providers.Provider, systemPrompt string, useWorktree bool) *Orchestrator {
	return &Orchestrator{
		workspaceDir: workspaceDir,
		model:        model,
		provider:     provider,
		systemPrompt: systemPrompt,
		useWorktree:  useWorktree,
		registry:     tools.New(),
	}
}

// NewWithRegistry creates an orchestrator with a pre-configured tool registry and index.
func NewWithRegistry(workspaceDir, model string, provider providers.Provider, systemPrompt string, useWorktree bool, registry *tools.Registry, idx *index.Index) *Orchestrator {
	return &Orchestrator{
		workspaceDir: workspaceDir,
		model:        model,
		provider:     provider,
		systemPrompt: systemPrompt,
		useWorktree:  useWorktree,
		registry:     registry,
		index:        idx,
	}
}

// Run spawns one cell per task and fans all events into a single channel.
func (o *Orchestrator) Run(ctx context.Context, tasks []Task) (<-chan CellEvent, <-chan Result) {
	events := make(chan CellEvent, 512)
	results := make(chan Result, len(tasks))

	var wg sync.WaitGroup
	for _, t := range tasks {
		wg.Add(1)
		go func(task Task) {
			defer wg.Done()
			result := o.runCell(ctx, task, events)
			results <- result
		}(t)
	}

	go func() {
		wg.Wait()
		close(events)
		close(results)
	}()

	return events, results
}

func (o *Orchestrator) runCell(ctx context.Context, task Task, events chan<- CellEvent) Result {
	cellID := fmt.Sprintf("%s-%d", task.ID, time.Now().UnixMilli())

	c, err := cell.Spawn(cell.Config{
		ID:           cellID,
		WorkspaceDir: o.workspaceDir,
		Model:        o.model,
		Provider:     o.provider,
		SystemPrompt: o.systemPrompt,
		UseWorktree:  o.useWorktree,
		Registry:     o.registry,
		Index:        o.index,
	})
	if err != nil {
		return Result{CellID: cellID, TaskID: task.ID, Err: err}
	}
	defer c.Stop()

	for ev := range c.Run(ctx, task.Prompt) {
		events <- CellEvent{Event: ev, CellID: cellID, TaskID: task.ID}
	}

	return Result{
		CellID:  cellID,
		TaskID:  task.ID,
		Workdir: c.Workdir,
	}
}
