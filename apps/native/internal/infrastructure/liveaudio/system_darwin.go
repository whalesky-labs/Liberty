package liveaudio

import (
	"context"
	"runtime"
	"sync"
	"time"
	"unsafe"

	"github.com/ebitengine/purego"
	"github.com/ebitengine/purego/objc"
	"liberty.local/liberty/internal/domain"
)

type audioBuffer struct {
	Channels, Bytes uint32
	Data            unsafe.Pointer
}
type audioBufferList struct {
	Count   uint32
	_       uint32
	Buffers [2]audioBuffer
}
type audioDescription struct {
	Rate                                                                                    float64
	Format, Flags, BytesPerPacket, FramesPerPacket, BytesPerFrame, Channels, Bits, Reserved uint32
}
type screenAudioAPI struct {
	output       objc.Class
	format       func(uintptr) uintptr
	description  func(uintptr) *audioDescription
	buffers      func(uintptr, *uintptr, *audioBufferList, uintptr, uintptr, uintptr, uint32, *uintptr) int32
	release      func(uintptr)
	queue        func(string, uintptr) uintptr
	queueRelease func(uintptr)
}
type screenAudioSink struct {
	frames  chan []byte
	failure chan error
	pcm     pcmFrames
	api     screenAudioAPI
}

var screenSinks sync.Map

var loadScreenAudio = sync.OnceValues(func() (screenAudioAPI, error) {
	var api screenAudioAPI
	for _, name := range []string{"Foundation", "ScreenCaptureKit"} {
		if _, err := purego.Dlopen("/System/Library/Frameworks/"+name+".framework/"+name, purego.RTLD_NOW|purego.RTLD_GLOBAL); err != nil {
			return api, domain.ErrRuntimeUnsupported
		}
	}
	configuration := objc.ID(objc.GetClass("SCStreamConfiguration")).Send(objc.RegisterName("new"))
	if configuration == 0 {
		return api, domain.ErrRuntimeUnsupported
	}
	available := objc.Send[bool](configuration, objc.RegisterName("respondsToSelector:"), objc.RegisterName("setCapturesAudio:"))
	configuration.Send(objc.RegisterName("release"))
	if !available {
		return api, domain.ErrRuntimeUnsupported
	}
	cm, err := purego.Dlopen("/System/Library/Frameworks/CoreMedia.framework/CoreMedia", purego.RTLD_NOW|purego.RTLD_GLOBAL)
	if err != nil {
		return api, domain.ErrRuntimeUnsupported
	}
	purego.RegisterLibFunc(&api.format, cm, "CMSampleBufferGetFormatDescription")
	purego.RegisterLibFunc(&api.description, cm, "CMAudioFormatDescriptionGetStreamBasicDescription")
	purego.RegisterLibFunc(&api.buffers, cm, "CMSampleBufferGetAudioBufferListWithRetainedBlockBuffer")
	purego.RegisterLibFunc(&api.release, purego.RTLD_DEFAULT, "CFRelease")
	purego.RegisterLibFunc(&api.queue, purego.RTLD_DEFAULT, "dispatch_queue_create")
	purego.RegisterLibFunc(&api.queueRelease, purego.RTLD_DEFAULT, "dispatch_release")
	api.output, err = objc.RegisterClass("LibertySystemAudioOutput", objc.GetClass("NSObject"), []*objc.Protocol{objc.GetProtocol("SCStreamOutput"), objc.GetProtocol("SCStreamDelegate")}, nil, []objc.MethodDef{
		{Cmd: objc.RegisterName("stream:didOutputSampleBuffer:ofType:"), Fn: func(self objc.ID, _ objc.SEL, _ objc.ID, sample uintptr, kind int64) {
			if sink, ok := screenSinks.Load(self); ok && kind == 1 {
				sink.(*screenAudioSink).receive(sample)
			}
		}},
		{Cmd: objc.RegisterName("stream:didStopWithError:"), Fn: func(self objc.ID, _ objc.SEL, _ objc.ID, _ objc.ID) {
			if sink, ok := screenSinks.Load(self); ok {
				sink.(*screenAudioSink).fail(domain.ErrLiveCapture)
			}
		}},
	})
	return api, err
})

func systemAudioAvailable() bool { _, err := loadScreenAudio(); return err == nil }
func (s *screenAudioSink) fail(err error) {
	select {
	case s.failure <- err:
	default:
	}
}
func (s *screenAudioSink) receive(sample uintptr) {
	d := s.api.description(s.api.format(sample))
	if d == nil || d.Format != 0x6c70636d || d.Bits != 32 || d.Flags&1 == 0 || d.Channels != 1 {
		s.fail(domain.ErrLiveCapture)
		return
	}
	var list audioBufferList
	var block uintptr
	if s.api.buffers(sample, nil, &list, unsafe.Sizeof(list), 0, 0, 0, &block) != 0 {
		s.fail(domain.ErrLiveCapture)
		return
	}
	if block != 0 {
		defer s.api.release(block)
	}
	if list.Count != 1 || list.Buffers[0].Channels != 1 || list.Buffers[0].Bytes > 4*192000 || list.Buffers[0].Bytes%4 != 0 || list.Buffers[0].Data == nil {
		s.fail(domain.ErrLiveCapture)
		return
	}
	for _, value := range unsafe.Slice((*float32)(list.Buffers[0].Data), int(list.Buffers[0].Bytes/4)) {
		if err := s.pcm.sample(float64(value), d.Rate); err != nil {
			s.fail(err)
			return
		}
	}
}

func captureSystemAudio(ctx context.Context, consume func([]byte) error) (captureErr error) {
	api, err := loadScreenAudio()
	if err != nil {
		return err
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	pool := objc.ID(objc.GetClass("NSAutoreleasePool")).Send(objc.RegisterName("new"))
	defer pool.Send(objc.RegisterName("drain"))
	// Shareable content requests the OS permission only after explicit Start.
	type contentReply struct {
		content objc.ID
		failed  bool
	}
	reply := make(chan contentReply, 1)
	var replyMu sync.Mutex
	abandoned := false
	callback := objc.NewBlock(func(_ objc.Block, content, failure objc.ID) {
		replyMu.Lock()
		defer replyMu.Unlock()
		if abandoned {
			return
		}
		if content != 0 {
			content.Send(objc.RegisterName("retain"))
		}
		reply <- contentReply{content, failure != 0}
	})
	objc.ID(objc.GetClass("SCShareableContent")).Send(objc.RegisterName("getShareableContentExcludingDesktopWindows:onScreenWindowsOnly:completionHandler:"), true, false, callback)
	callback.Release()
	var content objc.ID
	select {
	case r := <-reply:
		content = r.content
		if content != 0 {
			defer content.Send(objc.RegisterName("release"))
		}
		if r.failed || content == 0 {
			return domain.ErrLiveCapture
		}
	case <-ctx.Done():
		replyMu.Lock()
		abandoned = true
		select {
		case r := <-reply:
			if r.content != 0 {
				r.content.Send(objc.RegisterName("release"))
			}
		default:
		}
		replyMu.Unlock()
		return ctx.Err()
	}
	displays := content.Send(objc.RegisterName("displays"))
	if objc.Send[uint64](displays, objc.RegisterName("count")) == 0 {
		return domain.ErrLiveCapture
	}
	display := displays.Send(objc.RegisterName("objectAtIndex:"), uint64(0))
	empty := objc.ID(objc.GetClass("NSArray")).Send(objc.RegisterName("array"))
	filter := objc.ID(objc.GetClass("SCContentFilter")).Send(objc.RegisterName("alloc")).Send(objc.RegisterName("initWithDisplay:excludingWindows:"), display, empty)
	defer filter.Send(objc.RegisterName("release"))
	configuration := objc.ID(objc.GetClass("SCStreamConfiguration")).Send(objc.RegisterName("new"))
	defer configuration.Send(objc.RegisterName("release"))
	configuration.Send(objc.RegisterName("setCapturesAudio:"), true)
	configuration.Send(objc.RegisterName("setExcludesCurrentProcessAudio:"), true)
	configuration.Send(objc.RegisterName("setSampleRate:"), int64(16000))
	configuration.Send(objc.RegisterName("setChannelCount:"), int64(1))
	configuration.Send(objc.RegisterName("setWidth:"), uint64(2))
	configuration.Send(objc.RegisterName("setHeight:"), uint64(2))
	output := objc.ID(api.output).Send(objc.RegisterName("new"))
	defer output.Send(objc.RegisterName("release"))
	sink := &screenAudioSink{frames: make(chan []byte, 25), failure: make(chan error, 1), api: api}
	sink.pcm.emit = func(frame []byte) error {
		select {
		case sink.frames <- frame:
			return nil
		default:
			return domain.ErrLiveBackpressure
		}
	}
	screenSinks.Store(output, sink)
	defer screenSinks.Delete(output)
	queue := api.queue("liberty.system.audio", 0)
	defer api.queueRelease(queue)
	stream := objc.ID(objc.GetClass("SCStream")).Send(objc.RegisterName("alloc")).Send(objc.RegisterName("initWithFilter:configuration:delegate:"), filter, configuration, output)
	defer stream.Send(objc.RegisterName("release"))
	var nativeError objc.ID
	if !objc.Send[bool](stream, objc.RegisterName("addStreamOutput:type:sampleHandlerQueue:error:"), output, int64(1), queue, &nativeError) {
		return domain.ErrLiveCapture
	}
	start := make(chan bool, 1)
	started := objc.NewBlock(func(_ objc.Block, failure objc.ID) { start <- failure == 0 })
	stream.Send(objc.RegisterName("startCaptureWithCompletionHandler:"), started)
	started.Release()
	defer func() {
		stopped := make(chan bool, 1)
		callback := objc.NewBlock(func(_ objc.Block, failure objc.ID) { stopped <- failure == 0 })
		stream.Send(objc.RegisterName("stopCaptureWithCompletionHandler:"), callback)
		callback.Release()
		select {
		case ok := <-stopped:
			if !ok {
				captureErr = domain.ErrTerminationUnverified
			}
		case <-time.After(5 * time.Second):
			captureErr = domain.ErrTerminationUnverified
		}
	}()
	select {
	case ok := <-start:
		if !ok {
			return domain.ErrLiveCapture
		}
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(15 * time.Second):
		return domain.ErrLiveCapture
	}
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case err := <-sink.failure:
			return err
		case frame := <-sink.frames:
			if err := consume(frame); err != nil {
				return err
			}
		}
	}
}
