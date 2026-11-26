package companion3d

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"testing"
	"testing/fstest"
)

func pixelFixtureRoot(t testing.TB) string {
	t.Helper()
	return filepath.Join("..", "..", "..", "..", "..", "..", "packages", "desktop-resources", "pet", "models", "candidates", "pixel-actions")
}

func TestPixelLibraryRejectsUnreviewedAssetRevision(t *testing.T) {
	root := pixelFixtureRoot(t)
	manifest, err := os.ReadFile(filepath.Join(root, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	review, err := os.ReadFile(filepath.Join(root, "native-review.json"))
	if err != nil {
		t.Fatal(err)
	}
	files := fstest.MapFS{"manifest.json": {Data: manifest}, "native-review.json": {Data: review}}
	if _, err := OpenPixelLibrary(files); err != nil {
		t.Fatal(err)
	}
	var changed map[string]any
	if err := json.Unmarshal(review, &changed); err != nil {
		t.Fatal(err)
	}
	changed["actionSHA256"].(map[string]any)["work"] = string(bytes.Repeat([]byte{'0'}, 64))
	data, _ := json.Marshal(changed)
	files["native-review.json"] = &fstest.MapFile{Data: data}
	if _, err := OpenPixelLibrary(files); err == nil {
		t.Fatal("stale visual review accepted")
	}
	delete(files, "native-review.json")
	if _, err := OpenPixelLibrary(files); err == nil {
		t.Fatal("missing review accepted")
	}
}
func pixelLibrary(t testing.TB) *PixelLibrary {
	t.Helper()
	l, err := OpenPixelLibrary(os.DirFS(pixelFixtureRoot(t)))
	if err != nil {
		t.Fatal(err)
	}
	return l
}
func packedFloat(data []byte, index int) float64 {
	return float64(math.Float32frombits(binary.LittleEndian.Uint32(data[index*4:])))
}
func pixelPosition(m *PixelModel, index, pose int) [3]float64 {
	base := m.Vertices[index*PixelVertexStride:]
	point := [4]float64{packedFloat(base, 0), packedFloat(base, 1), packedFloat(base, 2), 1}
	if pose > 0 {
		for k := range 3 {
			point[k] += packedFloat(m.Morphs, index*24+(pose-1)*3+k)
		}
	}
	var result [3]float64
	for k := range 4 {
		weight := packedFloat(base, 12+k)
		joint := m.poses[pose][int(packedFloat(base, 8+k))]
		for row := range 3 {
			for col := range 4 {
				result[row] += weight * joint[col*4+row] * point[col]
			}
		}
	}
	return result
}

func TestPixelAllActionsMatchIndependentSkinAndMorphSamples(t *testing.T) {
	var reference struct {
		Actions []struct {
			ID, SHA256 string
			Samples    []struct {
				Part    string
				Indices []int
				Poses   [][][3]float64
			}
		}
	}
	data, err := os.ReadFile(filepath.Join(pixelFixtureRoot(t), "native-reference.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(data, &reference); err != nil {
		t.Fatal(err)
	}
	library := pixelLibrary(t)
	assets := library.Actions()
	if len(reference.Actions) != 15 {
		t.Fatal("missing reference actions")
	}
	for index, action := range reference.Actions {
		t.Run(action.ID, func(t *testing.T) {
			if assets[index].SHA256 != action.SHA256 {
				t.Fatal("stale independent reference")
			}
			m, err := library.Load(action.ID)
			if err != nil {
				t.Fatal(err)
			}
			if m.Triangles < 10000 || m.VertexCount > maxPixelVertices || len(m.Morphs) != m.VertexCount*PixelMorphStride {
				t.Fatal("missing pixel geometry")
			}
			parts := make(map[string]PixelPart)
			for _, p := range m.Parts {
				parts[p.Name] = p
			}
			for _, sample := range action.Samples {
				part, ok := parts[sample.Part]
				if !ok || len(sample.Poses) != 9 {
					t.Fatal("missing part", sample.Part)
				}
				for pose, expected := range sample.Poses {
					for i, value := range expected {
						vertex := sample.Indices[i]
						if vertex < 0 || vertex >= part.Count {
							t.Fatal("sample index")
						}
						actual := pixelPosition(m, part.First+vertex, pose)
						for k := range 3 {
							if math.Abs(actual[k]-value[k]) > 3e-5 {
								t.Fatalf("%s pose %d vertex %d axis %d: %g != %g", sample.Part, pose, vertex, k, actual[k], value[k])
							}
						}
					}
				}
			}
			var packet []byte
			for pose := range 9 {
				start := float64(pose) * .3
				want := m.Pose(nil, start+1e-6, .4, .1)
				for _, offset := range []float64{.01, .1, .2, .29999} {
					packet = m.Pose(packet, start+offset, .4, .1)
					if !bytes.Equal(want, packet) || PixelPoseIndex(start+offset) != pose {
						t.Fatal("separated pose between keys")
					}
				}
			}
			if !bytes.Equal(m.Pose(nil, 0, 0, 0), m.Pose(nil, 2.7, 0, 0)) {
				t.Fatal("loop seam")
			}
			m.ReleaseGeometry()
			if len(m.Vertices)+len(m.Morphs)+len(m.Indices) != 0 || len(m.Pose(packet, .6, 0, 0)) != (m.JointCount+1)*64 {
				t.Fatal("upload release")
			}
		})
	}
}

func rewritePixel(t *testing.T, original []byte, change func(map[string]any)) []byte {
	t.Helper()
	n := int(binary.LittleEndian.Uint32(original[12:]))
	var d map[string]any
	if err := json.Unmarshal(original[20:20+n], &d); err != nil {
		t.Fatal(err)
	}
	change(d)
	text, err := json.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	for len(text)%4 != 0 {
		text = append(text, ' ')
	}
	rest := original[20+n:]
	out := make([]byte, 20)
	binary.LittleEndian.PutUint32(out, 0x46546c67)
	binary.LittleEndian.PutUint32(out[4:], 2)
	binary.LittleEndian.PutUint32(out[8:], uint32(20+len(text)+len(rest)))
	binary.LittleEndian.PutUint32(out[12:], uint32(len(text)))
	binary.LittleEndian.PutUint32(out[16:], 0x4e4f534a)
	return append(append(out, text...), rest...)
}
func TestPixelRejectsMalformedAndDesynchronizedData(t *testing.T) {
	data, err := os.ReadFile(filepath.Join(pixelFixtureRoot(t), "crush.glb"))
	if err != nil {
		t.Fatal(err)
	}
	cases := map[string]func(map[string]any){
		"cycle": func(d map[string]any) { d["nodes"].([]any)[0].(map[string]any)["children"] = []any{0} },
		"external buffer": func(d map[string]any) {
			d["buffers"].([]any)[0].(map[string]any)["uri"] = "https://example.invalid/model.bin"
		},
		"overflow view":      func(d map[string]any) { d["bufferViews"].([]any)[0].(map[string]any)["byteOffset"] = 1e15 },
		"oversized accessor": func(d map[string]any) { d["accessors"].([]any)[0].(map[string]any)["count"] = 1e9 },
		"interpolated bones": func(d map[string]any) {
			d["animations"].([]any)[0].(map[string]any)["samplers"].([]any)[0].(map[string]any)["interpolation"] = "LINEAR"
		},
		"missing skin":  func(d map[string]any) { d["skins"] = []any{} },
		"invalid joint": func(d map[string]any) { d["skins"].([]any)[0].(map[string]any)["joints"].([]any)[0] = 999 },
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := DecodePixel(rewritePixel(t, data, change)); err == nil {
				t.Fatal("invalid model accepted")
			}
		})
	}
	for _, cut := range []int{0, 12, 28, len(data) - 1} {
		if _, err := DecodePixel(data[:cut]); err == nil {
			t.Fatal("truncated model")
		}
	}
	library := pixelLibrary(t)
	if _, err := library.Load("../../secret"); err == nil {
		t.Fatal("path accepted")
	}
	for _, seconds := range []float64{math.NaN(), math.Inf(1), -1, 0, 2.7} {
		if PixelPoseIndex(seconds) != 0 {
			t.Fatal("invalid pose time")
		}
	}
}

func BenchmarkPixelPose(b *testing.B) {
	m, err := pixelLibrary(b).Load("work")
	if err != nil {
		b.Fatal(err)
	}
	m.ReleaseGeometry()
	packet := make([]byte, 0, (m.JointCount+1)*64)
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		packet = m.Pose(packet, 1.35, .3, 0)
	}
}
