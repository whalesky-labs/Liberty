// Package metalbridge is a native module: its interfaces are implemented in
// Swift, Rust or Zig, in the directory of the language (see AGENTS.md).
package metalbridge

import "github.com/egoist/mygo/native"

// Renderer is a serial, explicitly closed experimental Metal mesh renderer.
//
//mygo:native
type Renderer interface {
	Initialize(texture *native.Texture, vertices []byte, joints int, size int) error
	Draw(matrices []byte) error
	// InitializePixel uploads one indexed action, its eight morph deltas and
	// four-joint skin weights. Reinitializing releases the previous action.
	InitializePixel(texture *native.Texture, vertices *native.Buffer, morphs *native.Buffer, indices *native.Buffer, joints int, size int) error
	DrawPixel(matrices []byte, pose int, lighting bool) error
	Stats() (RenderStats, error)
	Unload() error
	// Snapshot is for acceptance captures only, never the live UI render path.
	Snapshot() ([]byte, error)
	Close() error
}

// RenderStats reports owned buffer allocations, not process/GPU memory usage.
type RenderStats struct {
	VertexBytes int64
	MorphBytes  int64
	IndexBytes  int64
	PoseBytes   int64
	DrawCalls   int64
}
