package mcdriver

import (
	"context"
	"errors"
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

// NaN や無限大の向き・位置は送らずにエラーにし、前の向きを保つ。Place の Yaw と Cursor も同じ。
func TestNotFinite(t *testing.T) {
	cl := &Client{}
	cl.initFeatures()
	cl.x, cl.y, cl.z = 0.5, 10, 0.5
	if err := cl.Look(30, 20); err != nil {
		t.Fatal(err)
	}
	nan, inf := float32(math.NaN()), float32(math.Inf(1))
	for _, yp := range [][2]float32{{nan, 0}, {0, nan}, {inf, 0}, {0, -inf}, {-inf, inf}} {
		if err := cl.Look(yp[0], yp[1]); !errors.Is(err, ErrNotFinite) {
			t.Errorf("Look(%v, %v) = %v、期待 ErrNotFinite", yp[0], yp[1], err)
		}
	}
	n64, i64 := math.NaN(), math.Inf(-1)
	for _, p := range []r3.Vec{{X: n64, Y: 0, Z: 0}, {X: 0, Y: n64, Z: 0}, {X: 0, Y: 0, Z: i64}} {
		if err := cl.LookAt(p); !errors.Is(err, ErrNotFinite) {
			t.Errorf("LookAt(%v) = %v、期待 ErrNotFinite", p, err)
		}
	}
	if cl.yaw != 30 || cl.pitch != 20 {
		t.Errorf("NaN などを渡したあとの向き %v %v、期待は前の 30 20 のまま", cl.yaw, cl.pitch)
	}
	if err := cl.LookAt(r3.Vec{X: 10.5, Y: 11.62, Z: 0.5}); err != nil || cl.yaw != -90 {
		t.Errorf("ふつうの LookAt = %v（yaw %v）", err, cl.yaw)
	}
	for _, y := range []float64{n64, i64, 1e300} { // 1e300 は float32 にすると無限大
		if _, err := cl.Place(context.Background(), BlockPos{}, Up, PlaceOpts{Yaw: &y}); !errors.Is(err, ErrNotFinite) {
			t.Errorf("Place の Yaw %v = %v、期待 ErrNotFinite", y, err)
		}
	}
	for _, c := range []r3.Vec{{X: n64, Y: 0.5, Z: 0.5}, {X: 0.5, Y: n64, Z: 0.5}, {X: 0.5, Y: 0.5, Z: i64}} {
		if _, err := cl.Place(context.Background(), BlockPos{}, Up, PlaceOpts{Cursor: &c}); !errors.Is(err, ErrNotFinite) {
			t.Errorf("Place の Cursor %v = %v、期待 ErrNotFinite", c, err)
		}
	}
}

func TestInputFlags(t *testing.T) {
	if k := inputFlags(Controls{Forward: true, Jump: true, Sprint: true}); k != 0x51 {
		t.Errorf("inputFlags = %#x", k)
	}
}
