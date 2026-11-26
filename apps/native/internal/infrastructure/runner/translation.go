package runner

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"liberty.local/liberty/internal/contracts"
	"liberty.local/liberty/internal/domain"
)

type TranslationCommand struct{ Python, Script, Engine, Model, Directory string }
type TranslationWorker struct {
	mu       sync.Mutex
	cmd      *exec.Cmd
	control  *processControl
	stdin    io.WriteCloser
	lines    chan streamLine
	done     chan struct{}
	cancel   context.CancelFunc
	sequence uint64
	closed   bool
}
type translationEvent struct {
	Protocol string `json:"protocol"`
	Version  int    `json:"version"`
	Type     string `json:"type"`
	Sequence uint64 `json:"sequence"`
	Text     string `json:"text"`
	Code     string `json:"code"`
}

func parseTranslationEvent(data []byte) (translationEvent, error) {
	var e translationEvent
	v, err := contracts.Shared()
	if err != nil || len(data) > 16*1024 || !utf8.Valid(data) || v.Validate("https://liberty.local/schemas/translation/v1/event.schema.json", data) != nil {
		return e, domain.ErrLiveTranslation
	}
	d := json.NewDecoder(bytes.NewReader(data))
	token, err := d.Token()
	if err != nil || token != json.Delim('{') {
		return e, domain.ErrLiveTranslation
	}
	seen := map[string]bool{}
	for d.More() {
		key, err := d.Token()
		name, ok := key.(string)
		if err != nil || !ok || seen[name] {
			return e, domain.ErrLiveTranslation
		}
		seen[name] = true
		var raw json.RawMessage
		if d.Decode(&raw) != nil {
			return e, domain.ErrLiveTranslation
		}
	}
	if json.Unmarshal(data, &e) != nil {
		return e, domain.ErrLiveTranslation
	}
	if e.Type == "translation" && (e.Sequence == 0 || strings.TrimSpace(e.Text) == "" || len(e.Text) > domain.LiveMaxTextBytes) {
		return e, domain.ErrLiveTranslation
	}
	return e, nil
}
func (s TranslationCommand) Start(ctx context.Context) (*TranslationWorker, error) {
	for _, name := range []string{s.Python, s.Script} {
		info, err := os.Stat(name)
		if !filepath.IsAbs(name) || err != nil || !info.Mode().IsRegular() {
			return nil, ErrStart
		}
	}
	for _, name := range []string{s.Engine, s.Model, s.Directory} {
		info, err := os.Stat(name)
		if !filepath.IsAbs(name) || err != nil || !info.IsDir() {
			return nil, ErrStart
		}
	}
	processCtx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(processCtx, s.Python, "-I", "-B", s.Script, "--engine", s.Engine, "--model", s.Model)
	cmd.Dir = s.Directory
	control, err := prepareProcess(cmd)
	if err != nil {
		cancel()
		return nil, err
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		cancel()
		control.close()
		return nil, ErrStart
	}
	w := &TranslationWorker{cmd: cmd, control: control, stdin: stdin, lines: make(chan streamLine, 4), done: make(chan struct{}), cancel: cancel}
	out := &lineWriter{emit: func(line streamLine) error {
		select {
		case w.lines <- line:
			return nil
		case <-processCtx.Done():
			return processCtx.Err()
		}
	}}
	cmd.Stdout = out
	cmd.Stderr = io.Discard
	cmd.Cancel = control.stop
	cmd.WaitDelay = time.Second
	if cmd.Start() != nil {
		stdin.Close()
		cancel()
		control.close()
		return nil, ErrStart
	}
	if control.started(cmd.Process) != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		stdin.Close()
		cancel()
		control.close()
		return nil, ErrStart
	}
	go func() { _ = cmd.Wait(); _ = out.flush(); close(w.done) }()
	ctx, stop := context.WithTimeout(ctx, 30*time.Second)
	defer stop()
	select {
	case line := <-w.lines:
		e, err := parseTranslationEvent(line.data)
		if line.err != nil || err != nil || e.Type != "ready" {
			w.Close()
			return nil, domain.ErrLiveTranslation
		}
		return w, nil
	case <-ctx.Done():
		w.Close()
		return nil, ctx.Err()
	case <-w.done:
		w.Close()
		return nil, domain.ErrLiveTranslation
	}
}
func (w *TranslationWorker) closeLocked() {
	if w.closed {
		return
	}
	w.closed = true
	w.cancel()
	_ = w.stdin.Close()
	_ = w.control.stop()
	<-w.done
	w.control.close()
}
func (w *TranslationWorker) Close() { w.mu.Lock(); defer w.mu.Unlock(); w.closeLocked() }
func (w *TranslationWorker) Translate(ctx context.Context, text string) (string, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return "", domain.ErrLiveClosed
	}
	if strings.TrimSpace(text) == "" || len(text) > domain.LiveMaxTextBytes || !utf8.ValidString(text) {
		return "", domain.ErrLiveInput
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	w.sequence++
	raw, err := json.Marshal(map[string]any{"protocol": "liberty-local-translation", "version": 1, "sequence": w.sequence, "text": text})
	if err != nil || len(raw) > 32767 {
		return "", domain.ErrLiveInput
	}
	raw = append(raw, '\n')
	written := make(chan error, 1)
	go func() { _, err := w.stdin.Write(raw); written <- err }()
	select {
	case err := <-written:
		if err != nil {
			w.closeLocked()
			return "", domain.ErrLiveTranslation
		}
	case <-ctx.Done():
		w.closeLocked()
		<-written
		return "", ctx.Err()
	}
	select {
	case line := <-w.lines:
		e, err := parseTranslationEvent(line.data)
		if line.err != nil || err != nil || e.Type != "translation" || e.Sequence != w.sequence {
			w.closeLocked()
			return "", domain.ErrLiveTranslation
		}
		return e.Text, nil
	case <-ctx.Done():
		w.closeLocked()
		return "", ctx.Err()
	case <-w.done:
		w.closeLocked()
		return "", domain.ErrLiveTranslation
	}
}
