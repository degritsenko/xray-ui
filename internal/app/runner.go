package app

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

type Core interface {
	Apply(context.Context, []byte, func() error) error
	Restart(context.Context, []byte, func() error) error
	Stop()
	Running() bool
}
type coreProcess struct {
	cmd    *exec.Cmd
	done   chan struct{}
	config []byte
	dir    string
}
type Runner struct {
	mu          sync.Mutex
	binary, dir string
	output      io.Writer
	grace       time.Duration
	current     *coreProcess
	status      atomic.Pointer[coreProcess]
}

func NewRunner(binary, dir string, output io.Writer, grace time.Duration) *Runner {
	if grace <= 0 {
		grace = time.Second
	}
	return &Runner{binary: binary, dir: dir, output: output, grace: grace}
}
func running(p *coreProcess) bool {
	if p == nil {
		return false
	}
	select {
	case <-p.done:
		return false
	default:
		return true
	}
}
func (r *Runner) Running() bool { return running(r.status.Load()) }
func (r *Runner) pid() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !running(r.current) {
		return 0
	}
	return r.current.cmd.Process.Pid
}
func stopProcess(p *coreProcess) {
	if running(p) {
		_ = p.cmd.Process.Signal(syscall.SIGTERM)
		select {
		case <-p.done:
		case <-time.After(3 * time.Second):
			_ = p.cmd.Process.Kill()
			<-p.done
		}
	}
}
func (r *Runner) Stop() {
	r.mu.Lock()
	defer r.mu.Unlock()
	stopProcess(r.current)
	if r.current != nil {
		_ = os.RemoveAll(r.current.dir)
	}
	r.current = nil
	r.status.Store(nil)
}
func (r *Runner) prepare(data []byte) (string, error) {
	if err := os.MkdirAll(r.dir, 0700); err != nil {
		return "", errors.New("could not create Xray directory")
	}
	dir, err := os.MkdirTemp(r.dir, ".process-")
	if err != nil {
		return "", errors.New("could not prepare Xray configuration")
	}
	if err = os.WriteFile(filepath.Join(dir, "config.json"), data, 0600); err != nil {
		_ = os.RemoveAll(dir)
		return "", errors.New("could not save Xray configuration")
	}
	return dir, nil
}
func (r *Runner) startPrepared(ctx context.Context, dir string, data []byte) (*coreProcess, error) {
	// No file writes here: a rollback must work even when storage became full.
	cmd := exec.Command(r.binary, "run", "-config", filepath.Join(dir, "config.json"))
	cmd.Stdout = r.output
	cmd.Stderr = r.output
	if cmd.Start() != nil {
		return nil, errors.New("could not start Xray")
	}
	p := &coreProcess{cmd: cmd, done: make(chan struct{}), config: append([]byte(nil), data...), dir: dir}
	go func() { _ = cmd.Wait(); close(p.done) }()
	select {
	case <-p.done:
		return nil, errors.New("new Xray process exited during startup")
	case <-ctx.Done():
		stopProcess(p)
		return nil, ctx.Err()
	case <-time.After(r.grace):
		return p, nil
	}
}
func (r *Runner) Apply(ctx context.Context, data []byte, commit func() error) error {
	return r.apply(ctx, data, commit, false)
}
func (r *Runner) Restart(ctx context.Context, data []byte, commit func() error) error {
	return r.apply(ctx, data, commit, true)
}
func (r *Runner) apply(ctx context.Context, data []byte, commit func() error, force bool) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if !force && running(r.current) && bytes.Equal(data, r.current.config) {
		return commit()
	}
	// Prepare the actual launch file before touching the accepted process. The
	// .json extension is required by Xray's configuration format detection.
	dir, err := r.prepare(data)
	if err != nil {
		return err
	}
	accepted := false
	defer func() {
		if !accepted {
			_ = os.RemoveAll(dir)
		}
	}()
	validationCtx, cancelValidation := context.WithTimeout(ctx, 10*time.Second)
	validation := exec.CommandContext(validationCtx, r.binary, "run", "-test", "-config", filepath.Join(dir, "config.json"))
	err = validation.Run()
	cancelValidation()
	if err != nil {
		return errors.New("Xray rejected configuration; current connection retained")
	}
	old := r.current
	wasRunning := running(old)
	stopProcess(old)
	r.status.Store(nil)
	next, applyErr := r.startPrepared(ctx, dir, data)
	if applyErr == nil {
		applyErr = commit()
	}
	if applyErr != nil {
		stopProcess(next)
		r.current = nil
		if wasRunning {
			restoreCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			restored, restoreErr := r.startPrepared(restoreCtx, old.dir, old.config)
			cancel()
			r.current = restored
			r.status.Store(restored)
			if restoreErr != nil {
				return errors.New("could not apply or restore Xray; start it from the panel")
			}
		} else if old != nil {
			_ = os.RemoveAll(old.dir)
		}
		return errors.New("new settings were not applied; previous settings retained")
	}
	r.current = next
	r.status.Store(next)
	accepted = true
	if old != nil {
		_ = os.RemoveAll(old.dir)
	}
	return nil
}
