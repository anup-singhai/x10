package ui

import (
	"fmt"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/lipgloss"
	"golang.org/x/term"

	"x10/agent"
	"x10/orchestrator"
)

var (
	styleAgent  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("12"))
	styleDim    = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	styleAction = lipgloss.NewStyle().Foreground(lipgloss.Color("11"))
	styleResult = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	styleError  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("9"))
	styleDone   = lipgloss.NewStyle().Foreground(lipgloss.Color("10"))

	// diff colors
	styleDiffAdded   = lipgloss.NewStyle().Background(lipgloss.Color("22")).Foreground(lipgloss.Color("156"))
	styleDiffRemoved = lipgloss.NewStyle().Background(lipgloss.Color("52")).Foreground(lipgloss.Color("203"))
	styleDiffCtx     = lipgloss.NewStyle().Foreground(lipgloss.Color("240"))

	// image attachment
	styleImg = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("14"))

	// tool result styling
	styleToolRead    = lipgloss.NewStyle().Foreground(lipgloss.Color("14")).Bold(true)  // cyan
	styleToolWrite   = lipgloss.NewStyle().Foreground(lipgloss.Color("10")).Bold(true)  // green
	styleToolError   = lipgloss.NewStyle().Foreground(lipgloss.Color("9")).Bold(true)   // red
	styleToolSearch  = lipgloss.NewStyle().Foreground(lipgloss.Color("13")).Bold(true)  // magenta
	styleFileName    = lipgloss.NewStyle().Foreground(lipgloss.Color("15"))             // bright white
	styleFilePath    = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))              // dim
	styleLineNum     = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))              // dim
	styleMeta        = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))              // dim

	styleH1   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("12"))
	styleH2   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("14"))
	styleH3   = lipgloss.NewStyle().Bold(true)
	styleBold = lipgloss.NewStyle().Bold(true)
	styleCode = lipgloss.NewStyle().Foreground(lipgloss.Color("10"))
	stylePre  = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))

	reBold   = regexp.MustCompile(`\*\*(.+?)\*\*`)
	reItalic = regexp.MustCompile(`\*([^*\n]+?)\*`)
	reCode   = regexp.MustCompile("`([^`]+)`")
)

type Renderer struct {
	multiAgent  bool
	lastAgent   string
	lineBuf     strings.Builder
	inCodeBlock bool
	spinner     *spinner
	mu          sync.Mutex
}

func New(multiAgent bool) *Renderer {
	return &Renderer{multiAgent: multiAgent}
}

func (r *Renderer) Handle(ev orchestrator.CellEvent) {
	if r.multiAgent && ev.CellID != r.lastAgent {
		r.flushLine()
		r.lastAgent = ev.CellID
		fmt.Printf("\n%s\n", styleAgent.Render("["+ev.CellID+"]"))
	}

	switch ev.Type {
	case agent.EventToken:
		r.stopSpinner()
		// buffer until newline, then render line
		for _, ch := range ev.Text {
			if ch == '\n' {
				r.flushLine()
				fmt.Println()
			} else {
				r.lineBuf.WriteRune(ch)
			}
		}

	case agent.EventToolCall:
		r.stopSpinner()
		r.flushLine()
		display := ev.Action
		if len(display) > 110 {
			display = display[:110] + "…"
		}
		fmt.Printf("%s %s\n", styleDim.Render("▶"), styleAction.Render(display))
		r.startSpinner("") // spinner while tool executes

	case agent.EventToolResult:
		r.stopSpinner()
		if strings.HasPrefix(ev.Result, "DIFF:") {
			r.renderDiff(ev.Result)
		} else {
			// Enhanced tool result rendering
			r.renderToolResult(ev.Result)
		}
		r.startSpinner("") // spinner while waiting for next LLM response

	case agent.EventDone:
		r.stopSpinner()
		r.flushLine()
		fmt.Printf("%s\n", styleDone.Render("✓"))

	case agent.EventError:
		r.stopSpinner()
		r.flushLine()
		fmt.Printf("%s %v\n", styleError.Render("✗"), ev.Error)
	}
}

// StartWaiting starts the spinner before the first token (called when agent starts).
func (r *Renderer) StartWaiting() {
	r.startSpinner("")
}

func (r *Renderer) flushLine() {
	line := r.lineBuf.String()
	r.lineBuf.Reset()
	if line == "" {
		return
	}
	fmt.Print(r.renderLine(line))
}

func (r *Renderer) renderLine(line string) string {
	// code block fence toggle
	if strings.HasPrefix(line, "```") {
		r.inCodeBlock = !r.inCodeBlock
		return stylePre.Render(line)
	}
	if r.inCodeBlock {
		return stylePre.Render(line)
	}

	// headers
	if strings.HasPrefix(line, "#### ") {
		return styleBold.Render(line[5:])
	}
	if strings.HasPrefix(line, "### ") {
		return styleH3.Render(line[4:])
	}
	if strings.HasPrefix(line, "## ") {
		return styleH2.Render(line[3:])
	}
	if strings.HasPrefix(line, "# ") {
		return styleH1.Render(line[2:])
	}

	// horizontal rule
	if strings.TrimRight(line, "-") == "" && len(line) >= 3 {
		return styleDim.Render(strings.Repeat("─", min(termWidth(), 60)))
	}

	// bullet points
	if strings.HasPrefix(line, "- ") {
		return "  • " + applyInline(line[2:])
	}
	if strings.HasPrefix(line, "* ") {
		return "  • " + applyInline(line[2:])
	}

	return applyInline(line)
}

func applyInline(s string) string {
	// bold before italic to avoid double-matching
	s = reBold.ReplaceAllStringFunc(s, func(m string) string {
		return styleBold.Render(m[2 : len(m)-2])
	})
	s = reItalic.ReplaceAllStringFunc(s, func(m string) string {
		return lipgloss.NewStyle().Italic(true).Render(m[1 : len(m)-1])
	})
	s = reCode.ReplaceAllStringFunc(s, func(m string) string {
		return styleCode.Render(m[1 : len(m)-1])
	})
	return s
}

// ── spinner ───────────────────────────────────────────────────────────────────

type spinner struct {
	stop chan struct{}
	done chan struct{}
}

func (r *Renderer) startSpinner(label string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.spinner != nil {
		return
	}
	s := &spinner{stop: make(chan struct{}), done: make(chan struct{})}
	r.spinner = s
	go func() {
		defer close(s.done)
		frames := []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}
		i := 0
		for {
			select {
			case <-s.stop:
				fmt.Print("\r\033[K") // clear spinner line
				return
			case <-time.After(80 * time.Millisecond):
				f := styleDim.Render(frames[i%len(frames)])
				if label != "" {
					fmt.Printf("\r%s %s", f, styleDim.Render(label))
				} else {
					fmt.Printf("\r%s", f)
				}
				i++
			}
		}
	}()
}

func (r *Renderer) stopSpinner() {
	r.mu.Lock()
	s := r.spinner
	r.spinner = nil
	r.mu.Unlock()
	if s != nil {
		close(s.stop)
		<-s.done // wait for clear to finish before printing
	}
}

// ── diff renderer ─────────────────────────────────────────────────────────────

func (r *Renderer) renderToolResult(result string) {
	result = strings.TrimSpace(result)
	if result == "" {
		return
	}

	lines := strings.SplitN(result, "\n", 2)
	firstLine := lines[0]

	// Detect result type and render accordingly
	if strings.HasPrefix(firstLine, "error:") {
		// Error result
		fmt.Printf("  %s %s\n", styleToolError.Render("✗"), styleToolError.Render(firstLine))
		if len(lines) > 1 {
			detail := strings.TrimSpace(lines[1])
			if detail != "" && len(detail) < 100 {
				fmt.Printf("    %s\n", styleMeta.Render(detail))
			}
		}
	} else if strings.HasPrefix(firstLine, "found") && strings.Contains(firstLine, "match") {
		// Search/glob result
		fmt.Printf("  %s %s\n", styleToolSearch.Render("🔍"), styleToolSearch.Render(firstLine))
		if len(lines) > 1 {
			// Show matches compactly
			matchLines := strings.Split(strings.TrimSpace(lines[1]), "\n")
			maxShow := 5
			for i, m := range matchLines {
				if i >= maxShow {
					fmt.Printf("    %s\n", styleMeta.Render(fmt.Sprintf("… and %d more", len(matchLines)-i)))
					break
				}
				fmt.Printf("    %s\n", styleFilePath.Render(m))
			}
		}
	} else if strings.HasPrefix(firstLine, "no ") && strings.Contains(firstLine, "found") {
		// Not found result
		fmt.Printf("  %s %s\n", styleMeta.Render("○"), styleMeta.Render(firstLine))
	} else if strings.HasPrefix(firstLine, "read ") {
		// File read result
		fmt.Printf("  %s %s\n", styleToolRead.Render("📖"), styleToolRead.Render(firstLine))
		if len(lines) > 1 {
			content := strings.TrimSpace(lines[1])
			preview := r.previewContent(content, 10)
			if preview != "" {
				for _, line := range strings.Split(preview, "\n") {
					fmt.Printf("    %s\n", stylePre.Render(line))
				}
			}
		}
	} else if strings.HasPrefix(firstLine, "[") && strings.Contains(firstLine, "]") {
		// JSON result (symbol lookup, etc)
		fmt.Printf("  %s %s\n", styleToolSearch.Render("⚙"), styleToolSearch.Render("symbol found"))
		// Show compact preview
		if len(result) < 200 {
			fmt.Printf("    %s\n", stylePre.Render(result))
		} else {
			fmt.Printf("    %s\n", styleMeta.Render("(use read_file or search for details)"))
		}
	} else {
		// Generic result
		fmt.Printf("  %s %s\n", styleDim.Render("◀"), styleResult.Render(firstLine))
		if len(lines) > 1 {
			detail := strings.TrimSpace(lines[1])
			if detail != "" && len(detail) < 150 {
				for _, line := range strings.Split(detail, "\n")[:5] {
					fmt.Printf("    %s\n", styleMeta.Render(line))
				}
			}
		}
	}
}

// previewContent shows first N lines of content
func (r *Renderer) previewContent(content string, maxLines int) string {
	lines := strings.Split(content, "\n")
	if len(lines) > maxLines {
		lines = lines[:maxLines]
		lines = append(lines, fmt.Sprintf("… (%d more lines)", strings.Count(content, "\n")-maxLines))
	}
	return strings.Join(lines, "\n")
}

// ── diff renderer ─────────────────────────────────────────────────────────────

func (r *Renderer) renderDiff(result string) {
	lines := strings.SplitN(result, "\n", 2)
	header := strings.TrimPrefix(lines[0], "DIFF:")
	fmt.Printf("%s %s\n", styleDim.Render("◀"), styleAction.Render(header))

	if len(lines) < 2 || strings.TrimSpace(lines[1]) == "" {
		return
	}

	tw := termWidth()
	diffLines := strings.Split(strings.TrimRight(lines[1], "\n"), "\n")
	shown := 0
	for _, line := range diffLines {
		if line == "" {
			continue
		}
		if shown >= 60 {
			fmt.Println(styleDim.Render("  … (truncated)"))
			break
		}
		switch line[0] {
		case '+':
			text := padOrTrunc("+ "+line[1:], tw)
			fmt.Println(styleDiffAdded.Render(text))
		case '-':
			text := padOrTrunc("- "+line[1:], tw)
			fmt.Println(styleDiffRemoved.Render(text))
		default:
			fmt.Println(styleDiffCtx.Render("  " + line[1:]))
		}
		shown++
	}
}

// padOrTrunc pads s to width w (for full-width background color blocks).
func padOrTrunc(s string, w int) string {
	if w <= 0 {
		return s
	}
	if len(s) < w {
		return s + strings.Repeat(" ", w-len(s))
	}
	return s[:w]
}

// ── helpers ───────────────────────────────────────────────────────────────────

func PrintBanner(model, workdir string, agents int) {
	line := fmt.Sprintf("x10  model=%s  dir=%s", model, workdir)
	if agents > 1 {
		line += fmt.Sprintf("  agents=%d", agents)
	}
	fmt.Println(styleAgent.Render(line))
	fmt.Println(styleDim.Render(strings.Repeat("─", min(termWidth(), 60))))
}

func PrintPrompt() {
	fmt.Print(styleDim.Render("\n> "))
}

// PrintImageInput erases the raw image path the user typed and replaces it with
// a clean display: the blank line + "> ◈ filename.png  task text"
func PrintImageInput(origInput, fname, taskText string) {
	tw := termWidth()
	// Lines occupied by "> " (2 chars) + the typed input
	inputLines := (2 + len(origInput) + tw - 1) / tw
	// +1 for the blank line PrintPrompt emits via its leading \n
	totalLines := inputLines + 1

	for i := 0; i < totalLines; i++ {
		fmt.Print("\033[1A\033[2K") // cursor up 1, clear line
	}

	tag := styleImg.Render("◈ " + fname)
	task := styleDim.Render(taskText)
	fmt.Printf("\n%s %s  %s\n", styleDim.Render(">"), tag, task)
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	lines := strings.SplitN(s, "\n", 2)
	line := strings.TrimSpace(lines[0])
	if len(lines) > 1 && strings.TrimSpace(lines[1]) != "" {
		line += styleDim.Render(fmt.Sprintf(" (+%d lines)", strings.Count(s, "\n")))
	}
	return line
}

func termWidth() int {
	w, _, err := term.GetSize(int(os.Stdout.Fd()))
	if err != nil || w <= 0 {
		return 100
	}
	return w
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
