package physics

import (
	"math"

	"gonum.org/v1/gonum/spatial/r3"
)

// 本家の Mth の sin と cos は 65536 個の表を引く（Math.sin とわずかに違う）。向きから動く方向を求めるところで使う。

var sinTable = func() []float32 {
	t := make([]float32, 65536)
	for i := range t {
		t[i] = float32(math.Sin(float64(i) * math.Pi * 2 / 65536))
	}
	return t
}()

// mthSin は本家 1.21.4 の Mth.sin(float)（float のまま 10430.378F を掛けて表を引く）。
func mthSin(v float32) float32 {
	return sinTable[int32(v*10430.378)&65535]
}

// mthCos は本家 1.21.4 の Mth.cos(float)。
func mthCos(v float32) float32 {
	// float に丸めてから足す（Go が掛け算と足し算をまとめて計算しないように）
	return sinTable[int32(float32(v*10430.378)+16384)&65535]
}

// mthEqual は本家の Mth.equal(double, double)（差が 1.0E-5F 未満）。
func mthEqual(a, b float64) bool {
	return math.Abs(b-a) < float64(float32(1e-5))
}

// normalize は本家の Vec3.normalize（長さが 1.0E-5F 未満なら 0）。
func normalize(v r3.Vec) r3.Vec {
	d := math.Sqrt(v.X*v.X + v.Y*v.Y + v.Z*v.Z)
	if d < float64(float32(1e-5)) {
		return r3.Vec{}
	}
	return r3.Vec{X: v.X / d, Y: v.Y / d, Z: v.Z / d}
}
