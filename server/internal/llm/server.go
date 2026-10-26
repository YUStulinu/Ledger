package llm

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"
)

// ServerConfig says where llama-server and the model are.
type ServerConfig struct {
	Binary string // llama-server executable
	Model  string // .gguf file
	Port   int
	// Context size in tokens, and how many layers go to the GPU. Zero leaves the layers to
	// llama.cpp, which fits as many as the free GPU memory holds: forcing them all onto a GPU that
	// is short of memory makes Windows spill into shared system memory, at a tenth of the speed.
	Context   int
	GPULayers int
	Log       string // where the server's output goes ("" discards it)
}

// Server is a llama-server process started by Ledger.
type Server struct {
	*Client
	cmd *exec.Cmd
}

// DefaultBinary looks for llama-server under tools/llama.cpp (scripts/setup-llama.sh puts it there),
// then on the PATH.
func DefaultBinary(root string) string {
	name := "llama-server"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	if p := filepath.Join(root, "tools", "llama.cpp", name); fileExists(p) {
		return p
	}
	if p, err := exec.LookPath(name); err == nil {
		return p
	}
	return filepath.Join(root, "tools", "llama.cpp", name)
}

func fileExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
}

// Start launches llama-server and waits until the model is loaded. If a server already answers
// on the port, it is used as is.
func Start(ctx context.Context, cfg ServerConfig) (*Server, error) {
	if cfg.Port == 0 {
		cfg.Port = 8091
	}
	if cfg.Context == 0 {
		cfg.Context = 4096
	}
	client := NewClient(fmt.Sprintf("http://127.0.0.1:%d", cfg.Port))
	if client.Healthy(ctx) {
		return &Server{Client: client}, nil
	}
	if !fileExists(cfg.Binary) {
		return nil, fmt.Errorf("llama-server not found at %s (see scripts/setup-llama.sh)", cfg.Binary)
	}
	if !fileExists(cfg.Model) {
		return nil, fmt.Errorf("model not found at %s (see scripts/setup-model.sh)", cfg.Model)
	}
	if ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", cfg.Port)); err != nil {
		return nil, fmt.Errorf("port %d is taken by something that is not llama-server", cfg.Port)
	} else {
		ln.Close()
	}
	args := []string{"-m", cfg.Model, "--host", "127.0.0.1", "--port", fmt.Sprint(cfg.Port), "-c", fmt.Sprint(cfg.Context),
		"--parallel", "1", "--no-webui"}
	if cfg.GPULayers > 0 {
		args = append(args, "-ngl", fmt.Sprint(cfg.GPULayers))
	}
	cmd := exec.Command(cfg.Binary, args...)
	if cfg.Log != "" {
		f, err := os.Create(cfg.Log)
		if err != nil {
			return nil, err
		}
		cmd.Stdout, cmd.Stderr = f, f
	}
	tieToParent(cmd)
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("starting llama-server: %w", err)
	}
	afterStart(cmd)
	s := &Server{Client: client, cmd: cmd}
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()
	deadline := time.Now().Add(3 * time.Minute)
	for time.Now().Before(deadline) {
		select {
		case err := <-exited:
			return nil, fmt.Errorf("llama-server exited while loading the model: %v (see %s)", err, cfg.Log)
		case <-ctx.Done():
			s.Stop()
			return nil, ctx.Err()
		case <-time.After(300 * time.Millisecond):
		}
		if client.Healthy(ctx) {
			return s, nil
		}
	}
	s.Stop()
	return nil, fmt.Errorf("llama-server did not become ready in 3 minutes")
}

// Stop ends the server if Ledger started it.
func (s *Server) Stop() {
	if s.cmd != nil && s.cmd.Process != nil {
		s.cmd.Process.Kill()
	}
}
