//go:build companion3d

package desktop

import (
	"bytes"
	"context"
	"encoding/json"
	"image/png"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/egoist/mygo/native"
	"liberty.local/liberty/internal/desktop/presentation/companion3d"
	"liberty.local/liberty/internal/infrastructure/companion3d/metalbridge"
	"liberty.local/liberty/internal/infrastructure/system"
)

func TestCompanionPixelNativeAcceptance(t *testing.T) {
	dir := os.Getenv("LIBERTY_PET3D_ACCEPTANCE_DIR")
	assets := os.Getenv("LIBERTY_PET3D_ASSET_DIR")
	if runtime.GOOS != "darwin" || !filepath.IsAbs(dir) || !filepath.IsAbs(assets) {
		t.Skip("opt-in native pixel Metal acceptance")
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	library, err := companion3d.OpenPixelLibrary(os.DirFS(assets))
	if err != nil {
		t.Fatal(err)
	}
	r, err := metalbridge.NewRenderer()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	type result struct {
		Action           string
		UploadMS, DrawMS float64
		BufferBytes      int64
	}
	var results []result
	for _, asset := range library.Actions() {
		m, err := library.Load(asset.ID)
		if err != nil {
			t.Fatal(err)
		}
		texture := native.NewTexture()
		started := time.Now()
		if err = uploadCompanionPixel(r, texture, m, 418); err != nil {
			t.Fatal(asset.ID, err)
		}
		upload := time.Since(started)
		expected := int64(len(m.Vertices) + len(m.Morphs) + len(m.Indices) + (m.JointCount+1)*64)
		m.ReleaseGeometry()
		var packet, first []byte
		var total time.Duration
		for pose := range 9 {
			packet = m.Pose(packet, float64(pose)*.3+1e-6, 0, 0)
			started = time.Now()
			if err = r.DrawPixel(packet, pose, false); err != nil {
				t.Fatal(asset.ID, pose, err)
			}
			total += time.Since(started)
			data, err := r.Snapshot()
			if err != nil {
				t.Fatal(err)
			}
			img, err := png.Decode(bytes.NewReader(data))
			if err != nil {
				t.Fatal(err)
			}
			opaque, transparent := 0, 0
			for y := 0; y < 418; y++ {
				for x := 0; x < 418; x++ {
					_, _, _, a := img.At(x, y).RGBA()
					if a > 60000 {
						opaque++
					}
					if a == 0 {
						transparent++
					}
				}
			}
			if img.Bounds().Dx() != 418 || opaque < 3000 || transparent < 12000 {
				t.Fatal(asset.ID, pose, "missing geometry or alpha", opaque, transparent)
			}
			if pose == 0 {
				first = data
			} else if pose == 4 && bytes.Equal(first, data) {
				t.Fatal("missing skeletal/morph animation", asset.ID)
			}
			if err = os.WriteFile(filepath.Join(dir, asset.ID+"-"+string(rune('1'+pose))+".png"), data, 0600); err != nil {
				t.Fatal(err)
			}
		}
		stats, err := r.Stats()
		if err != nil {
			t.Fatal(err)
		}
		owned := stats.VertexBytes + stats.MorphBytes + stats.IndexBytes + stats.PoseBytes
		if owned != expected {
			t.Fatal("retained previous action buffers", asset.ID, owned, expected)
		}
		results = append(results, result{asset.ID, float64(upload.Microseconds()) / 1000, float64(total.Microseconds()) / 9000, owned})
		if err = r.Unload(); err != nil {
			t.Fatal(err)
		}
		stats, err = r.Stats()
		if err != nil || stats.VertexBytes+stats.MorphBytes+stats.IndexBytes+stats.PoseBytes != 0 {
			t.Fatal("unload retained buffers", stats, err)
		}
		if err = r.DrawPixel(packet, 0, false); err == nil {
			t.Fatal("draw after unload")
		}
	}
	// Repeat complete upload/draw/unload cycles; validate native owned resources,
	// not RSS fluctuations or GC timing, for deterministic lifecycle evidence.
	m, err := library.Load("work")
	if err != nil {
		t.Fatal(err)
	}
	for range 20 {
		if err = uploadCompanionPixel(r, native.NewTexture(), m, 256); err != nil {
			t.Fatal(err)
		}
		if err = r.DrawPixel(m.Pose(nil, 0, 0, 0), 0, false); err != nil {
			t.Fatal(err)
		}
		if err = r.Unload(); err != nil {
			t.Fatal(err)
		}
	}
	stats, err := r.Stats()
	if err != nil || stats.VertexBytes+stats.MorphBytes+stats.IndexBytes+stats.PoseBytes != 0 {
		t.Fatal("cycle leak", stats, err)
	}
	if err = r.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err = r.Stats(); err == nil {
		t.Fatal("closed renderer accessible")
	}
	data, _ := json.MarshalIndent(struct {
		Actions      []result
		UnloadCycles int
		Scope        string
	}{results, 20, "Native GPU captures and owned mesh buffers; not full-app CPU/memory acceptance."}, "", "  ")
	if err = os.WriteFile(filepath.Join(dir, "native-results.json"), append(data, '\n'), 0600); err != nil {
		t.Fatal(err)
	}
}

// This measures the isolated renderer process, not the complete client or its
// MyGo compositor. Keep it opt-in and report its scope with every measurement.
func TestCompanionPixelNativeResources(t *testing.T) {
	dir, assets := os.Getenv("LIBERTY_PET3D_ACCEPTANCE_DIR"), os.Getenv("LIBERTY_PET3D_ASSET_DIR")
	if runtime.GOOS != "darwin" || !filepath.IsAbs(dir) || !filepath.IsAbs(assets) {
		t.Skip("opt-in native renderer resource sample")
	}
	library, err := companion3d.OpenPixelLibrary(os.DirFS(assets))
	if err != nil {
		t.Fatal(err)
	}
	r, err := metalbridge.NewRenderer()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	var sampler system.MetricsSampler
	baseline, err := sampler.Sample(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	m, err := library.Load("work")
	if err != nil {
		t.Fatal(err)
	}
	if err = uploadCompanionPixel(r, native.NewTexture(), m, 320); err != nil {
		t.Fatal(err)
	}
	m.ReleaseGeometry()
	runtime.GC()
	if _, err = sampler.Sample(context.Background()); err != nil {
		t.Fatal(err)
	}
	frames, metrics := time.NewTicker(300*time.Millisecond), time.NewTicker(time.Second)
	defer frames.Stop()
	defer metrics.Stop()
	type sample struct {
		CPUPercent   float64
		FootprintMiB float64
	}
	values := make([]sample, 0, 30)
	packet := make([]byte, 0, (m.JointCount+1)*64)
	pose := 0
	for len(values) < 30 {
		select {
		case <-frames.C:
			packet = m.Pose(packet, float64(pose)*.3+1e-6, 0, 0)
			if err = r.DrawPixel(packet, pose, false); err != nil {
				t.Fatal(err)
			}
			pose = (pose + 1) % 9
		case <-metrics.C:
			value, err := sampler.Sample(context.Background())
			if err != nil || value.CPUPercent == nil {
				t.Fatal("sample unavailable", err)
			}
			values = append(values, sample{*value.CPUPercent, float64(value.MemoryBytes) / (1 << 20)})
		}
	}
	stats, err := r.Stats()
	if err != nil {
		t.Fatal(err)
	}
	mean, peak, footprint := 0., 0., 0.
	for _, value := range values {
		mean += value.CPUPercent
		peak = max(peak, value.CPUPercent)
		footprint = max(footprint, value.FootprintMiB)
	}
	mean /= float64(len(values))
	if stats.DrawCalls < 95 || stats.DrawCalls > 105 {
		t.Fatal("unexpected frame cadence", stats.DrawCalls)
	}
	if err = r.Unload(); err != nil {
		t.Fatal(err)
	}
	statsAfter, err := r.Stats()
	if err != nil || statsAfter.VertexBytes+statsAfter.MorphBytes+statsAfter.IndexBytes+statsAfter.PoseBytes != 0 {
		t.Fatal("resource release", err)
	}
	report := struct {
		Scope                                                                  string
		Seconds                                                                int
		DrawCalls                                                              int64
		MeanCPUPercent, PeakCPUPercent, BaselineFootprintMiB, PeakFootprintMiB float64
		Samples                                                                []sample
	}{"Isolated Metal renderer test process, work action at 320 px / 300 ms, no MyGo windows or PNG baseline; 100% CPU is one core. Not full-client acceptance.", 30, stats.DrawCalls, mean, peak, float64(baseline.MemoryBytes) / (1 << 20), footprint, values}
	data, _ := json.MarshalIndent(report, "", "  ")
	if err = os.WriteFile(filepath.Join(dir, "native-resource-sample.json"), append(data, '\n'), 0600); err != nil {
		t.Fatal(err)
	}
	t.Logf("isolated renderer: mean CPU %.2f%%, peak footprint %.1f MiB, %d draws; model buffers released", mean, footprint, stats.DrawCalls)
}
