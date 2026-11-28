package playback

import (
	"context"
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"sync"

	"liberty.local/liberty/internal/domain"
	"liberty.local/liberty/internal/infrastructure/execution"
	"liberty.local/liberty/internal/infrastructure/runner"
)

var ErrPlayback = errors.New("media_playback_failed")

type Player struct {
	Inputs interface {
		Input(context.Context, string) (domain.JobInput, error)
	}
	Runtime   execution.RuntimeResolver
	Directory string
	mu        sync.Mutex
	stateMu   sync.Mutex
	closed    bool
	cancels   map[uint64]context.CancelFunc
	sequence  uint64
}

func (p *Player) Play(ctx context.Context, id string, segment domain.Segment, speed float64) error {
	p.stateMu.Lock()
	if p.closed {
		p.stateMu.Unlock()
		return context.Canceled
	}
	ctx, cancel := context.WithCancel(ctx)
	if p.cancels == nil {
		p.cancels = make(map[uint64]context.CancelFunc)
	}
	p.sequence++
	sequence := p.sequence
	p.cancels[sequence] = cancel
	p.stateMu.Unlock()
	defer func() { cancel(); p.stateMu.Lock(); delete(p.cancels, sequence); p.stateMu.Unlock() }()
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if domain.ValidateJobID(id) != nil || segment.EndMS <= segment.StartMS || segment.EndMS-segment.StartMS > 120000 || (speed != .75 && speed != 1 && speed != 1.25 && speed != 1.5 && speed != 2) {
		return ErrPlayback
	}
	input, err := p.Inputs.Input(ctx, id)
	if err != nil {
		return err
	}
	if !filepath.IsAbs(input.SourcePath) {
		return ErrPlayback
	}
	info, err := os.Lstat(input.SourcePath)
	if err != nil || !info.Mode().IsRegular() {
		return ErrPlayback
	}
	r, err := p.Runtime.Resolve(ctx)
	if err != nil {
		return err
	}
	seconds := func(ms uint64) string { return strconv.FormatFloat(float64(ms)/1000, 'f', 3, 64) }
	args := []string{"-hide_banner", "-nostdin", "-loglevel", "error", "-protocol_whitelist", "file,pipe", "-ss", seconds(segment.StartMS), "-t", seconds(segment.EndMS - segment.StartMS), "-i", input.SourcePath, "-vn", "-af", "atempo=" + strconv.FormatFloat(speed, 'f', 2, 64), "-ac", "1", "-ar", "16000", "-c:a", "pcm_s16le", "-f", "s16le", "pipe:1"}
	pcm := make([]byte, 0)
	if err := runner.CapturePCM(ctx, r.FFmpeg, args, p.Directory, func(frame []byte) error {
		if len(pcm)+len(frame) > 6*1024*1024 {
			return ErrPlayback
		}
		pcm = append(pcm, frame...)
		return nil
	}); err != nil {
		return err
	}
	if len(pcm) == 0 {
		return ErrPlayback
	}
	wav := make([]byte, 44+len(pcm))
	copy(wav, "RIFF")
	binary.LittleEndian.PutUint32(wav[4:], uint32(36+len(pcm)))
	copy(wav[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(wav[16:], 16)
	binary.LittleEndian.PutUint16(wav[20:], 1)
	binary.LittleEndian.PutUint16(wav[22:], 1)
	binary.LittleEndian.PutUint32(wav[24:], 16000)
	binary.LittleEndian.PutUint32(wav[28:], 32000)
	binary.LittleEndian.PutUint16(wav[32:], 2)
	binary.LittleEndian.PutUint16(wav[34:], 16)
	copy(wav[36:], "data")
	binary.LittleEndian.PutUint32(wav[40:], uint32(len(pcm)))
	copy(wav[44:], pcm)
	return playWave(ctx, wav, p.Directory)
}

func (p *Player) Close() {
	p.stateMu.Lock()
	p.closed = true
	for _, cancel := range p.cancels {
		cancel()
	}
	p.stateMu.Unlock()
	// Accepted playback owns no repository access after this join. New calls
	// are rejected even if the profile is closed outside the desktop shell.
	p.mu.Lock()
	p.mu.Unlock()
}
