package runner

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"liberty.local/liberty/internal/domain"
	"liberty.local/liberty/internal/infrastructure/diagnostics"
)

var (
	ErrProcess = errors.New("runner_process_failed")
	ErrStart   = errors.New("runner_process_start_failed")
	ErrOutput  = errors.New("runner_output_invalid")
)

const MaxEventBytes = 16 * 1024
const MaxResultBytes = 32 * 1024 * 1024

type CommandSpec struct {
	Python, Script, Input, Directory, FFmpeg, Models string
	ModelPaths                                       ModelPaths
	Language, Device, Backend                        string
	Speaker                                          bool
	Hotwords                                         []string
	Threads                                          uint32
	BatchSeconds                                     uint32
}
type Hooks struct {
	// Callbacks are serialized, must return promptly, and never run on the UI
	// thread. An error cancels the process tree and prevents publishing a result.
	Started    func(uint32) error
	Event      func(Event) error
	Diagnostic func(string) error
	Heartbeat  func() error
	Progress   func(Progress) error
}

func (s CommandSpec) command(ctx context.Context) (*exec.Cmd, error) {
	for _, path := range []string{s.Python, s.Script, s.Input} {
		if !filepath.IsAbs(path) {
			return nil, ErrStart
		}
		info, err := os.Stat(path)
		if err != nil || !info.Mode().IsRegular() {
			return nil, ErrStart
		}
	}
	if !filepath.IsAbs(s.Directory) {
		return nil, ErrStart
	}
	info, err := os.Lstat(s.Directory)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, ErrStart
	}
	if s.FFmpeg != "" && !filepath.IsAbs(s.FFmpeg) {
		return nil, ErrStart
	}
	if s.Models != "" && !filepath.IsAbs(s.Models) {
		return nil, ErrStart
	}
	cmd := exec.CommandContext(ctx, s.Python, s.Script, "--job-dir", s.Directory, "--input", s.Input, "--lang", s.Language, "--speaker", strconv.FormatBool(s.Speaker), "--hotwords", strings.Join(s.Hotwords, ","))
	cmd.Dir = s.Directory
	device := s.Device
	switch device {
	case "cpu", "mps", "cuda":
	default:
		device = "auto"
	}
	overrides := map[string]string{"OMP_NUM_THREADS": strconv.FormatUint(uint64(max(1, s.Threads)), 10), "MKL_NUM_THREADS": strconv.FormatUint(uint64(max(1, s.Threads)), 10), "NUMEXPR_NUM_THREADS": strconv.FormatUint(uint64(max(1, s.Threads)), 10), "KMP_DUPLICATE_LIB_OK": "TRUE", "LIBERTY_ASR_BACKEND": s.Backend, "FUNASR_DEVICE": device, "FUNASR_BATCH_SIZE_S": strconv.FormatUint(uint64(s.BatchSeconds), 10)}
	if s.FFmpeg != "" {
		overrides["LIBERTY_FFMPEG_PATH"] = s.FFmpeg
	}
	if s.Models != "" {
		if s.Backend != "sherpa-onnx" {
			models, err := s.ModelPaths.Environment(s.Models)
			if err != nil {
				return nil, err
			}
			for key, value := range models {
				overrides[key] = value
			}
		}
		for key, child := range map[string]string{"MODELSCOPE_CACHE": "modelscope", "HF_HOME": "huggingface", "TORCH_HOME": "torch"} {
			overrides[key] = filepath.Join(s.Models, child)
		}
		for _, key := range []string{"MODELSCOPE_OFFLINE", "HF_HUB_OFFLINE", "TRANSFORMERS_OFFLINE"} {
			overrides[key] = "1"
		}
	}
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		if _, exists := overrides[key]; !exists {
			cmd.Env = append(cmd.Env, entry)
		}
	}
	for key, value := range overrides {
		cmd.Env = append(cmd.Env, key+"="+value)
	}
	return cmd, nil
}

type streamLine struct {
	data   []byte
	stderr bool
	err    error
}

// lineWriter bounds memory even for a child that never emits a newline. Stderr
// truncates at the byte limit and discards the rest of that line; stdout fails.
type lineWriter struct {
	data      []byte
	truncated bool
	stderr    bool
	emit      func(streamLine) error
}

func (w *lineWriter) Write(p []byte) (int, error) {
	for _, b := range p {
		if b == '\n' {
			if err := w.flush(); err != nil {
				return 0, err
			}
			continue
		}
		if len(w.data) < MaxEventBytes {
			w.data = append(w.data, b)
		} else if !w.stderr {
			_ = w.emit(streamLine{err: ErrProtocol})
			return 0, ErrProtocol
		} else {
			w.truncated = true
		}
	}
	return len(p), nil
}
func (w *lineWriter) flush() error {
	data := strings.TrimSuffix(string(w.data), "\r")
	if w.stderr && w.truncated {
		data += " [truncated]"
	}
	w.data = nil
	w.truncated = false
	if data == "" {
		return nil
	}
	return w.emit(streamLine{data: []byte(data), stderr: w.stderr})
}

// Execute owns one process tree and reads only its attempt directory. Result
// publication remains an application/repository operation guarded by the lease.
func Execute(ctx context.Context, s CommandSpec, h Hooks) (domain.RunnerResult, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	cmd, err := s.command(ctx)
	if err != nil {
		return domain.RunnerResult{}, err
	}
	root, err := os.OpenRoot(s.Directory)
	if err != nil {
		return domain.RunnerResult{}, ErrOutput
	}
	defer root.Close()
	control, err := prepareProcess(cmd)
	if err != nil {
		return domain.RunnerResult{}, ErrStart
	}
	defer control.close()
	events := make(chan streamLine, 32)
	emit := func(line streamLine) error {
		select {
		case events <- line:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	out, diagnostic := &lineWriter{emit: emit}, &lineWriter{emit: emit, stderr: true}
	cmd.Stdout = out
	cmd.Stderr = diagnostic
	cmd.Cancel = control.stop
	cmd.WaitDelay = time.Second
	if err = cmd.Start(); err != nil {
		return domain.RunnerResult{}, ErrStart
	}
	if err = control.started(cmd.Process); err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return domain.RunnerResult{}, ErrStart
	}
	// Wait is always joined, including callback/protocol/cancellation failures.
	done := make(chan error, 1)
	go func() {
		waitErr := cmd.Wait()
		if err := out.flush(); err != nil && waitErr == nil {
			waitErr = err
		}
		if err := diagnostic.flush(); err != nil && waitErr == nil {
			waitErr = err
		}
		done <- waitErr
	}()
	stopAndWait := func(cause error) (domain.RunnerResult, error) {
		cancel()
		stopErr := control.stop()
		<-done
		if stopErr != nil {
			return domain.RunnerResult{}, domain.ErrTerminationUnverified
		}
		return domain.RunnerResult{}, cause
	}
	if h.Started != nil {
		if err := h.Started(uint32(cmd.Process.Pid)); err != nil {
			return stopAndWait(err)
		}
	}
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	var fence ProgressFence
	apply := func(line streamLine) error {
		if line.err != nil {
			return line.err
		}
		if line.stderr {
			if h.Diagnostic != nil {
				return h.Diagnostic(diagnostics.Text(string(line.data), 4096))
			}
			return nil
		}
		if !utf8.Valid(line.data) {
			return ErrProtocol
		}
		event, err := ParseEvent(line.data)
		if err != nil {
			return err
		}
		event.Message = diagnostics.Text(event.Message, 4096)
		if event.Timestamp != nil {
			safe := diagnostics.Text(*event.Timestamp, 128)
			event.Timestamp = &safe
		}
		if h.Event != nil {
			return h.Event(event)
		}
		return nil
	}
	for {
		select {
		case <-ctx.Done():
			return stopAndWait(ctx.Err())
		case line := <-events:
			if err := apply(line); err != nil {
				return stopAndWait(err)
			}
		case <-ticker.C:
			if h.Heartbeat != nil {
				if err := h.Heartbeat(); err != nil {
					return stopAndWait(err)
				}
			}
			if h.Progress != nil {
				if data, err := readOutput(root, "progress.json", MaxEventBytes); err == nil {
					p, err := ParseProgress(data)
					if err == nil && fence.Accept(p) == nil {
						if p.StatusMessage != nil {
							value := diagnostics.Text(*p.StatusMessage, 4096)
							p.StatusMessage = &value
						}
						if p.FailureReason != nil {
							value := diagnostics.Text(*p.FailureReason, 4096)
							p.FailureReason = &value
						}
						if err := h.Progress(p); err != nil {
							return stopAndWait(err)
						}
					}
				}
			}
		case err := <-done:
			if control.stop() != nil {
				return domain.RunnerResult{}, domain.ErrTerminationUnverified
			}
			for len(events) > 0 {
				if err := apply(<-events); err != nil {
					return domain.RunnerResult{}, err
				}
			}
			if ctx.Err() != nil {
				return domain.RunnerResult{}, ctx.Err()
			}
			if err != nil {
				if errors.Is(err, ErrProtocol) {
					return domain.RunnerResult{}, ErrProtocol
				}
				return domain.RunnerResult{}, ErrProcess
			}
			data, err := readOutput(root, "result.json", MaxResultBytes)
			if err != nil {
				return domain.RunnerResult{}, err
			}
			return ParseResult(data, s.Speaker, s.Backend)
		}
	}
}
func readOutput(root *os.Root, name string, limit int64) ([]byte, error) {
	info, err := root.Lstat(name)
	if err != nil || !info.Mode().IsRegular() || info.Size() > limit {
		return nil, ErrOutput
	}
	file, err := root.Open(name)
	if err != nil {
		return nil, ErrOutput
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil || int64(len(data)) > limit {
		return nil, ErrOutput
	}
	return data, nil
}

// sharedStop serializes cancellation, command-context cancellation and cleanup.
// The OS handle/group remains owned until Wait has joined.
type sharedStop struct{ mu sync.Mutex }
