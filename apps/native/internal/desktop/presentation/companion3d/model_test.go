package companion3d

import (
	"encoding/binary"
	"math"
	"testing"
)

func TestPilotMeshesHaveDepthArticulationAndFiniteLoopPoses(t *testing.T) {
	for _, name := range []string{"crush", "rope", "work"} {
		t.Run(name, func(t *testing.T) {
			m, err := Load(name)
			if err != nil {
				t.Fatal(err)
			}
			if m.Name != name || m.Joints() < 20 || len(m.Vertices) < 1000*40 {
				t.Fatal("missing model geometry")
			}
			if len(m.Vertices) > 4<<20 {
				t.Fatal("pilot vertex budget")
			}
			front := m.Pose(0, 0, 0)
			turned := m.Pose(.5, math.Pi/2, .1)
			if string(front) == string(turned) {
				t.Fatal("pose did not change")
			}
			for _, seconds := range []float64{0, .1, .5, m.Duration - 1e-5, m.Duration, 1e6} {
				pose := m.Pose(seconds, .5, .1)
				if len(pose) != (m.Joints()+1)*64 {
					t.Fatal("matrix count")
				}
				for i := 0; i < len(pose); i += 4 {
					if !finite(float64(math.Float32frombits(binary.LittleEndian.Uint32(pose[i:])))) {
						t.Fatal("nonfinite GPU input")
					}
				}
			}
			if string(front) != string(m.Pose(m.Duration, 0, 0)) {
				t.Fatal("loop did not return to start")
			}
		})
	}
}
func TestMalformedPilotRejected(t *testing.T) {
	if Reviewable() {
		t.Fatal("user-rejected fixtures must not be offered as character previews")
	}
	data, err := assets.ReadFile("assets/crush.glb")
	if err != nil {
		t.Fatal(err)
	}
	for _, cut := range []int{0, 12, 19, len(data) - 1} {
		if _, err := Decode(data[:cut]); err == nil {
			t.Fatal("truncated model accepted")
		}
	}
	for _, offset := range []int{0, 4, 8, 12, 16} {
		copyData := append([]byte(nil), data...)
		binary.LittleEndian.PutUint32(copyData[offset:], 0xffffffff)
		if _, err := Decode(copyData); err == nil {
			t.Fatal("invalid header accepted")
		}
	}
	if _, err := Load("../../secret"); err == nil {
		t.Fatal("nonallowlisted model accepted")
	}
}

func BenchmarkPilotPose(b *testing.B) {
	m, err := Load("work")
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	for b.Loop() {
		m.Pose(.7, .1, .1)
	}
}
