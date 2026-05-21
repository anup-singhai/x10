package main

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/spf13/cobra"

	"x10/agent"
	"x10/config"
	"x10/index"
	"x10/local"
	"x10/orchestrator"
	"x10/providers"
	"x10/tools"
	"x10/ui"
)

const systemPromptBase = `You are x10, a fast coding agent.

## When to use tools vs when to just answer
- If the user asks a QUESTION (explain, describe, how does X work, what is Y) — answer it in text. Do NOT edit or create files.
- Only call tools when the user explicitly asks you to modify code, run something, or look up a specific symbol not in the pre-loaded context.
- Never create documentation, architecture files, or notes unless explicitly asked.

## Critical speed rules
1. The user message already contains pre-loaded codebase context. READ IT before calling any tools.
2. If the pre-loaded context answers the task — respond IMMEDIATELY. Do not call any search tools.
3. When you must read multiple files, request ALL of them in ONE response turn — never one file per turn.
4. Prefer edit_file over write_file. Never rewrite a whole file to change a few lines.

## Image handling
- If the task contains image data (in <image> blocks), analyze that directly.
- Do NOT try to read image files from filesystem paths.

## Tools (use sparingly — each call costs a round trip)
- codebase_search(query): find symbol definitions by name
- symbol_lookup(name): exact name → file + line
- read_file, write_file, edit_file, bash, glob, grep, list_dir`

const systemPromptNoIndex = `You are x10, a fast coding agent.

## When to use tools vs when to just answer
- If the user asks a QUESTION (explain, describe, how does X work) — answer in text. Do NOT edit or create files.
- Only use tools when the user explicitly asks to modify code or run a command.

## Critical speed rules
1. When you need to read multiple files, request ALL of them in ONE response turn.
2. Prefer edit_file over write_file. Never rewrite a whole file to change a few lines.`

func main() {
	root := &cobra.Command{
		Use:   "x10",
		Short: "Lightning-fast multi-agent coding CLI",
		Long:  "x10 — direct LLM coding agent. No middleware. Multi-agent. Open source.",
	}

	var (
		flagModel    string
		flagAgents   int
		flagWorktree bool
		flagWorkdir  string
		flagNoIndex  bool
		flagIndex    bool
	)

	runCmd := &cobra.Command{
		Use:   "run [task]",
		Short: "Run a coding task (interactive if no task given)",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load()
			if err != nil {
				return err
			}

			model := flagModel
			workdir := flagWorkdir
			if workdir == "" {
				workdir, _ = os.Getwd()
			}
			workdir, _ = filepath.Abs(workdir)

			ctx, stop := context.WithCancel(context.Background())
			defer stop()

			// exit immediately on Ctrl+C
			sigs := make(chan os.Signal, 1)
			signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)
			go func() {
				<-sigs
				fmt.Println("\nbye")
				os.Exit(0)
			}()

			// boot index
			var registry *tools.Registry
			var codeIdx *index.Index
			systemPrompt := systemPromptNoIndex

			if !flagNoIndex {
				var err error
				codeIdx, err = bootIndex(ctx, workdir, flagIndex)
				if err != nil {
					fmt.Fprintf(os.Stderr, "index unavailable: %v\n", err)
					registry = tools.New()
				} else {
					registry = tools.WithIndex(codeIdx, true) // preInject=true: skip codebase_context tool
					systemPrompt = systemPromptBase

					stopWatch := make(chan struct{})
					go codeIdx.Watch(stopWatch)
					defer close(stopWatch)
				}
			} else {
				registry = tools.New()
			}

			// Adaptive model selection: if no explicit model given, estimate task complexity
			if model == "" {
				if len(args) == 1 && codeIdx != nil {
					// We have a task and index: estimate complexity
					taskStr, _ := readStdinWithImageDetection(args[0])
					taskStr = injectImageFromPath(taskStr)
					
					// Extract plain text from image if present
					if strings.HasPrefix(taskStr, "[IMAGE]") {
						lines := strings.Split(taskStr, "\n")
						var endIdx int
						for i, line := range lines {
							if line == "[END_IMAGE]" {
								endIdx = i
								break
							}
						}
						if endIdx > 0 && endIdx+1 < len(lines) {
							taskStr = strings.TrimSpace(strings.Join(lines[endIdx+1:], "\n"))
						}
					}
					
					strategy := agent.DefaultStrategy()
					model = strategy.SelectModel(taskStr, codeIdx)
				} else {
					model = cfg.DefaultModel
				}
			}

			if model == "" {
				model = "claude-sonnet-4-6"
			}

			provider, model, err := makeProvider(model, cfg)
			if err != nil {
				return err
			}

			renderer := ui.New(flagAgents > 1)
			ui.PrintBanner(model, workdir, flagAgents)

			orch := orchestrator.NewWithRegistry(workdir, model, provider, systemPrompt, flagWorktree && flagAgents > 1, registry, codeIdx)

			if len(args) == 1 {
				task, _ := readStdinWithImageDetection(args[0])
				task = injectImageFromPath(task)
				return runTask(ctx, orch, renderer, task)
			}

			// REPL: persistent session with conversation memory
			sess, err := orchestrator.NewSession(workdir, model, provider, systemPrompt, registry, codeIdx)
			if err != nil {
				return err
			}
			return repl(ctx, sess, renderer)
		},
	}

	runCmd.Flags().StringVarP(&flagModel, "model", "m", "", "Model (e.g. claude-opus-4-6, gpt-4o)")
	runCmd.Flags().IntVarP(&flagAgents, "agents", "n", 1, "Number of parallel agents")
	runCmd.Flags().BoolVar(&flagWorktree, "worktree", false, "Isolate each agent in its own git worktree")
	runCmd.Flags().StringVarP(&flagWorkdir, "dir", "d", "", "Workspace directory (default: cwd)")
	runCmd.Flags().BoolVar(&flagNoIndex, "no-index", false, "Disable codebase indexer")
	runCmd.Flags().BoolVar(&flagIndex, "reindex", false, "Force full re-index before running")

	indexCmd := &cobra.Command{
		Use:   "index [dir]",
		Short: "Build or rebuild the codebase index",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			workdir, _ := os.Getwd()
			if len(args) == 1 {
				workdir, _ = filepath.Abs(args[0])
			}

			idx, err := index.Open(workdir)
			if err != nil {
				return err
			}
			defer idx.Close()

			fmt.Printf("indexing %s ...\n", workdir)
			return idx.Build(func(done, total int, file string) {
				fmt.Printf("\r  %d/%d  %s", done, total, file)
			})
		},
	}

	configCmd := &cobra.Command{
		Use:   "config",
		Short: "Manage configuration",
	}

	configSetCmd := &cobra.Command{
		Use:   "set <key> <value>",
		Short: "Set a config value (anthropic-key, openai-key, default-model)",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := config.Set(args[0], args[1]); err != nil {
				return err
			}
			fmt.Printf("set %s\n", args[0])
			return nil
		},
	}
	configCmd.AddCommand(configSetCmd)

	// ── models command ────────────────────────────────────────────────────
	modelsCmd := &cobra.Command{
		Use:   "models",
		Short: "Manage local SLMs",
	}

	modelsListCmd := &cobra.Command{
		Use:   "list",
		Short: "List available local models",
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Println("Available local models (use with: x10 -m local:<id>)\n")
			for _, m := range local.Catalog {
				tool := ""
				if m.ToolCall {
					tool = " ✓ tools"
				}
				fmt.Printf("  %-18s  %-8s  %s%s\n", m.ID, m.Size, m.Desc, tool)
			}
			fmt.Println("\nModels are cached in ~/.x10/models/ after first download.")
			return nil
		},
	}

	modelsPullCmd := &cobra.Command{
		Use:   "pull <id>",
		Short: "Download a local model",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			m, ok := local.FindModel(args[0])
			if !ok {
				return fmt.Errorf("unknown model %q — run: x10 models list", args[0])
			}
			fmt.Printf("pulling %s (%s)...\n", m.Name, m.Size)
			_, _, err := local.EnsureReady(context.Background(), args[0])
			if err != nil {
				return err
			}
			fmt.Printf("ready: %s\n", m.ID)
			return nil
		},
	}

	modelsCmd.AddCommand(modelsListCmd, modelsPullCmd)
	root.AddCommand(runCmd, indexCmd, configCmd, modelsCmd)
	root.RunE = runCmd.RunE
	root.Flags().AddFlagSet(runCmd.Flags())

	if err := root.Execute(); err != nil {
		os.Exit(1)
	}
}

// bootIndex opens the index and builds it if it doesn't exist yet (or --reindex).
func bootIndex(ctx context.Context, workdir string, forceReindex bool) (*index.Index, error) {
	idx, err := index.Open(workdir)
	if err != nil {
		return nil, err
	}

	symbols, _, _ := idx.Stats()
	if symbols == 0 || forceReindex {
		fmt.Print("building index...")
		if err := idx.Build(nil); err != nil {
			idx.Close()
			return nil, err
		}
		symbols, files, _ := idx.Stats()
		fmt.Printf(" %d symbols in %d files\n", symbols, files)
	} else {
		_, files, _ := idx.Stats()
		fmt.Printf("index: %d symbols in %d files\n", symbols, files)
	}

	return idx, nil
}

func runTask(ctx context.Context, orch *orchestrator.Orchestrator, renderer *ui.Renderer, task string) error {
	tasks := []orchestrator.Task{{ID: "agent-1", Prompt: task}}
	renderer.StartWaiting()
	events, results := orch.Run(ctx, tasks)

	for ev := range events {
		renderer.Handle(ev)
	}
	for r := range results {
		if r.Err != nil {
			return r.Err
		}
	}
	return nil
}

// readStdinWithImageDetection checks if stdin contains an image, returns (taskPrompt, hasImage)
func readStdinWithImageDetection(task string) (string, bool) {
	stat, _ := os.Stdin.Stat()
	if (stat.Mode() & os.ModeCharDevice) != 0 {
		return task, false // stdin is terminal, not piped
	}

	// Read first few bytes to detect image format
	peek := make([]byte, 12)
	n, _ := io.ReadFull(os.Stdin, peek)
	if n == 0 {
		return task, false
	}

	// Detect format by magic bytes
	var mediaType string
	if n >= 4 && peek[0] == 0xFF && peek[1] == 0xD8 && peek[2] == 0xFF {
		mediaType = "image/jpeg"
	} else if n >= 8 && string(peek[:8]) == "\x89PNG\r\n\x1a\n" {
		mediaType = "image/png"
	} else if n >= 4 && string(peek[:4]) == "GIF8" {
		mediaType = "image/gif"
	} else if n >= 4 && string(peek[:4]) == "RIFF" && n >= 12 && string(peek[8:12]) == "WEBP" {
		mediaType = "image/webp"
	} else {
		return task, false // not a recognized image format
	}

	// Read the full image data
	buf := make([]byte, 0, 1024*1024*5) // up to 5MB
	buf = append(buf, peek[:n]...)
	rest, _ := io.ReadAll(os.Stdin)
	buf = append(buf, rest...)

	encoded := base64.StdEncoding.EncodeToString(buf)
	
	// Build image task format
	imgTask := fmt.Sprintf("[IMAGE]\nbase64:%s\nmedia_type:%s\n[END_IMAGE]\n\n%s", encoded, mediaType, task)
	return imgTask, true
}

// detectImageInput checks if stdin is piped and returns (imageBase64, mediaType, isImage)
func detectImageInput() (string, string, bool) {
	// Check if stdin is piped
	stat, _ := os.Stdin.Stat()
	if (stat.Mode() & os.ModeCharDevice) != 0 {
		return "", "", false // stdin is terminal, not piped
	}

	// Read first few bytes to detect image format
	peek := make([]byte, 12)
	n, _ := io.ReadFull(os.Stdin, peek)
	if n == 0 {
		return "", "", false
	}

	// Detect format by magic bytes
	var mediaType string
	if n >= 4 && peek[0] == 0xFF && peek[1] == 0xD8 && peek[2] == 0xFF {
		mediaType = "image/jpeg"
	} else if n >= 8 && string(peek[:8]) == "\x89PNG\r\n\x1a\n" {
		mediaType = "image/png"
	} else if n >= 4 && string(peek[:4]) == "GIF8" {
		mediaType = "image/gif"
	} else if n >= 4 && string(peek[:4]) == "RIFF" && n >= 12 && string(peek[8:12]) == "WEBP" {
		mediaType = "image/webp"
	} else {
		return "", "", false // not a recognized image format
	}

	// Read the full image data
	buf := make([]byte, 0, 1024*1024*5) // up to 5MB
	buf = append(buf, peek[:n]...)
	rest, _ := io.ReadAll(os.Stdin)
	buf = append(buf, rest...)

	encoded := base64.StdEncoding.EncodeToString(buf)
	return encoded, mediaType, true
}

func repl(ctx context.Context, sess *orchestrator.Session, renderer *ui.Renderer) error {
	for {
		input, err := ui.ReadLine()
		if err == ui.ErrInterrupt {
			fmt.Println("bye")
			return nil
		}
		if err != nil {
			return nil
		}
		if input == "" {
			continue
		}
		if input == "/exit" || input == "/quit" {
			fmt.Println("bye")
			return nil
		}
		if input == "/clear" {
			sess.Reset()
			fmt.Println("conversation cleared")
			continue
		}

		input = injectImageFromPath(input)

		renderer.StartWaiting()
		events := sess.Send(ctx, input)
		for ev := range events {
			renderer.Handle(ev)
		}
		if ctx.Err() != nil {
			return nil
		}
	}
}

// injectImageFromPath scans the task string for an image file path (e.g. dragged
// from Finder into the terminal). If found, reads the file, encodes it as base64,
// and returns a task string in the [IMAGE]…[END_IMAGE] format the agent understands.
func injectImageFromPath(task string) string {
	imgExts := map[string]string{
		".png": "image/png", ".jpg": "image/jpeg", ".jpeg": "image/jpeg",
		".gif": "image/gif", ".webp": "image/webp",
	}

	lower := strings.ToLower(task)
	for ext, mime := range imgExts {
		idx := strings.Index(lower, ext)
		if idx == -1 {
			continue
		}
		end := idx + len(ext)

		// walk left to find the start of the path
		// handle shell-escaped spaces (\ )
		start := 0
		for i := idx - 1; i >= 0; i-- {
			ch := task[i]
			if ch == ' ' || ch == '\t' {
				if i > 0 && task[i-1] == '\\' {
					// escaped space — part of path, keep going
					i-- // skip the backslash too
					continue
				}
				start = i + 1
				break
			}
		}

		candidate := task[start:end]
		candidate = strings.ReplaceAll(candidate, "\\ ", " ") // unescape

		if _, err := os.Stat(candidate); err != nil {
			continue // file doesn't exist, not a path
		}

		data, err := os.ReadFile(candidate)
		if err != nil {
			continue
		}

		// everything before and after the path is the text task
		before := strings.TrimSpace(task[:start])
		after := strings.TrimSpace(task[end:])
		text := strings.TrimSpace(before + " " + after)
		if text == "" {
			text = "analyze this image"
		}

		encoded := base64.StdEncoding.EncodeToString(data)
		return fmt.Sprintf("[IMAGE]\nbase64:%s\nmedia_type:%s\n[END_IMAGE]\n\n%s", encoded, mime, text)
	}

	return task // no image found
}

// imageFilenameFromInput extracts just the base filename of any image path found in input.
func imageFilenameFromInput(input string) string {
	exts := []string{".png", ".jpg", ".jpeg", ".gif", ".webp"}
	lower := strings.ToLower(input)
	for _, ext := range exts {
		idx := strings.Index(lower, ext)
		if idx == -1 {
			continue
		}
		end := idx + len(ext)
		start := 0
		for i := idx - 1; i >= 0; i-- {
			ch := input[i]
			if ch == ' ' || ch == '\t' {
				if i > 0 && input[i-1] == '\\' {
					i--
					continue
				}
				start = i + 1
				break
			}
		}
		candidate := strings.ReplaceAll(input[start:end], "\\ ", " ")
		if _, err := os.Stat(candidate); err == nil {
			return filepath.Base(candidate)
		}
	}
	return ""
}

// makeProvider returns (provider, cleanModel, error).
// cleanModel strips any provider prefix (e.g. "ollama:llama3.2" → "llama3.2").
func makeProvider(model string, cfg *config.Config) (providers.Provider, string, error) {
	switch {
	// ── built-in local SLMs (managed by x10) ─────────────────────────────
	case strings.HasPrefix(model, "local:"):
		alias := strings.TrimPrefix(model, "local:")
		apiBase, cleanModel, err := local.EnsureReady(context.Background(), alias)
		if err != nil {
			return nil, "", err
		}
		return providers.NewOpenAICompat(apiBase, ""), cleanModel, nil

	// ── local LLMs (OpenAI-compatible) ────────────────────────────────────
	case strings.HasPrefix(model, "ollama:"), strings.HasPrefix(model, "ollama/"):
		clean := strings.TrimPrefix(strings.TrimPrefix(model, "ollama:"), "ollama/")
		base := cfg.OllamaURL
		if base == "" {
			base = "http://localhost:11434/v1"
		}
		return providers.NewOpenAICompat(base, "ollama"), clean, nil

	case strings.HasPrefix(model, "lmstudio:"), strings.HasPrefix(model, "lmstudio/"):
		clean := strings.TrimPrefix(strings.TrimPrefix(model, "lmstudio:"), "lmstudio/")
		base := cfg.LMStudioURL
		if base == "" {
			base = "http://localhost:1234/v1"
		}
		return providers.NewOpenAICompat(base, "lm-studio"), clean, nil

	// generic OpenAI-compat: "http://host:port/v1:modelname"
	case strings.HasPrefix(model, "http://"), strings.HasPrefix(model, "https://"):
		parts := strings.SplitN(model, ":", 3)
		if len(parts) == 3 {
			base := parts[0] + ":" + parts[1]
			clean := parts[2]
			return providers.NewOpenAICompat(base, ""), clean, nil
		}
		return providers.NewOpenAICompat(model, ""), model, nil

	// ── free/open model providers (no local install) ──────────────────────
	case strings.HasPrefix(model, "groq:"), strings.HasPrefix(model, "groq/"):
		clean := strings.TrimPrefix(strings.TrimPrefix(model, "groq:"), "groq/")
		if cfg.GroqKey == "" {
			return nil, "", fmt.Errorf("groq key not set — run: x10 config set groq-key <key>\nGet a free key at https://console.groq.com")
		}
		return providers.NewOpenAICompat("https://api.groq.com/openai/v1", cfg.GroqKey), clean, nil

	case strings.HasPrefix(model, "together:"), strings.HasPrefix(model, "together/"):
		clean := strings.TrimPrefix(strings.TrimPrefix(model, "together:"), "together/")
		if cfg.TogetherKey == "" {
			return nil, "", fmt.Errorf("together key not set — run: x10 config set together-key <key>")
		}
		return providers.NewOpenAICompat("https://api.together.xyz/v1", cfg.TogetherKey), clean, nil

	// ── cloud providers ───────────────────────────────────────────────────
	case strings.HasPrefix(model, "claude"):
		if cfg.AnthropicKey == "" {
			return nil, "", fmt.Errorf("anthropic key not set — run: x10 config set anthropic-key <key>")
		}
		return providers.NewAnthropic(cfg.AnthropicKey), model, nil

	case strings.HasPrefix(model, "gpt"), strings.HasPrefix(model, "o1"), strings.HasPrefix(model, "o3"):
		if cfg.OpenAIKey == "" {
			return nil, "", fmt.Errorf("openai key not set — run: x10 config set openai-key <key>")
		}
		return providers.NewOpenAI(cfg.OpenAIKey), model, nil

	default:
		if cfg.AnthropicKey != "" {
			return providers.NewAnthropic(cfg.AnthropicKey), model, nil
		}
		if cfg.OpenAIKey != "" {
			return providers.NewOpenAI(cfg.OpenAIKey), model, nil
		}
		if cfg.GroqKey != "" {
			return providers.NewOpenAICompat("https://api.groq.com/openai/v1", cfg.GroqKey), model, nil
		}
		return nil, "", fmt.Errorf("no API key configured\n  Anthropic: x10 config set anthropic-key <key>\n  Free (Groq): x10 config set groq-key <key>  (get at console.groq.com)")
	}
}
