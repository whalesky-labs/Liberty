// Package companion3d loads the repository's generated, articulated GLB pilots.
// It intentionally implements a small, validated glTF subset, not an importer
// for arbitrary user files. Production pets still use their current renderer.
package companion3d

import (
	"embed"
	"encoding/binary"
	"encoding/json"
	"errors"
	"math"
	"slices"
)

//go:embed assets/*.glb assets/manifest.json
var assets embed.FS

var ErrModel = errors.New("invalid_companion_pilot")

// Reviewable gates user-facing previews, not the isolated renderer tests.
func Reviewable() bool {
	data, err := assets.ReadFile("assets/manifest.json")
	if err != nil {
		return false
	}
	var manifest struct{ Status string }
	return json.Unmarshal(data, &manifest) == nil && manifest.Status == "pilot-review-required"
}

type accessor struct {
	BufferView, ComponentType, Count int
	Type                             string
}
type bufferView struct{ Buffer, ByteOffset, ByteLength int }
type node struct {
	Name                         string
	Mesh                         *int
	Children                     []int
	Translation, Rotation, Scale []float64
}
type primitive struct {
	Attributes        map[string]int
	Indices, Material int
}
type sampler struct {
	Input, Output int
	Interpolation string
}
type channel struct {
	Sampler int
	Target  struct {
		Node int
		Path string
	}
}
type document struct {
	Asset     struct{ Version string }
	Nodes     []node
	Meshes    []struct{ Primitives []primitive }
	Materials []struct {
		PBRMetallicRoughness struct{ BaseColorFactor []float64 }
	}
	Accessors   []accessor
	BufferViews []bufferView
	Animations  []struct {
		Name     string
		Samplers []sampler
		Channels []channel
	}
}
type track struct {
	target        int
	path          string
	times, values []float64
	stride        int
}
type Model struct {
	Name     string
	Vertices []byte
	nodes    []node
	parents  []int
	tracks   []track
	Duration float64
}

func Load(name string) (*Model, error) {
	if name != "crush" && name != "rope" && name != "work" {
		return nil, ErrModel
	}
	data, err := assets.ReadFile("assets/" + name + ".glb")
	if err != nil {
		return nil, ErrModel
	}
	return Decode(data)
}
func finite(v float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) }
func Decode(data []byte) (*Model, error) {
	if len(data) < 28 || len(data) > 8<<20 || binary.LittleEndian.Uint32(data) != 0x46546c67 || binary.LittleEndian.Uint32(data[4:]) != 2 || int(binary.LittleEndian.Uint32(data[8:])) != len(data) {
		return nil, ErrModel
	}
	length := int(binary.LittleEndian.Uint32(data[12:]))
	if length < 2 || length > len(data)-28 || binary.LittleEndian.Uint32(data[16:]) != 0x4e4f534a {
		return nil, ErrModel
	}
	var doc document
	if json.Unmarshal(data[20:20+length], &doc) != nil || doc.Asset.Version != "2.0" || len(doc.Nodes) == 0 || len(doc.Nodes) > 256 || len(doc.Animations) != 1 {
		return nil, ErrModel
	}
	binStart := 28 + length
	if binary.LittleEndian.Uint32(data[24+length:]) != 0x004e4942 || int(binary.LittleEndian.Uint32(data[20+length:])) != len(data)-binStart {
		return nil, ErrModel
	}
	bin := data[binStart:]
	read := func(id int) ([]float64, error) {
		if id < 0 || id >= len(doc.Accessors) {
			return nil, ErrModel
		}
		a := doc.Accessors[id]
		stride := map[string]int{"SCALAR": 1, "VEC3": 3, "VEC4": 4}[a.Type]
		if stride == 0 || a.Count < 1 || a.Count > 65536 || a.BufferView < 0 || a.BufferView >= len(doc.BufferViews) {
			return nil, ErrModel
		}
		v := doc.BufferViews[a.BufferView]
		bytes := 4
		if a.ComponentType == 5123 {
			bytes = 2
		} else if a.ComponentType != 5126 {
			return nil, ErrModel
		}
		count := a.Count * stride
		if v.Buffer != 0 || v.ByteOffset < 0 || v.ByteLength != count*bytes || v.ByteOffset > len(bin)-v.ByteLength {
			return nil, ErrModel
		}
		out := make([]float64, count)
		for i := range out {
			start := v.ByteOffset + i*bytes
			if bytes == 2 {
				out[i] = float64(binary.LittleEndian.Uint16(bin[start:]))
			} else {
				out[i] = float64(math.Float32frombits(binary.LittleEndian.Uint32(bin[start:])))
			}
			if !finite(out[i]) {
				return nil, ErrModel
			}
		}
		return out, nil
	}
	m := &Model{Name: doc.Animations[0].Name, nodes: doc.Nodes, parents: make([]int, len(doc.Nodes))}
	for i := range m.parents {
		m.parents[i] = -1
	}
	for i, n := range doc.Nodes {
		if len(n.Translation) != 3 || len(n.Rotation) != 4 || len(n.Scale) != 0 && len(n.Scale) != 3 {
			return nil, ErrModel
		}
		for _, v := range append(slices.Clone(n.Translation), n.Rotation...) {
			if !finite(v) {
				return nil, ErrModel
			}
		}
		for _, child := range n.Children {
			if child <= i || child >= len(doc.Nodes) || m.parents[child] != -1 {
				return nil, ErrModel
			}
			m.parents[child] = i
		}
		if n.Mesh == nil {
			continue
		}
		if *n.Mesh < 0 || *n.Mesh >= len(doc.Meshes) {
			return nil, ErrModel
		}
		for _, p := range doc.Meshes[*n.Mesh].Primitives {
			posID, pok := p.Attributes["POSITION"]
			normalID, nok := p.Attributes["NORMAL"]
			if !pok || !nok {
				return nil, ErrModel
			}
			pos, e1 := read(posID)
			normal, e2 := read(normalID)
			indexes, e3 := read(p.Indices)
			if e1 != nil || e2 != nil || e3 != nil || len(pos)%3 != 0 || len(normal) != len(pos) || len(indexes)%3 != 0 || p.Material < 0 || p.Material >= len(doc.Materials) {
				return nil, ErrModel
			}
			color := doc.Materials[p.Material].PBRMetallicRoughness.BaseColorFactor
			if len(color) != 4 {
				return nil, ErrModel
			}
			for _, v := range color {
				if !finite(v) || v < 0 || v > 1 {
					return nil, ErrModel
				}
			}
			for _, v := range indexes {
				index := int(v)
				if float64(index) != v || index < 0 || index >= len(pos)/3 {
					return nil, ErrModel
				}
				for _, value := range []float64{pos[index*3], pos[index*3+1], pos[index*3+2], normal[index*3], normal[index*3+1], normal[index*3+2], color[0], color[1], color[2], float64(i)} {
					m.Vertices = binary.LittleEndian.AppendUint32(m.Vertices, math.Float32bits(float32(value)))
				}
			}
			if len(m.Vertices) > 4<<20 {
				return nil, ErrModel
			}
		}
	}
	for i := 1; i < len(m.parents); i++ {
		if m.parents[i] < 0 {
			return nil, ErrModel
		}
	}
	animation := doc.Animations[0]
	for _, c := range animation.Channels {
		if c.Sampler < 0 || c.Sampler >= len(animation.Samplers) || c.Target.Node < 0 || c.Target.Node >= len(doc.Nodes) {
			return nil, ErrModel
		}
		s := animation.Samplers[c.Sampler]
		times, e1 := read(s.Input)
		values, e2 := read(s.Output)
		stride := 3
		if c.Target.Path == "rotation" {
			stride = 4
		} else if c.Target.Path != "translation" && c.Target.Path != "scale" {
			return nil, ErrModel
		}
		if e1 != nil || e2 != nil || s.Interpolation != "LINEAR" || len(times) < 2 || len(values) != len(times)*stride {
			return nil, ErrModel
		}
		for i, t := range times {
			if t < 0 || i > 0 && t <= times[i-1] {
				return nil, ErrModel
			}
		}
		m.Duration = max(m.Duration, times[len(times)-1])
		m.tracks = append(m.tracks, track{c.Target.Node, c.Target.Path, times, values, stride})
	}
	if m.Duration <= 0 || len(m.Vertices) == 0 {
		return nil, ErrModel
	}
	return m, nil
}
func (m *Model) Joints() int { return len(m.nodes) }

type matrix [16]float64

func multiply(a, b matrix) (out matrix) {
	for c := range 4 {
		for r := range 4 {
			for k := range 4 {
				out[c*4+r] += a[k*4+r] * b[c*4+k]
			}
		}
	}
	return
}
func transform(t, q, s []float64) matrix {
	x, y, z, w := q[0], q[1], q[2], q[3]
	d := math.Sqrt(x*x + y*y + z*z + w*w)
	if d == 0 {
		w = 1
	} else {
		x /= d
		y /= d
		z /= d
		w /= d
	}
	return matrix{(1 - 2*(y*y+z*z)) * s[0], 2 * (x*y + z*w) * s[0], 2 * (x*z - y*w) * s[0], 0, 2 * (x*y - z*w) * s[1], (1 - 2*(x*x+z*z)) * s[1], 2 * (y*z + x*w) * s[1], 0, 2 * (x*z + y*w) * s[2], 2 * (y*z - x*w) * s[2], (1 - 2*(x*x+y*y)) * s[2], 0, t[0], t[1], t[2], 1}
}

// Pose is absolute-time sampling: pausing/resuming cannot accumulate simulation
// drift. Every output is a GPU column-major matrix; index zero is the camera.
func (m *Model) Pose(seconds, yaw, pitch float64) []byte {
	if !finite(seconds) || !finite(yaw) || !finite(pitch) {
		seconds, yaw, pitch = 0, 0, 0
	}
	translations, rotations, scales := make([][]float64, len(m.nodes)), make([][]float64, len(m.nodes)), make([][]float64, len(m.nodes))
	for i, n := range m.nodes {
		translations[i], rotations[i], scales[i] = n.Translation, n.Rotation, []float64{1, 1, 1}
		if len(n.Scale) == 3 {
			scales[i] = n.Scale
		}
	}
	t := math.Mod(max(0, seconds), m.Duration)
	for _, tr := range m.tracks {
		i := 0
		for i+1 < len(tr.times)-1 && tr.times[i+1] <= t {
			i++
		}
		a := max(0, min(1, (t-tr.times[i])/(tr.times[i+1]-tr.times[i])))
		v := make([]float64, tr.stride)
		for k := range v {
			v[k] = tr.values[i*tr.stride+k]*(1-a) + tr.values[(i+1)*tr.stride+k]*a
		}
		switch tr.path {
		case "translation":
			translations[tr.target] = v
		case "rotation":
			rotations[tr.target] = v
		case "scale":
			scales[tr.target] = v
		}
	}
	unit := []float64{1, 1, 1}
	origin := []float64{0, 0, 0}
	view := multiply(transform([]float64{0, 0, -4}, []float64{math.Sin(pitch / 2), 0, 0, math.Cos(pitch / 2)}, unit), multiply(transform(origin, []float64{0, math.Sin(yaw / 2), 0, math.Cos(yaw / 2)}, unit), transform([]float64{0, -1, 0}, []float64{0, 0, 0, 1}, unit)))
	projection := matrix{1 / 1.35, 0, 0, 0, 0, 1 / 1.35, 0, 0, 0, 0, -1 / 10., 0, 0, 0, 0, 1}
	all := make([]matrix, len(m.nodes)+1)
	all[0] = multiply(projection, view)
	for i := range m.nodes {
		world := transform(translations[i], rotations[i], scales[i])
		if m.parents[i] >= 0 {
			world = multiply(all[m.parents[i]+1], world)
		}
		all[i+1] = world
	}
	out := make([]byte, 0, len(all)*64)
	for _, mat := range all {
		for _, v := range mat {
			out = binary.LittleEndian.AppendUint32(out, math.Float32bits(float32(v)))
		}
	}
	return out
}
