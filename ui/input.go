package ui

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/term"
)

// ErrInterrupt is returned from ReadLine when the user presses Ctrl+C / Ctrl+D.
var ErrInterrupt = errors.New("interrupt")

// ReadLine reads one line of input with raw-mode image-path detection.
//
// When the user drags an image file from Finder (or pastes a path ending in
// .png/.jpg/.jpeg/.gif/.webp that exists on disk), the raw path is replaced
// inline with a clean "◈ filename.png" badge.
//
// Backspace removes the last typed character; when only the image badge
// remains, the next backspace removes the entire attachment.
//
// Returns the raw input string (image paths still present) so that the
// caller can pass it to injectImageFromPath as before.
func ReadLine() (string, error) {
	fd := int(os.Stdin.Fd())
	if !term.IsTerminal(fd) {
		return readLineBasic()
	}

	old, err := term.MakeRaw(fd)
	if err != nil {
		// Can't get raw mode — fall back gracefully
		fmt.Print(styleDim.Render("\n> "))
		return readLineBasic()
	}
	defer term.Restore(fd, old)

	// blank line above prompt (mirrors PrintPrompt behaviour)
	fmt.Print("\n")

	// buf holds the full raw text (including any image path)
	var buf []rune
	// imgRawPath is the raw escaped path found in buf; imgName is its basename
	var imgRawPath, imgName string

	draw := func() {
		tw := termWidth()
		rawLen := 2 + len(buf) // "> " + text (visual estimate without ANSI)
		linesUsed := (rawLen + tw - 1) / tw
		if linesUsed < 1 {
			linesUsed = 1
		}
		// go up to cover all wrapped lines, then clear to end of screen
		for i := 1; i < linesUsed; i++ {
			fmt.Print("\033[1A")
		}
		fmt.Print("\r\033[J")

		if imgName != "" {
			// replace raw path in display with styled badge
			display := strings.Replace(string(buf), imgRawPath,
				styleImg.Render("◈ "+imgName), 1)
			fmt.Printf("%s %s", styleDim.Render(">"), display)
		} else {
			fmt.Printf("%s %s", styleDim.Render(">"), string(buf))
		}
	}

	draw() // initial prompt

	r := bufio.NewReader(os.Stdin)
	for {
		ch, _, err := r.ReadRune()
		if err != nil {
			return "", err
		}

		switch ch {
		case '\r', '\n': // Enter
			fmt.Print("\r\n")
			return strings.TrimSpace(string(buf)), nil

		case 3, 4: // Ctrl+C, Ctrl+D
			fmt.Print("\r\n")
			return "", ErrInterrupt

		case 127, 8: // Backspace / Delete
			if len(buf) > 0 {
				buf = buf[:len(buf)-1]
				// re-check whether image path still present
				if imgRawPath != "" && !strings.Contains(string(buf), imgRawPath) {
					imgRawPath = ""
					imgName = ""
				}
			} else if imgRawPath != "" {
				buf = nil
				imgRawPath = ""
				imgName = ""
			}
			draw()

		case 27: // ESC / escape sequence (arrow keys, etc.) — consume and ignore
			next, _ := r.ReadByte()
			if next == '[' || next == 'O' {
				r.ReadByte() // consume the final byte of a 3-byte sequence
			}
			// don't redraw — no visual change

		default:
			if ch < 32 {
				continue // ignore other control chars
			}
			buf = append(buf, ch)

			// Only search for image path if we haven't found one yet
			if imgRawPath == "" {
				if raw, name := detectImagePath(string(buf)); name != "" {
					imgRawPath = raw
					imgName = name
				}
			}
			draw()
		}
	}
}

// detectImagePath scans text for a complete image file path that exists on
// disk. Returns (rawPath, basename) or ("","") if nothing found.
func detectImagePath(text string) (rawPath, basename string) {
	exts := []string{".png", ".jpg", ".jpeg", ".gif", ".webp"}
	lower := strings.ToLower(text)
	for _, ext := range exts {
		idx := strings.LastIndex(lower, ext)
		if idx == -1 {
			continue
		}
		end := idx + len(ext)
		// must be at end of text, or followed by a space (not mid-word)
		if end < len(text) && text[end] != ' ' && text[end] != '\t' {
			continue
		}
		// walk left to find start of path, honouring shell-escaped spaces "\ "
		start := 0
		for i := idx - 1; i >= 0; i-- {
			c := text[i]
			if c == ' ' || c == '\t' {
				if i > 0 && text[i-1] == '\\' {
					i-- // skip backslash too (loop will decrement once more)
					continue
				}
				start = i + 1
				break
			}
		}
		raw := text[start:end]
		candidate := strings.ReplaceAll(raw, "\\ ", " ")
		if _, err := os.Stat(candidate); err == nil {
			return raw, filepath.Base(candidate)
		}
	}
	return "", ""
}

// readLineBasic is a fallback for non-TTY stdin (piped input, etc.).
func readLineBasic() (string, error) {
	scanner := bufio.NewScanner(os.Stdin)
	if scanner.Scan() {
		return strings.TrimSpace(scanner.Text()), nil
	}
	if err := scanner.Err(); err != nil {
		return "", err
	}
	return "", ErrInterrupt
}
