package playback

import (
	"context"
	"golang.org/x/sys/windows"
	"runtime"
	"time"
	"unsafe"
)

func playWave(ctx context.Context, data []byte, _ string) error {
	play := windows.NewLazySystemDLL("winmm.dll").NewProc("PlaySoundW")
	if err := play.Find(); err != nil {
		return ErrPlayback
	}
	var pin runtime.Pinner
	pin.Pin(&data[0])
	defer pin.Unpin()
	result, _, _ := play.Call(uintptr(unsafe.Pointer(&data[0])), 0, 0x0001|0x0002|0x0004) // ASYNC | NODEFAULT | MEMORY
	if result == 0 {
		return ErrPlayback
	}
	defer func() { play.Call(0, 0, 0); runtime.KeepAlive(data) }()
	duration := time.Duration(len(data)-44) * time.Second / 32000
	timer := time.NewTimer(duration + 100*time.Millisecond)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
