// Package local manages on-device SLM inference for x10.
//
// On first use of a local model x10:
//  1. Downloads the llama.cpp server binary for the current platform
//     (~15 MB, cached in ~/.x10/bin/).
//  2. Downloads the GGUF model file from HuggingFace
//     (cached in ~/.x10/models/).
//  3. Starts llama-server as a background process on a fixed local port.
//  4. Returns an OpenAI-compatible base URL so the rest of x10 works unchanged.
package local

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

const (
	serverPort = 18080
	serverBase = "http://127.0.0.1:18080/v1"
	hfBase     = "https://huggingface.co"
	ghAPI      = "https://api.github.com/repos/ggerganov/llama.cpp/releases/latest"
)

// EnsureReady guarantees the local server is running with the requested model
// and returns the OpenAI-compat base URL + actual model name.
func EnsureReady(ctx context.Context, modelID string) (apiBase, modelName string, err error) {
	m, ok := FindModel(modelID)
	if !ok {
		return "", "", fmt.Errorf("unknown local model %q\n  run: x10 models list", modelID)
	}

	binPath, err := ensureBinary(ctx)
	if err != nil {
		return "", "", fmt.Errorf("llama-server: %w", err)
	}

	modelPath, err := ensureModel(ctx, m)
	if err != nil {
		return "", "", fmt.Errorf("model %s: %w", m.Name, err)
	}

	if err := ensureServer(ctx, binPath, modelPath, m.CtxLen); err != nil {
		return "", "", fmt.Errorf("server: %w", err)
	}

	return serverBase, m.ID, nil
}

// ── binary management ─────────────────────────────────────────────────────────

func binDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".x10", "bin")
}

func serverBinPath() string {
	name := "llama-server"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	return filepath.Join(binDir(), name)
}

func ensureBinary(ctx context.Context) (string, error) {
	p := serverBinPath()
	if fileExists(p) {
		return p, nil
	}
	fmt.Println("  downloading llama-server (one-time, ~15 MB)...")
	return p, downloadBinary(ctx, p)
}

func downloadBinary(ctx context.Context, dest string) error {
	// Resolve the latest llama.cpp release asset URL via GitHub API
	assetURL, err := latestBinaryURL(ctx)
	if err != nil {
		return fmt.Errorf("resolve release: %w", err)
	}

	data, err := download(ctx, assetURL, "llama-server")
	if err != nil {
		return err
	}

	// The release is a zip containing the binary
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return fmt.Errorf("unzip: %w", err)
	}

	binName := "llama-server"
	if runtime.GOOS == "windows" {
		binName += ".exe"
	}

	for _, f := range zr.File {
		if filepath.Base(f.Name) == binName {
			if err := os.MkdirAll(filepath.Dir(dest), 0755); err != nil {
				return err
			}
			rc, err := f.Open()
			if err != nil {
				return err
			}
			defer rc.Close()
			out, err := os.OpenFile(dest, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0755)
			if err != nil {
				return err
			}
			defer out.Close()
			_, err = io.Copy(out, rc)
			return err
		}
	}
	return fmt.Errorf("llama-server binary not found in release archive")
}

func latestBinaryURL(ctx context.Context) (string, error) {
	req, _ := http.NewRequestWithContext(ctx, "GET", ghAPI, nil)
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	var rel struct {
		Assets []struct {
			Name               string `json:"name"`
			BrowserDownloadURL string `json:"browser_download_url"`
		} `json:"assets"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&rel); err != nil {
		return "", err
	}

	suffix := platformAssetSuffix()
	if suffix == "" {
		return "", fmt.Errorf("unsupported platform %s/%s", runtime.GOOS, runtime.GOARCH)
	}

	for _, a := range rel.Assets {
		if strings.Contains(a.Name, suffix) && strings.HasSuffix(a.Name, ".zip") {
			return a.BrowserDownloadURL, nil
		}
	}
	return "", fmt.Errorf("no asset found for platform %s", suffix)
}

func platformAssetSuffix() string {
	switch {
	case runtime.GOOS == "darwin" && runtime.GOARCH == "arm64":
		return "macos-arm64"
	case runtime.GOOS == "darwin" && runtime.GOARCH == "amd64":
		return "macos-x86_64"
	case runtime.GOOS == "linux" && runtime.GOARCH == "amd64":
		return "ubuntu-x64"
	case runtime.GOOS == "linux" && runtime.GOARCH == "arm64":
		return "ubuntu-arm64"
	case runtime.GOOS == "windows" && runtime.GOARCH == "amd64":
		return "win-avx2-x64"
	}
	return ""
}

// ── model management ──────────────────────────────────────────────────────────

func modelsDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".x10", "models")
}

func modelPath(m Model) string {
	return filepath.Join(modelsDir(), m.HFFile)
}

func ensureModel(ctx context.Context, m Model) (string, error) {
	p := modelPath(m)
	if fileExists(p) {
		return p, nil
	}
	fmt.Printf("  downloading %s (%s) — first run only...\n", m.Name, m.Size)
	return p, downloadModel(ctx, m, p)
}

func downloadModel(ctx context.Context, m Model, dest string) error {
	url := fmt.Sprintf("%s/%s/resolve/main/%s", hfBase, m.HFRepo, m.HFFile)
	data, err := downloadWithProgress(ctx, url, m.Name)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0755); err != nil {
		return err
	}
	return os.WriteFile(dest, data, 0644)
}

// ── server management ─────────────────────────────────────────────────────────

func ensureServer(ctx context.Context, binPath, modelPath string, ctxLen int) error {
	if isServerRunning() {
		return nil
	}

	args := []string{
		"-m", modelPath,
		"--port", strconv.Itoa(serverPort),
		"--ctx-size", strconv.Itoa(ctxLen),
		"--parallel", "1",
		"--no-mmap",
		"--log-disable",
	}

	cmd := exec.CommandContext(context.Background(), binPath, args...)
	cmd.Stdout = nil
	cmd.Stderr = nil
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start llama-server: %w", err)
	}

	// Wait up to 30 seconds for server to be ready
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		time.Sleep(500 * time.Millisecond)
		if isServerRunning() {
			return nil
		}
	}
	return fmt.Errorf("llama-server did not become ready within 30s")
}

func isServerRunning() bool {
	resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/health", serverPort))
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode == 200
}

// ── download helpers ──────────────────────────────────────────────────────────

func download(ctx context.Context, url, label string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("HTTP %d for %s", resp.StatusCode, label)
	}
	return io.ReadAll(resp.Body)
}

func downloadWithProgress(ctx context.Context, url, label string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("HTTP %d downloading %s", resp.StatusCode, label)
	}

	total := resp.ContentLength
	var buf bytes.Buffer
	tmp := make([]byte, 32*1024)
	var received int64
	for {
		n, err := resp.Body.Read(tmp)
		if n > 0 {
			buf.Write(tmp[:n])
			received += int64(n)
			if total > 0 {
				pct := int(100 * received / total)
				fmt.Printf("\r  %s: %d%%", label, pct)
			}
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
	}
	fmt.Println()
	return buf.Bytes(), nil
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}
