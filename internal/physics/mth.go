package physics

import "math"

// 本家の Mth の sin と cos は 65536 個の表を引く（Math.sin とわずかに違う）。向きから動く方向を求めるところで使う。

var sinTable = func() []float32 {
	t := make([]float32, 65536)
	for i := range t {
		t[i] = float32(math.Sin(float64(i) * math.Pi * 2 / 65536))
	}
	return t
}()

// mthSin は本家の Mth.sin(double)。
func mthSin(v float64) float32 {
	return sinTable[int(int64(v*10430.378350470453)&65535)]
}

// mthCos は本家の Mth.cos(double)。
func mthCos(v float64) float32 {
	return sinTable[int(int64(v*10430.378350470453+16384)&65535)]
}

// mthEqual は本家の Mth.equal(double, double)（差が 1.0E-5F 未満）。
func mthEqual(a, b float64) bool {
	return math.Abs(b-a) < float64(float32(1e-5))
}
