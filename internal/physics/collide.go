package physics

import (
	"math"
	"sort"

	"gonum.org/v1/gonum/spatial/r3"
)

// 当たり判定の計算。本家の Entity.collide / collideBoundingBox / collideWithShapes と
// VoxelShape.collide（誤差 1e-7）をなぞる。本家はブロックの形を VoxelShape にまとめるが、
// ここでは箱（r3.Box）の一覧のまま計算する（結果は同じになる）。

// collisionEpsilon は本家の VoxelShape の当たり判定で使う誤差（1.0E-7）。
const collisionEpsilon = 1e-7

func moveBox(b r3.Box, dx, dy, dz float64) r3.Box {
	d := r3.Vec{X: dx, Y: dy, Z: dz}
	return r3.Box{Min: r3.Add(b.Min, d), Max: r3.Add(b.Max, d)}
}

// expandTowards は本家の AABB.expandTowards（動く向きに箱を伸ばす）。
func expandTowards(b r3.Box, v r3.Vec) r3.Box {
	if v.X < 0 {
		b.Min.X += v.X
	} else {
		b.Max.X += v.X
	}
	if v.Y < 0 {
		b.Min.Y += v.Y
	} else {
		b.Max.Y += v.Y
	}
	if v.Z < 0 {
		b.Min.Z += v.Z
	} else {
		b.Max.Z += v.Z
	}
	return b
}

func deflate(b r3.Box, d float64) r3.Box {
	return r3.Box{Min: r3.Add(b.Min, r3.Vec{X: d, Y: d, Z: d}), Max: r3.Sub(b.Max, r3.Vec{X: d, Y: d, Z: d})}
}

// intersects は本家の AABB.intersects（面が接しているだけなら重ならない）。
func intersects(a, b r3.Box) bool {
	return a.Min.X < b.Max.X && a.Max.X > b.Min.X &&
		a.Min.Y < b.Max.Y && a.Max.Y > b.Min.Y &&
		a.Min.Z < b.Max.Z && a.Max.Z > b.Min.Z
}

func axisOf(v r3.Vec, axis int) float64 {
	switch axis {
	case 0:
		return v.X
	case 1:
		return v.Y
	}
	return v.Z
}

// collideAxis は箱 box を軸 axis に沿って d だけ動かすとき、shapes にぶつからずに動ける量を返す
// （本家の Shapes.collide）。
func collideAxis(axis int, box r3.Box, shapes []r3.Box, d float64) float64 {
	if math.Abs(d) < collisionEpsilon {
		return 0
	}
	o1, o2 := (axis+1)%3, (axis+2)%3
	for _, s := range shapes {
		// ほかの 2 つの軸で重なっているものだけ（誤差 1e-7 の内側）
		if !(axisOf(s.Max, o1) > axisOf(box.Min, o1)+collisionEpsilon && axisOf(s.Min, o1) < axisOf(box.Max, o1)-collisionEpsilon) {
			continue
		}
		if !(axisOf(s.Max, o2) > axisOf(box.Min, o2)+collisionEpsilon && axisOf(s.Min, o2) < axisOf(box.Max, o2)-collisionEpsilon) {
			continue
		}
		if d > 0 {
			if axisOf(s.Min, axis) >= axisOf(box.Max, axis)-collisionEpsilon {
				d = min(d, axisOf(s.Min, axis)-axisOf(box.Max, axis))
			}
		} else {
			if axisOf(s.Max, axis) <= axisOf(box.Min, axis)+collisionEpsilon {
				d = max(d, axisOf(s.Max, axis)-axisOf(box.Min, axis))
			}
		}
		if math.Abs(d) < collisionEpsilon {
			return 0
		}
	}
	return d
}

// collideWithShapes は本家の Entity.collideWithShapes。先に y、次に x と z のうち動きの小さいほう、最後に大きいほう。
func collideWithShapes(mv r3.Vec, box r3.Box, shapes []r3.Box) r3.Vec {
	if len(shapes) == 0 {
		return mv
	}
	dx, dy, dz := mv.X, mv.Y, mv.Z
	if dy != 0 {
		dy = collideAxis(1, box, shapes, dy)
		if dy != 0 {
			box = moveBox(box, 0, dy, 0)
		}
	}
	zFirst := math.Abs(dx) < math.Abs(dz)
	if zFirst && dz != 0 {
		dz = collideAxis(2, box, shapes, dz)
		if dz != 0 {
			box = moveBox(box, 0, 0, dz)
		}
	}
	if dx != 0 {
		dx = collideAxis(0, box, shapes, dx)
		if !zFirst && dx != 0 {
			box = moveBox(box, dx, 0, 0)
		}
	}
	if !zFirst && dz != 0 {
		dz = collideAxis(2, box, shapes, dz)
	}
	return r3.Vec{X: dx, Y: dy, Z: dz}
}

// blockBoxes は q と重なる（面で接するだけのものは除く）ブロックの当たり判定の箱を、ワールドの座標で返す。
// 本家の BlockCollisions と同じく、フェンスのように 1 ブロックをはみ出す形も拾うため、周りを 1 ブロック広く見る。
// 読み込まれていないブロックは当たり判定なしとする（本家も同じ）。
func blockBoxes(w World, q r3.Box) []r3.Box {
	x0, x1 := floor(q.Min.X-collisionEpsilon)-1, floor(q.Max.X+collisionEpsilon)+1
	y0, y1 := floor(q.Min.Y-collisionEpsilon)-1, floor(q.Max.Y+collisionEpsilon)+1
	z0, z1 := floor(q.Min.Z-collisionEpsilon)-1, floor(q.Max.Z+collisionEpsilon)+1
	var out []r3.Box
	for y := y0; y <= y1; y++ {
		for z := z0; z <= z1; z++ {
			for x := x0; x <= x1; x++ {
				s, ok := w.Block(x, y, z)
				if !ok || s.Block == nil {
					continue
				}
				for _, b := range s.CollisionBoxes() {
					wb := moveBox(b, float64(x), float64(y), float64(z))
					if intersects(wb, q) {
						out = append(out, wb)
					}
				}
			}
		}
	}
	return out
}

// stepCandidates は段差を上るときに試す高さを、低い順に返す（本家の collectCandidateStepUpHeights）。
// 周りの箱の上下の面の高さのうち、0 以上 maxStep 以下で、すでに動けた y（skip）と違うもの。
func stepCandidates(box r3.Box, shapes []r3.Box, maxStep, skip float32) []float32 {
	set := map[float32]bool{}
	for _, s := range shapes {
		for _, y := range [2]float64{s.Min.Y, s.Max.Y} {
			f := float32(y - box.Min.Y)
			if f < 0 || f == skip || f > maxStep {
				continue
			}
			set[f] = true
		}
	}
	out := make([]float32, 0, len(set))
	for f := range set {
		out = append(out, f)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

func horizontalDistSqr(v r3.Vec) float64 { return v.X*v.X + v.Z*v.Z }
func lengthSqr(v r3.Vec) float64         { return v.X*v.X + v.Y*v.Y + v.Z*v.Z }

func floor(v float64) int { return int(math.Floor(v)) }
func ceil(v float64) int  { return int(math.Ceil(v)) }
