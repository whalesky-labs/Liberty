package liveaudio

import (
	"context"
	"encoding/binary"
	"errors"
	"math"
	"time"

	"liberty.local/liberty/internal/domain"
)

const systemDeviceID = "system-output"
const mixedDevicePrefix = "system-and-microphone:"

// pcmFrames converts mono samples to bounded 100 ms frames. Capture callbacks
// never block the OS audio queue; a slow consumer receives an explicit error.
type pcmFrames struct {
	buffer []byte
	phase  float64
	sum    float64
	count  int
	emit   func([]byte) error
}

func (p *pcmFrames) sample(value float64, rate float64) error {
	if rate < 16000 || rate > 192000 || math.IsNaN(value) || math.IsInf(value, 0) {
		return domain.ErrLiveCapture
	}
	p.sum += value
	p.count++
	p.phase += 16000
	if p.phase < rate {
		return nil
	}
	p.phase -= rate
	value = max(-1, min(1, p.sum/float64(p.count)))
	p.sum, p.count = 0, 0
	n := int16(math.Round(value * 32767))
	p.buffer = append(p.buffer, byte(n), byte(uint16(n)>>8))
	if len(p.buffer) == domain.LiveFrameBytes {
		frame := p.buffer
		p.buffer = make([]byte, 0, domain.LiveFrameBytes)
		return p.emit(frame)
	}
	return nil
}

type captureFunc func(context.Context, func([]byte) error) error

func mixCapture(ctx context.Context, system, microphone captureFunc, consume func([]byte) error) error {
	return clockedCapture(ctx, consume, system, microphone)
}

func clockedCapture(ctx context.Context, consume func([]byte) error, captures ...captureFunc) (captureErr error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	channels := make([]chan []byte, len(captures))
	done := make(chan error, len(captures))
	for i, capture := range captures {
		channels[i] = make(chan []byte, 25)
		go func() {
			done <- capture(ctx, func(pcm []byte) error {
				if len(pcm) > domain.LiveFrameBytes || len(pcm)%2 != 0 {
					return domain.ErrLiveCapture
				}
				frame := make([]byte, domain.LiveFrameBytes)
				copy(frame, pcm)
				select {
				case channels[i] <- frame:
					return nil
				case <-ctx.Done():
					return ctx.Err()
				default:
					return domain.ErrLiveBackpressure
				}
			})
		}()
	}
	remaining := len(captures)
	defer func() {
		cancel()
		for remaining > 0 {
			if err := <-done; errors.Is(err, domain.ErrTerminationUnverified) {
				captureErr = err
			}
			remaining--
		}
	}()
	tick := time.NewTicker(100 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case err := <-done:
			remaining--
			if err == nil {
				return domain.ErrLiveCapture
			}
			return err
		case <-tick.C:
			mixed := make([]byte, domain.LiveFrameBytes)
			for _, channel := range channels {
				select {
				case frame := <-channel:
					for j := 0; j < len(mixed); j += 2 {
						n := int32(int16(binary.LittleEndian.Uint16(mixed[j:]))) + int32(int16(binary.LittleEndian.Uint16(frame[j:])))/int32(len(captures))
						binary.LittleEndian.PutUint16(mixed[j:], uint16(int16(n)))
					}
				default:
				}
			}
			if err := consume(mixed); err != nil {
				return err
			}
		}
	}
}
