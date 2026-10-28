package physics

import (
	"math"

	"gonum.org/v1/gonum/spatial/r3"
)

// 水と溶岩: 触れているか、深さ、流れに押される力、目が水の中か、泳ぐか。
// 出典は本家 1.21.4 の Entity.updateInWaterStateAndDoFluidPushing / updateFluidHeightAndDoFluidPushing /
// updateFluidOnEyes / updateSwimming と、FlowingFluid.getFlow / getHeight / isSolidFace / affectsFlow。

// updateFluids は本家の Entity.updateInWaterStateAndDoFluidPushing（先に水、次に溶岩）。
func (p *Player) updateFluids(w World) {
	p.InWater, p.WaterHeight = p.fluidPush(w, water, WaterFlowScale)
	scale := LavaFlowScale
	if p.UltraWarm {
		scale = LavaFlowScaleUltraWarm
	}
	_, p.LavaHeight = p.fluidPush(w, lava, scale)
	p.InLava = p.LavaHeight > 0
}

// fluidPush は本家の Entity.updateFluidHeightAndDoFluidPushing。当たり判定（0.001 縮めた箱）と重なる kind の液体を調べ、
// 触れているかと、足元からの一番深い深さを返す。流れがあれば、その平均に scale を掛けて速さに足す。
//
// 本家（クライアント）は、流れを足すときに深さが 0.4 未満なら流れに深さを掛けるが、その深さはそれまでに見た液体の
// 一番深い値（ブロックごとの深さではない）。ここもそれに合わせる（Paper のサーバーはブロックごとの深さを使う）。
func (p *Player) fluidPush(w World, kind fluidKind, scale float64) (bool, float64) {
	b := deflate(p.Box(), 0.001)
	x0, x1 := floor(b.Min.X), ceil(b.Max.X)
	y0, y1 := floor(b.Min.Y), ceil(b.Max.Y)
	z0, z1 := floor(b.Min.Z), ceil(b.Max.Z)
	var push r3.Vec
	n := 0
	h := 0.0
	in := false
	for x := x0; x < x1; x++ {
		for y := y0; y < y1; y++ {
			for z := z0; z < z1; z++ {
				fi := p.infoAt(w, BlockPos{x, y, z})
				if fi.fluid != kind {
					continue
				}
				top := float64(float32(y) + fluidHeight(w, x, y, z, fi))
				if top < b.Min.Y {
					continue
				}
				in = true
				h = max(top-b.Min.Y, h)
				f := flow(w, x, y, z, fi)
				if h < 0.4 {
					f = r3.Scale(h, f)
				}
				push = r3.Add(push, f)
				n++
			}
		}
	}
	if length(push) > 0 {
		if n > 0 {
			push = r3.Scale(1/float64(n), push)
		}
		// プレイヤーは向きだけにそろえない（normalize しない）
		push = r3.Scale(scale, push)
		if math.Abs(p.Vel.X) < 0.003 && math.Abs(p.Vel.Z) < 0.003 && length(push) < 0.0045000000000000005 {
			push = r3.Scale(0.0045000000000000005, normalize(push))
		}
		p.Vel = r3.Add(p.Vel, push)
	}
	return in, h
}

// length は本家の Vec3.length（Math.sqrt(x*x + y*y + z*z)）。
func length(v r3.Vec) float64 { return math.Sqrt(v.X*v.X + v.Y*v.Y + v.Z*v.Z) }

// fluidHeight は本家の FluidState.getHeight（上に同じ液体があれば 1、無ければ量/9）。
func fluidHeight(w World, x, y, z int, fi *stateInfo) float32 {
	if infoAtXYZ(w, x, y+1, z).fluid == fi.fluid {
		return 1
	}
	return fi.ownHeight()
}

func infoAtXYZ(w World, x, y, z int) *stateInfo {
	s, ok := w.Block(x, y, z)
	if !ok {
		return &airInfo
	}
	return infoOf(s)
}

// flow は本家の FlowingFluid.getFlow（(x, y, z) の液体 fi が流れる向き。長さ 1 か 0）。
// 横の 4 方向（北、東、南、西の順）の液体の高さとの差を足し合わせる。隣が液体でなく、動きを止めないブロック（空気など）なら、
// その下の同じ液体との差を見る（滝に向かって流れる）。落ちてくる流れは、横に壁があれば強く下に向く。
func flow(w World, x, y, z int, fi *stateInfo) r3.Vec {
	var dx, dz float64
	own := fi.ownHeight()
	for _, d := range horizontals {
		nx, nz := x+d.dx, z+d.dz
		n := infoAtXYZ(w, nx, y, nz)
		if !affectsFlow(n, fi.fluid) {
			continue
		}
		h := n.ownHeight()
		var f float32
		if h == 0 {
			if !n.blocksMotion {
				b := infoAtXYZ(w, nx, y-1, nz)
				if affectsFlow(b, fi.fluid) {
					h = b.ownHeight()
					if h > 0 {
						f = own - (h - 0.8888889)
					}
				}
			}
		} else if h > 0 {
			f = own - h
		}
		if f != 0 {
			dx += float64(float32(d.dx) * f)
			dz += float64(float32(d.dz) * f)
		}
	}
	v := r3.Vec{X: dx, Z: dz}
	if fi.falling {
		for i, d := range horizontals {
			nx, nz := x+d.dx, z+d.dz
			if isSolidFace(w, nx, y, nz, i, fi.fluid) || isSolidFace(w, nx, y+1, nz, i, fi.fluid) {
				v = normalize(v)
				v.Y += -6
				break
			}
		}
	}
	return normalize(v)
}

// affectsFlow は本家の FlowingFluid.affectsFlow（液体が無いか、同じ液体）。
func affectsFlow(n *stateInfo, kind fluidKind) bool {
	return n.fluid == noFluid || n.fluid == kind
}

// isSolidFace は本家の FlowingFluid.isSolidFace（(x, y, z) のブロックの、向き dir の面が埋まっているか）。
// 本家は隣のブロックの、流れから見て向こう側の面を調べる（そのままなぞる）。同じ液体と氷は壁にならない。
func isSolidFace(w World, x, y, z, dir int, kind fluidKind) bool {
	n := infoAtXYZ(w, x, y, z)
	if n.fluid == kind {
		return false
	}
	if n.ice {
		return false
	}
	return n.sturdy[dir]
}

// updateFluidOnEyes は本家の Entity.updateFluidOnEyes（目の高さのブロックの液体の面が目より上か）。
// 前の値を wasEyeInWater に残す（UnderWater に使う）。
func (p *Player) updateFluidOnEyes(w World) {
	p.wasEyeInWater = p.EyeInWater
	eyeY := p.Pos.Y + float64(p.EyeHeight())
	x, y, z := floor(p.Pos.X), floor(eyeY), floor(p.Pos.Z)
	fi := infoAtXYZ(w, x, y, z)
	p.EyeInWater = false
	if fi.fluid == water {
		p.EyeInWater = float64(float32(y)+fluidHeight(w, x, y, z, fi)) > eyeY
	}
}

// updateSwimming は本家の Entity.updateSwimming。泳いでいれば、走っていて水に触れているあいだ泳ぎ続ける。
// 泳ぎ始めるのは、走っていて、目が水の中（前のティック）で、足元のブロックが水のとき。
func (p *Player) updateSwimming(w World) {
	if p.Swimming {
		p.Swimming = p.Sprinting && p.InWater
	} else {
		p.Swimming = p.Sprinting && p.UnderWater() && p.infoAt(w, p.blockPosition()).fluid == water
	}
}
