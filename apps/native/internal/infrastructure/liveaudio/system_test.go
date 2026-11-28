package liveaudio

import (
	"context"
	"encoding/binary"
	"errors"
	"testing"
	"time"

	"liberty.local/liberty/internal/domain"
)

func TestPCMFramesResamplesAndBoundsFrames(t *testing.T) {
	frames := 0
	p := pcmFrames{emit: func(frame []byte) error {
		if len(frame) != 3200 || int16(binary.LittleEndian.Uint16(frame)) != 16384 {
			t.Fatal("incorrect mono conversion")
		}
		frames++
		return nil
	}}
	for i := 0; i < 4800; i++ {
		if err := p.sample(.5, 48000); err != nil {
			t.Fatal(err)
		}
	}
	if frames != 1 || len(p.buffer) != 0 {
		t.Fatal("resampling changed duration")
	}
}

func TestClockedCapturePreservesSilenceAndCancelsEverySource(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	done := make(chan struct{}, 2)
	silent := func(ctx context.Context, _ func([]byte) error) error {
		<-ctx.Done()
		done <- struct{}{}
		return ctx.Err()
	}
	frames := 0
	stop := errors.New("synthetic-consumer-stop")
	err := mixCapture(ctx, silent, silent, func(frame []byte) error {
		frames++
		if len(frame) != 3200 {
			t.Fatal("invalid silence frame")
		}
		return stop
	})
	if err != stop || frames != 1 || len(done) != 2 {
		t.Fatal("silence or source cleanup failed", err)
	}
}

func TestClockedCaptureRejectsBacklog(t *testing.T) {
	fast := func(_ context.Context, emit func([]byte) error) error {
		for i := 0; i < 100; i++ {
			if err := emit(make([]byte, 3200)); err != nil {
				return err
			}
		}
		return nil
	}
	if err := clockedCapture(context.Background(), func([]byte) error { return nil }, fast); !errors.Is(err, domain.ErrLiveBackpressure) {
		t.Fatal("unbounded backlog", err)
	}
}
