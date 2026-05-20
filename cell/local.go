package cell

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"x10/agent"
	"x10/index"
	"x10/providers"
	"x10/tools"
)

// Event wraps an agent event with cell metadata.
type Event = agent.Event

// Cell is an isolated execution context for one agent task.
type Cell struct {
	ID      string
	Workdir string
	agent   *agent.Agent
	cancel  context.CancelFunc
}

// Config controls how a cell is created.
type Config struct {
	ID           string
	WorkspaceDir string
	Model        string
	Provider     providers.Provider
	SystemPrompt string
	UseWorktree  bool
	Registry     *tools.Registry
	Index        *index.Index // optional; enables pre-injection of context
}

// Spawn creates a new local cell.
func Spawn(cfg Config) (*Cell, error) {
	workdir := cfg.WorkspaceDir

	if cfg.UseWorktree {
		var err error
		workdir, err = createWorktree(cfg.WorkspaceDir, cfg.ID)
		if err != nil {
			return nil, fmt.Errorf("cell %s: worktree: %w", cfg.ID, err)
		}
	}

	registry := cfg.Registry
	if registry == nil {
		registry = tools.New()
	}

	a := agent.New(cfg.ID, workdir, cfg.Model, cfg.Provider, registry, cfg.SystemPrompt)

	// attach index-based context pre-builder so the model starts with
	// relevant code already in context — eliminates exploration round trips
	if cfg.Index != nil {
		idx := cfg.Index
		a.WithContextBuilder(func(task string) string {
			ctx, err := idx.BuildRichContext(task, 20)
			if err != nil || ctx == "" {
				return ""
			}
			return ctx
		})
	}

	return &Cell{
		ID:      cfg.ID,
		Workdir: workdir,
		agent:   a,
	}, nil
}

// Run starts the cell and returns a channel of events.
func (c *Cell) Run(ctx context.Context, task string) <-chan Event {
	ctx, cancel := context.WithCancel(ctx)
	c.cancel = cancel
	return c.agent.Run(ctx, task)
}

// Stop cancels the cell's execution.
func (c *Cell) Stop() {
	if c.cancel != nil {
		c.cancel()
	}
}

// Reset clears the agent's conversation history.
func (c *Cell) Reset() { c.agent.Reset() }

func createWorktree(workspaceDir, cellID string) (string, error) {
	wtDir := filepath.Join(workspaceDir, ".x10", "worktrees", cellID)
	if err := os.MkdirAll(filepath.Dir(wtDir), 0755); err != nil {
		return "", err
	}
	branch := "x10/" + cellID
	cmd := exec.Command("git", "-C", workspaceDir, "worktree", "add", "-b", branch, wtDir, "HEAD")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git worktree add: %s", string(out))
	}
	return wtDir, nil
}
