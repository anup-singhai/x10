package orchestrator

import (
	"context"

	"x10/cell"
	"x10/index"
	"x10/providers"
	"x10/tools"
)

// Session is a persistent single-agent conversation.
// Unlike Run (which spawns fresh cells per task), a Session
// reuses one cell across REPL turns so history is preserved.
type Session struct {
	cell *cell.Cell
}

// NewSession creates a persistent session cell.
func NewSession(workspaceDir, model string, provider providers.Provider, systemPrompt string, registry *tools.Registry, idx *index.Index) (*Session, error) {
	c, err := cell.Spawn(cell.Config{
		ID:           "session",
		WorkspaceDir: workspaceDir,
		Model:        model,
		Provider:     provider,
		SystemPrompt: systemPrompt,
		UseWorktree:  false,
		Registry:     registry,
		Index:        idx,
	})
	if err != nil {
		return nil, err
	}
	return &Session{cell: c}, nil
}

// Send sends a message and returns a stream of events.
func (s *Session) Send(ctx context.Context, task string) <-chan CellEvent {
	raw := s.cell.Run(ctx, task)
	out := make(chan CellEvent, 256)
	go func() {
		for ev := range raw {
			out <- CellEvent{Event: ev, CellID: "session", TaskID: ""}
		}
		close(out)
	}()
	return out
}

// Reset clears the conversation history.
func (s *Session) Reset() {
	s.cell.Reset()
}
