//go:build companion3d

package desktop

import (
	"github.com/egoist/mygo/native"
	"liberty.local/liberty/internal/desktop/presentation/companion3d"
	"liberty.local/liberty/internal/infrastructure/companion3d/metalbridge"
	"runtime"
)

// NativeBuffer avoids serializing tens of MiB into the Go/Swift call envelope
// and then copying that envelope into three Swift Data values. Only the small
// pose packets use the byte codec during playback.
func uploadCompanionPixel(r metalbridge.Renderer, texture *native.Texture, m *companion3d.PixelModel, size int) error {
	vertices, morphs, indices := native.NewBuffer(len(m.Vertices)), native.NewBuffer(len(m.Morphs)), native.NewBuffer(len(m.Indices))
	copy(vertices.Bytes(), m.Vertices)
	copy(morphs.Bytes(), m.Morphs)
	copy(indices.Bytes(), m.Indices)
	err := r.InitializePixel(texture, vertices, morphs, indices, m.JointCount, size)
	runtime.KeepAlive(vertices)
	runtime.KeepAlive(morphs)
	runtime.KeepAlive(indices)
	return err
}
