package mcdriver

import (
	"math"
	"testing"

	"gonum.org/v1/gonum/spatial/r3"
)

// LookAt の向き: 東 -90、南 0、西 90、北 ±180。真下は pitch 90。
func TestLookAt(t *testing.T) {
	cl := &Client{}
	cl.initFeatures()
	cl.x, cl.y, cl.z = 0.5, 10, 0.5
	eye := 10 + 1.62
	for _, tc := range []struct {
		p          r3.Vec
		yaw, pitch float64
	}{
		{r3.Vec{X: 10.5, Y: eye, Z: 0.5}, -90, 0},
		{r3.Vec{X: 0.5, Y: eye, Z: 10.5}, 0, 0},
		{r3.Vec{X: -9.5, Y: eye, Z: 0.5}, 90, 0},
		{r3.Vec{X: 0.5, Y: eye, Z: -9.5}, -180, 0}, // 北
		{r3.Vec{X: 10.5, Y: eye + 10, Z: 0.5}, -90, -45},
		{r3.Vec{X: 0.5, Y: 0, Z: 0.5}, -90, 90}, // 真下は yaw を変えない（前の -90 のまま）
	} {
		cl.LookAt(tc.p)
		y := math.Mod(float64(cl.yaw)-tc.yaw, 360)
		if math.Abs(y) > 1e-3 && math.Abs(math.Abs(y)-360) > 1e-3 || math.Abs(float64(cl.pitch)-tc.pitch) > 1e-3 {
			t.Errorf("LookAt(%v) = yaw %v pitch %v、期待 %v %v", tc.p, cl.yaw, cl.pitch, tc.yaw, tc.pitch)
		}
		// 送る yaw は -180 以上 180 未満（atan2 - 90 は -270〜90 になるので直す）
		if cl.yaw < -180 || cl.yaw >= 180 {
			t.Errorf("LookAt(%v) の yaw %v が -180 以上 180 未満でない", tc.p, cl.yaw)
		}
	}
	// 北西（atan2 - 90 では -225）は 135
	cl.LookAt(r3.Vec{X: -9.5, Y: eye, Z: -9.5})
	if math.Abs(float64(cl.yaw)-135) > 1e-3 {
		t.Errorf("北西の yaw %v、期待 135", cl.yaw)
	}
	cl.Look(10, 120)
	if cl.pitch != 90 {
		t.Errorf("pitch を -90〜90 に収めない: %v", cl.pitch)
	}
	// Look も yaw を -180 以上 180 未満に直す
	for _, tc := range [][2]float32{{270, -90}, {180, -180}, {-180, -180}, {-270, 90}, {720, 0}, {-90, -90}, {179.5, 179.5}} {
		cl.Look(tc[0], 0)
		if cl.yaw != tc[1] {
			t.Errorf("Look(%v) の yaw %v、期待 %v", tc[0], cl.yaw, tc[1])
		}
	}
}

func TestInputFlags(t *testing.T) {
	if k := inputFlags(Controls{Forward: true, Jump: true, Sprint: true}); k != 0x51 {
		t.Errorf("inputFlags = %#x", k)
	}
}
