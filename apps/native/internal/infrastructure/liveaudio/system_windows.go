package liveaudio

import (
	"context"
	"encoding/binary"
	"math"
	"runtime"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
	"liberty.local/liberty/internal/domain"
)

func systemAudioAvailable() bool { return true }

func comCall(object uintptr, method int, args ...uintptr) (uintptr, bool) {
	vtable := *(*uintptr)(unsafe.Pointer(object))
	address := *(*uintptr)(unsafe.Pointer(vtable + uintptr(method)*unsafe.Sizeof(uintptr(0))))
	args = append([]uintptr{object}, args...)
	result, _, _ := syscall.SyscallN(address, args...)
	return result, int32(result) >= 0
}

func captureSystemAudio(ctx context.Context, consume func([]byte) error) error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	ole := windows.NewLazySystemDLL("ole32.dll")
	initialized, _, _ := ole.NewProc("CoInitializeEx").Call(0, 0)
	if int32(initialized) < 0 {
		return domain.ErrLiveCapture
	}
	defer ole.NewProc("CoUninitialize").Call()
	class := windows.GUID{Data1: 0xbcde0395, Data2: 0xe52f, Data3: 0x467c, Data4: [8]byte{0x8e, 0x3d, 0xc4, 0x57, 0x92, 0x91, 0x69, 0x2e}}
	enumeratorID := windows.GUID{Data1: 0xa95664d2, Data2: 0x9614, Data3: 0x4f35, Data4: [8]byte{0xa7, 0x46, 0xde, 0x8d, 0xb6, 0x36, 0x17, 0xe6}}
	clientID := windows.GUID{Data1: 0x1cb9ad4c, Data2: 0xdbfa, Data3: 0x4c32, Data4: [8]byte{0xb1, 0x78, 0xc2, 0xf5, 0x68, 0xa7, 0x03, 0xb2}}
	captureID := windows.GUID{Data1: 0xc8adbd64, Data2: 0xe71e, Data3: 0x48a0, Data4: [8]byte{0xa4, 0xde, 0x18, 0x5c, 0x39, 0x5c, 0xd3, 0x17}}
	var enumerator, device, client, capture uintptr
	result, _, _ := ole.NewProc("CoCreateInstance").Call(uintptr(unsafe.Pointer(&class)), 0, 23, uintptr(unsafe.Pointer(&enumeratorID)), uintptr(unsafe.Pointer(&enumerator)))
	if int32(result) < 0 || enumerator == 0 {
		return domain.ErrLiveCapture
	}
	defer comCall(enumerator, 2)
	if _, ok := comCall(enumerator, 4, 0, 0, uintptr(unsafe.Pointer(&device))); !ok || device == 0 {
		return domain.ErrLiveCapture
	}
	defer comCall(device, 2)
	if _, ok := comCall(device, 3, uintptr(unsafe.Pointer(&clientID)), 23, 0, uintptr(unsafe.Pointer(&client))); !ok || client == 0 {
		return domain.ErrLiveCapture
	}
	defer comCall(client, 2)
	var format uintptr
	if _, ok := comCall(client, 8, uintptr(unsafe.Pointer(&format))); !ok || format == 0 {
		return domain.ErrLiveCapture
	}
	defer ole.NewProc("CoTaskMemFree").Call(format)
	bytes := unsafe.Slice((*byte)(unsafe.Pointer(format)), 18)
	tag, channels := binary.LittleEndian.Uint16(bytes), int(binary.LittleEndian.Uint16(bytes[2:]))
	rate := binary.LittleEndian.Uint32(bytes[4:])
	stride, bits := int(binary.LittleEndian.Uint16(bytes[12:])), int(binary.LittleEndian.Uint16(bytes[14:]))
	if tag == 0xfffe {
		if binary.LittleEndian.Uint16(bytes[16:]) < 22 {
			return domain.ErrLiveCapture
		}
		extended := unsafe.Slice((*byte)(unsafe.Pointer(format)), 40)
		tag = binary.LittleEndian.Uint16(extended[24:])
	}
	if channels < 1 || channels > 8 || rate < 16000 || rate > 192000 || stride != channels*(bits/8) || !((tag == 3 && bits == 32) || (tag == 1 && (bits == 16 || bits == 32))) {
		return domain.ErrLiveCapture
	}
	// Shared mode, loopback, one-second OS buffer. Native mix format avoids
	// assuming device support for a particular sample rate or integer format.
	if _, ok := comCall(client, 3, 0, 0x20000, 10000000, 0, format, 0); !ok {
		return domain.ErrLiveCapture
	}
	if _, ok := comCall(client, 14, uintptr(unsafe.Pointer(&captureID)), uintptr(unsafe.Pointer(&capture))); !ok || capture == 0 {
		return domain.ErrLiveCapture
	}
	defer comCall(capture, 2)
	if _, ok := comCall(client, 10); !ok {
		return domain.ErrLiveCapture
	}
	defer comCall(client, 11)
	pcm := pcmFrames{emit: consume}
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	first := true
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-tick.C:
		}
		for {
			var pending uint32
			if _, ok := comCall(capture, 5, uintptr(unsafe.Pointer(&pending))); !ok {
				return domain.ErrLiveCapture
			}
			if pending == 0 {
				break
			}
			var data uintptr
			var frames, flags uint32
			if _, ok := comCall(capture, 3, uintptr(unsafe.Pointer(&data)), uintptr(unsafe.Pointer(&frames)), uintptr(unsafe.Pointer(&flags)), 0, 0); !ok {
				return domain.ErrLiveCapture
			}
			var readErr error
			if frames > rate || flags&1 != 0 && !first {
				readErr = domain.ErrLiveBackpressure
			}
			first = false
			if readErr == nil {
				var buffer []byte
				if flags&2 == 0 {
					if data == 0 {
						readErr = domain.ErrLiveCapture
					} else {
						buffer = unsafe.Slice((*byte)(unsafe.Pointer(data)), int(frames)*stride)
					}
				}
				for i := 0; i < int(frames) && readErr == nil; i++ {
					value := float64(0)
					if buffer != nil {
						for channel := 0; channel < channels; channel++ {
							part := buffer[i*stride+channel*(bits/8):]
							if tag == 3 {
								value += float64(math.Float32frombits(binary.LittleEndian.Uint32(part)))
							} else if bits == 16 {
								value += float64(int16(binary.LittleEndian.Uint16(part))) / 32768
							} else {
								value += float64(int32(binary.LittleEndian.Uint32(part))) / 2147483648
							}
						}
					}
					readErr = pcm.sample(value/float64(channels), float64(rate))
				}
			}
			if _, ok := comCall(capture, 4, uintptr(frames)); !ok {
				return domain.ErrLiveCapture
			}
			if readErr != nil {
				return readErr
			}
		}
	}
}
