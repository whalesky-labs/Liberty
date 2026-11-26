package companion3d

import (
	"encoding/binary"
	"encoding/json"
	"math"
)

const (
	PixelPoseCount    = 9
	PixelTargets      = 8
	PixelVertexStride = 64
	PixelMorphStride  = PixelTargets * 12
	maxPixelVertices  = 200000
	maxPixelIndices   = 450000
)

type pixelAccessor struct {
	BufferView                       *int
	ByteOffset, ComponentType, Count int
	Type                             string
	Normalized                       bool
	Sparse                           json.RawMessage
}
type pixelView struct{ Buffer, ByteOffset, ByteLength, ByteStride int }
type pixelNode struct {
	Name                                 string
	Mesh, Skin                           *int
	Children                             []int
	Translation, Rotation, Scale, Matrix []float64
}
type pixelPrimitive struct {
	Attributes map[string]int
	Indices    *int
	Targets    []map[string]int
	Mode       *int
}
type pixelDocument struct {
	Asset  struct{ Version string }
	Nodes  []pixelNode
	Meshes []struct {
		Name       string
		Primitives []pixelPrimitive
	}
	Skins []struct {
		Joints              []int
		InverseBindMatrices *int
	}
	Accessors   []pixelAccessor
	BufferViews []pixelView
	Buffers     []struct {
		ByteLength int
		URI        string
	}
	Animations []struct {
		Name     string
		Samplers []sampler
		Channels []channel
	}
	Images, Textures   []json.RawMessage
	ExtensionsRequired []string
}

type pixelReader struct {
	doc  *pixelDocument
	data []byte
}
type pixelValues struct {
	data                                  []byte
	count, width, component, size, stride int
	normalized                            bool
}

func (r pixelReader) values(id int, typ string) (pixelValues, error) {
	if id < 0 || id >= len(r.doc.Accessors) {
		return pixelValues{}, ErrModel
	}
	a := r.doc.Accessors[id]
	width := map[string]int{"SCALAR": 1, "VEC3": 3, "VEC4": 4, "MAT4": 16}[a.Type]
	size := map[int]int{5121: 1, 5123: 2, 5125: 4, 5126: 4}[a.ComponentType]
	if a.Type != typ || width == 0 || size == 0 || a.Count < 1 || a.Count > maxPixelIndices || a.ByteOffset < 0 || len(a.Sparse) != 0 || a.BufferView == nil || *a.BufferView < 0 || *a.BufferView >= len(r.doc.BufferViews) {
		return pixelValues{}, ErrModel
	}
	v := r.doc.BufferViews[*a.BufferView]
	stride := v.ByteStride
	if stride == 0 {
		stride = width * size
	}
	if v.Buffer != 0 || v.ByteOffset < 0 || v.ByteLength < 0 || v.ByteOffset > len(r.data) || v.ByteLength > len(r.data)-v.ByteOffset || stride < width*size || stride > 256 || a.ByteOffset%size != 0 || a.ByteOffset > v.ByteLength || (a.Count-1)*stride+width*size > v.ByteLength-a.ByteOffset || a.Normalized && a.ComponentType != 5121 && a.ComponentType != 5123 {
		return pixelValues{}, ErrModel
	}
	out := pixelValues{r.data[v.ByteOffset+a.ByteOffset : v.ByteOffset+v.ByteLength], a.Count, width, a.ComponentType, size, stride, a.Normalized}
	for i := 0; i < out.count; i++ {
		for k := 0; k < out.width; k++ {
			if !finite(out.at(i, k)) {
				return pixelValues{}, ErrModel
			}
		}
	}
	return out, nil
}
func (v pixelValues) at(i, k int) float64 {
	b := v.data[i*v.stride+k*v.size:]
	switch v.component {
	case 5121:
		x := float64(b[0])
		if v.normalized {
			return x / 255
		}
		return x
	case 5123:
		x := float64(binary.LittleEndian.Uint16(b))
		if v.normalized {
			return x / 65535
		}
		return x
	case 5125:
		return float64(binary.LittleEndian.Uint32(b))
	default:
		return float64(math.Float32frombits(binary.LittleEndian.Uint32(b)))
	}
}
func pixelFloat(dst []byte, value float64) []byte {
	return binary.LittleEndian.AppendUint32(dst, math.Float32bits(float32(value)))
}

type PixelPart struct {
	Name         string
	First, Count int
}

// PixelModel owns one action's indexed geometry. Positions, eight corrective
// targets and four-joint weights are uploaded once; sampling changes only a
// small matrix packet and a synchronized pose index. It never caches nine
// copies of the complete mesh and does not read textures back through the CPU.
type PixelModel struct {
	Name                               string
	Vertices, Morphs, Indices          []byte
	Parts                              []PixelPart
	Triangles, VertexCount, JointCount int
	poses                              [PixelPoseCount][]matrix
}

func DecodePixel(data []byte) (*PixelModel, error) {
	if len(data) < 28 || len(data) > 18<<20 || binary.LittleEndian.Uint32(data) != 0x46546c67 || binary.LittleEndian.Uint32(data[4:]) != 2 || int(binary.LittleEndian.Uint32(data[8:])) != len(data) {
		return nil, ErrModel
	}
	n := int(binary.LittleEndian.Uint32(data[12:]))
	if n < 2 || n > 1<<20 || n > len(data)-28 || n%4 != 0 || binary.LittleEndian.Uint32(data[16:]) != 0x4e4f534a || binary.LittleEndian.Uint32(data[24+n:]) != 0x004e4942 || int(binary.LittleEndian.Uint32(data[20+n:])) != len(data)-28-n {
		return nil, ErrModel
	}
	var d pixelDocument
	if json.Unmarshal(data[20:20+n], &d) != nil || d.Asset.Version != "2.0" || len(d.Nodes) == 0 || len(d.Nodes) > 256 || len(d.Meshes) == 0 || len(d.Meshes) > 64 || len(d.Skins) != 1 || len(d.Skins[0].Joints) < 1 || len(d.Skins[0].Joints) > 64 || len(d.Animations) != 1 || len(d.Animations[0].Channels) > 1024 || len(d.Accessors) > 2048 || len(d.BufferViews) > 2048 || len(d.Buffers) != 1 || d.Buffers[0].URI != "" || len(d.Images) != 0 || len(d.Textures) != 0 || len(d.ExtensionsRequired) != 0 {
		return nil, ErrModel
	}
	bin := data[28+n:]
	if d.Buffers[0].ByteLength > len(bin) || d.Buffers[0].ByteLength < len(bin)-3 {
		return nil, ErrModel
	}
	r := pixelReader{&d, bin}
	m := &PixelModel{Name: d.Animations[0].Name, JointCount: len(d.Skins[0].Joints)}
	parents := make([]int, len(d.Nodes))
	for i := range parents {
		parents[i] = -1
	}
	for i := range d.Nodes {
		nd := &d.Nodes[i]
		if len(nd.Matrix) != 0 {
			return nil, ErrModel
		}
		if len(nd.Translation) == 0 {
			nd.Translation = []float64{0, 0, 0}
		}
		if len(nd.Rotation) == 0 {
			nd.Rotation = []float64{0, 0, 0, 1}
		}
		if len(nd.Scale) == 0 {
			nd.Scale = []float64{1, 1, 1}
		}
		if len(nd.Translation) != 3 || len(nd.Rotation) != 4 || len(nd.Scale) != 3 {
			return nil, ErrModel
		}
		for _, list := range [][]float64{nd.Translation, nd.Rotation, nd.Scale} {
			for _, v := range list {
				if !finite(v) || math.Abs(v) > 1000 {
					return nil, ErrModel
				}
			}
		}
		for _, child := range nd.Children {
			if child < 0 || child >= len(d.Nodes) || child == i || parents[child] != -1 {
				return nil, ErrModel
			}
			parents[child] = i
		}
	}
	order := make([]int, 0, len(d.Nodes))
	seen := make([]uint8, len(d.Nodes))
	var visit func(int) bool
	visit = func(i int) bool {
		if seen[i] == 1 {
			return false
		}
		if seen[i] == 2 {
			return true
		}
		seen[i] = 1
		if parents[i] >= 0 && !visit(parents[i]) {
			return false
		}
		seen[i] = 2
		order = append(order, i)
		return true
	}
	for i := range d.Nodes {
		if !visit(i) {
			return nil, ErrModel
		}
	}
	if d.Skins[0].InverseBindMatrices == nil {
		return nil, ErrModel
	}
	ibm, err := r.values(*d.Skins[0].InverseBindMatrices, "MAT4")
	if err != nil || ibm.component != 5126 || ibm.count != m.JointCount {
		return nil, ErrModel
	}
	inverse := make([]matrix, m.JointCount)
	jointSeen := make(map[int]bool)
	for i, j := range d.Skins[0].Joints {
		if j < 0 || j >= len(d.Nodes) || jointSeen[j] {
			return nil, ErrModel
		}
		jointSeen[j] = true
		for k := range 16 {
			inverse[i][k] = ibm.at(i, k)
		}
	}
	// Parse the shared nine-pose contract before allocating the larger buffers.
	tracks := make(map[[2]int]pixelValues)
	morphNodes := make(map[int]bool)
	for _, c := range d.Animations[0].Channels {
		if c.Target.Node < 0 || c.Target.Node >= len(d.Nodes) || c.Sampler < 0 || c.Sampler >= len(d.Animations[0].Samplers) {
			return nil, ErrModel
		}
		s := d.Animations[0].Samplers[c.Sampler]
		times, e := r.values(s.Input, "SCALAR")
		if e != nil || s.Interpolation != "STEP" || times.component != 5126 || times.count != 10 {
			return nil, ErrModel
		}
		for i := range 10 {
			if math.Abs(times.at(i, 0)-float64(i)*.3) > 1e-5 {
				return nil, ErrModel
			}
		}
		kind, typ := 0, "VEC3"
		switch c.Target.Path {
		case "translation":
		case "rotation":
			kind, typ = 1, "VEC4"
		case "scale":
			kind = 2
		case "weights":
			kind, typ = 3, "SCALAR"
		default:
			return nil, ErrModel
		}
		key := [2]int{c.Target.Node, kind}
		if _, ok := tracks[key]; ok {
			return nil, ErrModel
		}
		values, e := r.values(s.Output, typ)
		if e != nil || values.component != 5126 {
			return nil, ErrModel
		}
		if kind == 3 {
			if values.count != 80 || d.Nodes[c.Target.Node].Mesh == nil {
				return nil, ErrModel
			}
			for i := range 10 {
				for k := range 8 {
					expected := 0.
					if i > 0 && i < 9 && k == i-1 {
						expected = 1
					}
					if math.Abs(values.at(i*8+k, 0)-expected) > 1e-6 {
						return nil, ErrModel
					}
				}
			}
			morphNodes[c.Target.Node] = true
		} else {
			if values.count != 10 {
				return nil, ErrModel
			}
			for k := range values.width {
				if math.Abs(values.at(0, k)-values.at(9, k)) > 1e-5 {
					return nil, ErrModel
				}
			}
		}
		tracks[key] = values
	}
	for frame := range PixelPoseCount {
		world := make([]matrix, len(d.Nodes))
		for _, i := range order {
			t, q, s := d.Nodes[i].Translation, d.Nodes[i].Rotation, d.Nodes[i].Scale
			values := [][]float64{append([]float64(nil), t...), append([]float64(nil), q...), append([]float64(nil), s...)}
			for kind := range 3 {
				if tr, ok := tracks[[2]int{i, kind}]; ok {
					for k := range values[kind] {
						values[kind][k] = tr.at(frame, k)
					}
				}
			}
			world[i] = transform(values[0], values[1], values[2])
			if parents[i] >= 0 {
				world[i] = multiply(world[parents[i]], world[i])
			}
		}
		m.poses[frame] = make([]matrix, m.JointCount)
		for i, j := range d.Skins[0].Joints {
			m.poses[frame][i] = multiply(world[j], inverse[i])
			for _, v := range m.poses[frame][i] {
				if !finite(v) || math.Abs(v) > 10000 {
					return nil, ErrModel
				}
			}
		}
	}
	// Limit totals before allocation, independent of repeated accessor aliases.
	totalVertices, totalIndices := 0, 0
	for _, nd := range d.Nodes {
		if nd.Mesh == nil {
			continue
		}
		if *nd.Mesh < 0 || *nd.Mesh >= len(d.Meshes) || nd.Skin == nil || *nd.Skin != 0 {
			return nil, ErrModel
		}
		for _, p := range d.Meshes[*nd.Mesh].Primitives {
			id, ok := p.Attributes["POSITION"]
			if !ok || p.Indices == nil {
				return nil, ErrModel
			}
			pos, e1 := r.values(id, "VEC3")
			idx, e2 := r.values(*p.Indices, "SCALAR")
			if e1 != nil || e2 != nil {
				return nil, ErrModel
			}
			totalVertices += pos.count
			totalIndices += idx.count
			if totalVertices > maxPixelVertices || totalIndices > maxPixelIndices {
				return nil, ErrModel
			}
		}
	}
	m.Vertices = make([]byte, 0, totalVertices*PixelVertexStride)
	m.Morphs = make([]byte, 0, totalVertices*PixelMorphStride)
	m.Indices = make([]byte, 0, totalIndices*4)
	for nodeID, nd := range d.Nodes {
		if nd.Mesh == nil {
			continue
		}
		mesh := d.Meshes[*nd.Mesh]
		if !morphNodes[nodeID] {
			return nil, ErrModel
		}
		first := m.VertexCount
		for _, p := range mesh.Primitives {
			if p.Mode != nil && *p.Mode != 4 || len(p.Targets) != 8 {
				return nil, ErrModel
			}
			readAttr := func(name, typ string) (pixelValues, error) {
				id, ok := p.Attributes[name]
				if !ok {
					return pixelValues{}, ErrModel
				}
				return r.values(id, typ)
			}
			pos, e1 := readAttr("POSITION", "VEC3")
			color, e2 := readAttr("COLOR_0", "VEC4")
			joints, e3 := readAttr("JOINTS_0", "VEC4")
			weights, e4 := readAttr("WEIGHTS_0", "VEC4")
			idx, e5 := r.values(*p.Indices, "SCALAR")
			if e1 != nil || e2 != nil || e3 != nil || e4 != nil || e5 != nil || pos.component != 5126 || color.count != pos.count || joints.count != pos.count || weights.count != pos.count || joints.normalized || joints.component != 5121 && joints.component != 5123 || idx.normalized || idx.component == 5126 || idx.count%3 != 0 {
				return nil, ErrModel
			}
			var targets [8]pixelValues
			for k, target := range p.Targets {
				id, ok := target["POSITION"]
				if !ok || len(target) != 1 {
					return nil, ErrModel
				}
				v, e := r.values(id, "VEC3")
				if e != nil || v.component != 5126 || v.count != pos.count {
					return nil, ErrModel
				}
				targets[k] = v
			}
			for i := range pos.count {
				for k := range 3 {
					v := pos.at(i, k)
					if math.Abs(v) > 20 {
						return nil, ErrModel
					}
					m.Vertices = pixelFloat(m.Vertices, v)
				}
				m.Vertices = pixelFloat(m.Vertices, 1)
				for k := range 4 {
					v := color.at(i, k)
					if v < 0 || v > 1 {
						return nil, ErrModel
					}
					m.Vertices = pixelFloat(m.Vertices, v)
				}
				for k := range 4 {
					v := joints.at(i, k)
					if v < 0 || v >= float64(m.JointCount) || v != math.Trunc(v) {
						return nil, ErrModel
					}
					m.Vertices = pixelFloat(m.Vertices, v)
				}
				sum := 0.
				for k := range 4 {
					v := weights.at(i, k)
					if v < 0 || v > 1 {
						return nil, ErrModel
					}
					sum += v
					m.Vertices = pixelFloat(m.Vertices, v)
				}
				if math.Abs(sum-1) > 1e-5 {
					return nil, ErrModel
				}
				for _, target := range targets {
					for k := range 3 {
						v := target.at(i, k)
						if math.Abs(v) > 20 {
							return nil, ErrModel
						}
						m.Morphs = pixelFloat(m.Morphs, v)
					}
				}
			}
			for i := range idx.count {
				v := idx.at(i, 0)
				if v < 0 || v >= float64(pos.count) || v != math.Trunc(v) {
					return nil, ErrModel
				}
				m.Indices = binary.LittleEndian.AppendUint32(m.Indices, uint32(m.VertexCount+int(v)))
			}
			m.VertexCount += pos.count
			m.Triangles += idx.count / 3
		}
		m.Parts = append(m.Parts, PixelPart{mesh.Name, first, m.VertexCount - first})
	}
	if m.Triangles < 1 || len(m.Parts) < 1 {
		return nil, ErrModel
	}
	return m, nil
}

func PixelPoseIndex(seconds float64) int {
	if !finite(seconds) || seconds < 0 {
		return 0
	}
	return min(8, int(math.Floor((math.Mod(seconds, 2.7)+1e-7)/.3)))
}

// Pose reuses dst. All joints and morph targets select the same discrete pose.
func (m *PixelModel) Pose(dst []byte, seconds, yaw, pitch float64) []byte {
	if !finite(yaw) || !finite(pitch) {
		yaw, pitch = 0, 0
	}
	unit, origin := []float64{1, 1, 1}, []float64{0, 0, 0}
	view := multiply(transform([]float64{0, 0, -8}, []float64{math.Sin(-pitch / 2), 0, 0, math.Cos(pitch / 2)}, unit), multiply(transform(origin, []float64{0, math.Sin(-yaw / 2), 0, math.Cos(yaw / 2)}, unit), transform([]float64{0, -1.6, .25}, []float64{0, 0, 0, 1}, unit)))
	projection := matrix{1 / 2.09, 0, 0, 0, 0, 1 / 2.09, 0, 0, 0, 0, -1 / 99.99, 0, 0, 0, -.01 / 99.99, 1}
	view = multiply(projection, view)
	dst = dst[:0]
	for _, v := range view {
		dst = pixelFloat(dst, v)
	}
	for _, joint := range m.poses[PixelPoseIndex(seconds)] {
		for _, v := range joint {
			dst = pixelFloat(dst, v)
		}
	}
	return dst
}

// ReleaseGeometry drops the CPU upload copies after successful GPU ownership.
// Small immutable joint poses remain for allocation-free playback.
func (m *PixelModel) ReleaseGeometry() { m.Vertices = nil; m.Morphs = nil; m.Indices = nil }
