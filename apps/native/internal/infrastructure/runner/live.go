package runner

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"time"
	"unicode/utf8"

	"liberty.local/liberty/internal/contracts"
	"liberty.local/liberty/internal/domain"
)

const liveProtocol = "liberty-live-asr"

type LiveCommand struct {
	Python, Script, Model, VAD, Directory string
	Threads                               uint32
}

func ParseLiveEvent(data []byte) (domain.LiveRecognitionEvent, error) {
	var event domain.LiveRecognitionEvent
	v, err := contracts.Shared()
	if err != nil || len(data) > 8192 || !utf8.Valid(data) || v.Validate("https://liberty.local/schemas/live/v1/event.schema.json", data) != nil {
		return event, domain.ErrLiveProtocol
	}
	// Reject duplicate keys before the ordinary typed decode can overwrite one.
	d := json.NewDecoder(bytes.NewReader(data))
	token, err := d.Token()
	if err != nil || token != json.Delim('{') {
		return event, domain.ErrLiveProtocol
	}
	seen := map[string]bool{}
	for d.More() {
		key, e := d.Token()
		if e != nil {
			return event, domain.ErrLiveProtocol
		}
		name, ok := key.(string)
		if !ok || seen[name] {
			return event, domain.ErrLiveProtocol
		}
		seen[name] = true
		var raw json.RawMessage
		if d.Decode(&raw) != nil {
			return event, domain.ErrLiveProtocol
		}
	}
	if json.Unmarshal(data, &event) != nil || event.EndMS < event.StartMS {
		return event, domain.ErrLiveProtocol
	}
	return event, nil
}

func (s LiveCommand) Run(ctx context.Context, frames <-chan domain.LiveAudio, emit func(domain.LiveRecognitionEvent) error) error {
	for _, name := range []string{s.Python, s.Script} {
		info, err := os.Stat(name)
		if !filepath.IsAbs(name) || err != nil || !info.Mode().IsRegular() {
			return ErrStart
		}
	}
	for _, name := range []string{s.Model, s.VAD, s.Directory} {
		info, err := os.Stat(name)
		if !filepath.IsAbs(name) || err != nil || !info.IsDir() {
			return ErrStart
		}
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	cmd := exec.CommandContext(ctx, s.Python, "-I", s.Script, "--model", s.Model, "--vad", s.VAD, "--threads", strconv.Itoa(int(max(1, min(s.Threads, 8)))))
	cmd.Dir = s.Directory
	control, err := prepareProcess(cmd)
	if err != nil {
		return err
	}
	defer control.close()
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return ErrStart
	}
	defer stdin.Close()
	lines := make(chan streamLine, 32)
	out := &lineWriter{emit: func(line streamLine) error {
		select {
		case lines <- line:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}}
	cmd.Stdout = out
	cmd.Stderr = io.Discard
	cmd.Cancel = control.stop
	cmd.WaitDelay = time.Second
	if cmd.Start() != nil {
		return ErrStart
	}
	if control.started(cmd.Process) != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return ErrStart
	}
	written := make(chan error, 1)
	go func() {
		defer stdin.Close()
		encoder := json.NewEncoder(stdin)
		var seq uint64
		var end int64
		for {
			select {
			case <-ctx.Done():
				written <- ctx.Err()
				return
			case frame, ok := <-frames:
				if !ok {
					written <- domain.ErrLiveProtocol
					return
				}
				if frame.OffsetMS < end || frame.OffsetMS > 86400000 || (frame.Kind != "audio" && frame.Kind != "boundary" && frame.Kind != "stop") || len(frame.PCM) > domain.LiveFrameBytes || len(frame.PCM)%32 != 0 || (frame.Kind == "audio" && len(frame.PCM) == 0) || (frame.Kind != "audio" && len(frame.PCM) != 0) {
					written <- domain.ErrLiveProtocol
					return
				}
				seq++
				value := struct {
					Protocol string `json:"protocol"`
					Version  int    `json:"version"`
					Sequence uint64 `json:"sequence"`
					Type     string `json:"type"`
					OffsetMS int64  `json:"offsetMs"`
					PCM      []byte `json:"pcm,omitempty"`
				}{liveProtocol, 1, seq, frame.Kind, frame.OffsetMS, frame.PCM}
				if err := encoder.Encode(value); err != nil {
					written <- ErrProcess
					return
				}
				end = frame.OffsetMS + int64(len(frame.PCM)/32)
				if frame.Kind == "stop" {
					written <- nil
					return
				}
			}
		}
	}()
	done := make(chan error, 1)
	go func() {
		err := cmd.Wait()
		if e := out.flush(); e != nil && err == nil {
			err = e
		}
		done <- err
	}()
	// Both goroutines are joined on all exits, including a blocked stdin write.
	writeJoined := false
	processJoined := false
	defer func() {
		cancel()
		_ = stdin.Close()
		_ = control.stop()
		if !processJoined {
			<-done
		}
		if !writeJoined {
			<-written
		}
	}()
	timer := time.NewTimer(2 * time.Minute)
	defer timer.Stop()
	var sequence uint64
	ready, stopped := false, false
	apply := func(line streamLine) error {
		if line.err != nil {
			return line.err
		}
		event, err := ParseLiveEvent(line.data)
		if err != nil {
			return err
		}
		if stopped || event.Sequence != sequence+1 || (!ready && event.Type != "ready" && event.Type != "error") || (ready && event.Type == "ready") {
			return domain.ErrLiveProtocol
		}
		sequence = event.Sequence
		if event.Type == "ready" {
			ready = true
			timer.Stop()
		}
		if event.Type == "stopped" {
			stopped = true
		}
		if event.Type == "error" {
			return ErrProcess
		}
		return emit(event)
	}
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
			return ErrProcess
		case err := <-written:
			writeJoined = true
			written = nil
			if err != nil {
				return err
			}
		case line := <-lines:
			if err := apply(line); err != nil {
				return err
			}
		case err := <-done:
			processJoined = true
			for len(lines) > 0 {
				if e := apply(<-lines); e != nil {
					return e
				}
			}
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if err != nil || !stopped {
				return ErrProcess
			}
			return nil
		}
	}
}

type pcmWriter struct {
	buffer  []byte
	consume func([]byte) error
	cancel  context.CancelFunc
	err     error
}

func (w *pcmWriter) Write(data []byte) (int, error) {
	count := len(data)
	for len(data) > 0 {
		n := min(domain.LiveFrameBytes-len(w.buffer), len(data))
		w.buffer = append(w.buffer, data[:n]...)
		data = data[n:]
		if len(w.buffer) == domain.LiveFrameBytes {
			frame := w.buffer
			w.buffer = nil
			if err := w.consume(frame); err != nil {
				w.err = err
				w.cancel()
				return 0, err
			}
		}
	}
	return count, nil
}

// CapturePCM owns the capture process group/Job Object, bounds each frame and
// discards device diagnostics rather than leaking identifiers to application logs.
func CapturePCM(ctx context.Context, executable string, args []string, directory string, consume func([]byte) error) error {
	if !filepath.IsAbs(executable) || !filepath.IsAbs(directory) {
		return ErrStart
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	cmd := exec.CommandContext(ctx, executable, args...)
	cmd.Dir = directory
	control, err := prepareProcess(cmd)
	if err != nil {
		return err
	}
	defer control.close()
	out := &pcmWriter{consume: consume, cancel: cancel}
	cmd.Stdout = out
	cmd.Stderr = io.Discard
	cmd.Cancel = control.stop
	cmd.WaitDelay = time.Second
	if cmd.Start() != nil {
		return ErrStart
	}
	if control.started(cmd.Process) != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return ErrStart
	}
	err = cmd.Wait()
	if control.stop() != nil {
		return domain.ErrTerminationUnverified
	}
	if out.err != nil {
		return out.err
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err != nil {
		return domain.ErrLiveCapture
	}
	if n := len(out.buffer) / 32 * 32; n > 0 {
		if err := consume(out.buffer[:n]); err != nil {
			return err
		}
	}
	return nil
}

func CaptureDeviceOutput(ctx context.Context, executable string, args []string, directory string) (string, error) {
	var data bytes.Buffer
	err := RunCommand(ctx, executable, args, directory, nil, func(line string) error {
		if data.Len()+len(line) > 128*1024 {
			return domain.ErrLiveCapture
		}
		data.WriteString(line)
		data.WriteByte('\n')
		return nil
	})
	// ffmpeg returns exit 1 after listing devices; output is parsed by the platform adapter.
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	if err != nil && err != ErrProcess {
		return "", err
	}
	return data.String(), nil
}
