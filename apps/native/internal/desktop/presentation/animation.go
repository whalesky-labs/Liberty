package presentation

import (
	"bytes"
	"encoding/binary"
	"errors"
	"image"
	"image/color"
	"image/draw"
	"time"

	"github.com/egoist/mygo/ui"
	"golang.org/x/image/webp"
)

// WebP's ANMF chunks are independent lossless/lossy frames. x/image decodes
// those frames; this adapter performs the specified offset/blend/disposal and
// timing. It does not flatten the original animated artwork into a still.
// Reference: https://developers.google.com/speed/webp/docs/riff_container
// Only repository-owned embedded artwork is accepted by this presentation API.
type animationFrame struct {
	bitmap   *ui.Bitmap
	duration time.Duration
}
type animation struct {
	frames   []animationFrame
	duration time.Duration
	loops    uint16
	started  time.Time
}

var errAnimation = errors.New("invalid_embedded_animation")

func little24(b []byte) int { return int(b[0]) | int(b[1])<<8 | int(b[2])<<16 }
func chunk(kind string, data []byte) []byte {
	result := make([]byte, 8+len(data)+len(data)%2)
	copy(result, kind)
	binary.LittleEndian.PutUint32(result[4:], uint32(len(data)))
	copy(result[8:], data)
	return result
}
func decodeAnimation(data []byte) (*animation, error) {
	if len(data) < 12 || string(data[:4]) != "RIFF" || string(data[8:12]) != "WEBP" || uint64(binary.LittleEndian.Uint32(data[4:8]))+8 != uint64(len(data)) {
		return nil, errAnimation
	}
	a := &animation{started: time.Now()}
	var canvas *image.RGBA
	var background color.NRGBA
	seenAnimation := false
	for offset := 12; offset < len(data); {
		if len(data)-offset < 8 {
			return nil, errAnimation
		}
		name := string(data[offset : offset+4])
		length := uint64(binary.LittleEndian.Uint32(data[offset+4 : offset+8]))
		end := uint64(offset) + 8 + length
		if end+length%2 > uint64(len(data)) {
			return nil, errAnimation
		}
		payload := data[offset+8 : int(end)]
		offset = int(end + length%2)
		switch name {
		case "VP8X":
			if len(payload) != 10 || canvas != nil || payload[0]&2 == 0 {
				return nil, errAnimation
			}
			w, h := little24(payload[4:7])+1, little24(payload[7:10])+1
			if w > 4096 || h > 4096 || w*h > 4*1024*1024 {
				return nil, errAnimation
			}
			canvas = image.NewRGBA(image.Rect(0, 0, w, h))
		case "ANIM":
			if canvas == nil || len(payload) != 6 || seenAnimation {
				return nil, errAnimation
			}
			seenAnimation = true
			background = color.NRGBA{B: payload[0], G: payload[1], R: payload[2], A: payload[3]}
			a.loops = binary.LittleEndian.Uint16(payload[4:])
			draw.Draw(canvas, canvas.Bounds(), &image.Uniform{C: background}, image.Point{}, draw.Src)
		case "ANMF":
			if canvas == nil || !seenAnimation || len(payload) < 16 || len(a.frames) >= 512 {
				return nil, errAnimation
			}
			x, y := little24(payload[:3])*2, little24(payload[3:6])*2
			w, h := little24(payload[6:9])+1, little24(payload[9:12])+1
			if int64(len(a.frames)+1)*int64(canvas.Bounds().Dx())*int64(canvas.Bounds().Dy()) > 16*1024*1024 {
				return nil, errAnimation
			}
			rect := image.Rect(x, y, x+w, y+h)
			if !rect.In(canvas.Bounds()) || payload[15]&0xfc != 0 {
				return nil, errAnimation
			}
			frameData := payload[16:]
			header := make([]byte, 10)
			if len(frameData) >= 4 && string(frameData[:4]) == "ALPH" {
				header[0] = 0x10
			}
			copy(header[4:7], payload[6:9])
			copy(header[7:10], payload[9:12])
			body := append(chunk("VP8X", header), frameData...)
			riff := make([]byte, 12, len(body)+12)
			copy(riff, "RIFF")
			binary.LittleEndian.PutUint32(riff[4:], uint32(len(body)+4))
			copy(riff[8:], "WEBP")
			riff = append(riff, body...)
			frame, err := webp.Decode(bytes.NewReader(riff))
			if err != nil || frame.Bounds().Dx() != w || frame.Bounds().Dy() != h {
				return nil, errAnimation
			}
			op := draw.Over
			if payload[15]&2 != 0 {
				op = draw.Src
			}
			draw.Draw(canvas, rect, frame, frame.Bounds().Min, op)
			// Each bitmap owns its pixels: later disposal must not mutate earlier frames.
			copyFrame := image.NewRGBA(canvas.Bounds())
			copy(copyFrame.Pix, canvas.Pix)
			duration := time.Duration(little24(payload[12:15])) * time.Millisecond
			// Browsers display <=10ms frames at 100ms; preserve that fallback.
			if duration <= 10*time.Millisecond {
				duration = 100 * time.Millisecond
			}
			a.frames = append(a.frames, animationFrame{ui.NewBitmap(copyFrame), duration})
			a.duration += duration
			if payload[15]&1 != 0 {
				draw.Draw(canvas, rect, &image.Uniform{C: background}, image.Point{}, draw.Src)
			}
		}
	}
	if len(a.frames) == 0 {
		return nil, errAnimation
	}
	return a, nil
}
func mustAnimation(data []byte) *animation {
	a, err := decodeAnimation(data)
	if err != nil {
		panic(err)
	}
	return a
}
func (a *animation) at(elapsed time.Duration) (*ui.Bitmap, time.Duration) {
	elapsed = max(0, elapsed)
	if a.loops > 0 && elapsed/a.duration >= time.Duration(a.loops) {
		return a.frames[len(a.frames)-1].bitmap, 0
	}
	elapsed %= a.duration
	for _, frame := range a.frames {
		if elapsed < frame.duration {
			return frame.bitmap, frame.duration - elapsed
		}
		elapsed -= frame.duration
	}
	return a.frames[0].bitmap, a.frames[0].duration
}
func (a *animation) Frame(c *ui.Context) ui.ImageSource {
	frame, next := a.at(c.Now().Sub(a.started))
	if next > 0 {
		c.After(next)
	}
	return frame
}
