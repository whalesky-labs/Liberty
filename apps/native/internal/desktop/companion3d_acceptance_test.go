//go:build companion3d

package desktop

import (
	"bytes"
	"image/png"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/egoist/mygo/native"
	"liberty.local/liberty/internal/desktop/presentation/companion3d"
	"liberty.local/liberty/internal/infrastructure/companion3d/metalbridge"
)

func TestCompanion3DNativeAcceptance(t *testing.T) {
	dir := os.Getenv("LIBERTY_PET3D_ACCEPTANCE_DIR")
	if runtime.GOOS != "darwin" || !filepath.IsAbs(dir) {
		t.Skip("opt-in native Metal model capture")
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	r, err := metalbridge.NewRenderer()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	for _, name := range []string{"crush", "rope", "work"} {
		model, err := companion3d.Load(name)
		if err != nil {
			t.Fatal(err)
		}
		texture := native.NewTexture()
		if err := r.Initialize(texture, model.Vertices, model.Joints(), 512); err != nil {
			t.Fatal(err)
		}
		for i, yaw := range []float64{0, .65, 2.7} {
			if err := r.Draw(model.Pose(.3, yaw, .12)); err != nil {
				t.Fatal(err)
			}
			data, err := r.Snapshot()
			if err != nil {
				t.Fatal(err)
			}
			img, err := png.Decode(bytes.NewReader(data))
			if err != nil {
				t.Fatal(err)
			}
			if img.Bounds().Dx() != 512 || img.Bounds().Dy() != 512 {
				t.Fatal("unexpected render size")
			}
			opaque, transparent := 0, 0
			for y := 0; y < 512; y++ {
				for x := 0; x < 512; x++ {
					_, _, _, alpha := img.At(x, y).RGBA()
					if alpha > 60000 {
						opaque++
					}
					if alpha == 0 {
						transparent++
					}
				}
			}
			if opaque < 5000 || transparent < 10000 {
				t.Fatal("model pixels or transparent background missing", opaque, transparent)
			}
			if err := os.WriteFile(filepath.Join(dir, name+string(rune('0'+i))+".png"), data, 0600); err != nil {
				t.Fatal(err)
			}
		}
		if err := r.Draw([]byte{1}); err == nil {
			t.Fatal("invalid pose accepted")
		}
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Snapshot(); err == nil {
		t.Fatal("closed renderer retained capture access")
	}
}
