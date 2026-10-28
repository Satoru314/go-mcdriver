package physics

import (
	"math"

	"gonum.org/v1/gonum/spatial/r3"
)

// 姿勢、しゃがんだときに端から落ちない動き、中に入ったブロックの効果。
// 出典は本家 1.21.4 の Player.updatePlayerPose / canPlayerFitWithinBlocksAndEntitiesWhen / maybeBackOffFromEdge /
// isAboveGround / canFallAtLeast、Entity.applyEffectsFromBlocks / checkInsideBlocks、
// WebBlock / SweetBerryBushBlock / PowderSnowBlock / BubbleColumnBlock / SlimeBlock の entityInside と stepOn。

// canFit は姿勢 q の当たり判定（1e-7 縮めた箱）がブロックとぶつからないか（本家の canPlayerFitWithinBlocksAndEntitiesWhen）。
func (p *Player) canFit(w World, q Pose) bool {
	return len(blockBoxes(w, deflate(PoseBoxAt(p.Pos, q), 1e-7), p.ctx())) == 0
}

// updatePose は本家の Player.updatePlayerPose。泳いでいれば泳ぎ、しゃがむキーを押していればしゃがみ、それ以外は立つ。
// その姿勢が入らなければ、しゃがみ、それも入らなければ這う（泳ぎの姿勢）。泳ぎの姿勢も入らなければ変えない。
func (p *Player) updatePose(w World) {
	if !p.canFit(w, PoseSwimming) {
		return
	}
	want := PoseStanding
	switch {
	case p.Swimming:
		want = PoseSwimming
	case p.Sneaking:
		want = PoseCrouching
	}
	switch {
	case p.canFit(w, want):
		p.Pose = want
	case p.canFit(w, PoseCrouching):
		p.Pose = PoseCrouching
	default:
		p.Pose = PoseSwimming
	}
}

// lookY は見ている向きの y（本家の getLookAngle().y = -sin(pitch)）。
func (p *Player) lookY() float64 {
	return float64(-mthSin(p.Pitch * (math.Pi / 180)))
}

// maybeBackOffFromEdge は本家の Player.maybeBackOffFromEdge。しゃがむキーを押していて地面の上にいれば、
// 動いた先で StepHeight より深く落ちるなら、その向きの動きを 0.05 ずつ減らす（端から落ちない）。
func (p *Player) maybeBackOffFromEdge(w World, mv r3.Vec) r3.Vec {
	f := StepHeight
	if mv.Y > 0 || !p.Sneaking || !p.isAboveGround(w, f) {
		return mv
	}
	const step = 0.05
	d, d1 := mv.X, mv.Z
	d3, d4 := sign(d)*step, sign(d1)*step
	for d != 0 && p.canFallAtLeast(w, d, 0, f) {
		if math.Abs(d) <= step {
			d = 0
			break
		}
		d -= d3
	}
	for d1 != 0 && p.canFallAtLeast(w, 0, d1, f) {
		if math.Abs(d1) <= step {
			d1 = 0
			break
		}
		d1 -= d4
	}
	for d != 0 && d1 != 0 && p.canFallAtLeast(w, d, d1, f) {
		if math.Abs(d) <= step {
			d = 0
		} else {
			d -= d3
		}
		if math.Abs(d1) <= step {
			d1 = 0
		} else {
			d1 -= d4
		}
	}
	return r3.Vec{X: d, Y: mv.Y, Z: d1}
}

// isAboveGround は本家の Player.isAboveGround。本家のクライアントは自分の落下距離（fallDistance）を数えないので
// （checkFallDamage はサーバーだけ）、落下距離は 0 として扱う。
func (p *Player) isAboveGround(w World, maxUpStep float32) bool {
	return p.OnGround || !p.canFallAtLeast(w, 0, 0, maxUpStep)
}

// canFallAtLeast は本家の Player.canFallAtLeast（足の下 distance の板が、(x, z) ずらした先で何ともぶつからない）。
func (p *Player) canFallAtLeast(w World, x, z float64, distance float32) bool {
	b := p.Box()
	q := r3.Box{
		Min: r3.Vec{X: b.Min.X + x, Y: b.Min.Y - float64(distance) - float64(float32(1e-5)), Z: b.Min.Z + z},
		Max: r3.Vec{X: b.Max.X + x, Y: b.Min.Y, Z: b.Max.Z + z},
	}
	return len(blockBoxes(w, q, p.ctx())) == 0
}

func sign(v float64) float64 {
	switch {
	case v > 0:
		return 1
	case v < 0:
		return -1
	}
	return v
}

// applyEffectsFromBlocks は本家の Entity.applyEffectsFromBlocks（移動のあと）。
// 地面に立っていれば足元のブロックの stepOn（スライムの上は遅い）、当たり判定と重なるブロックの entityInside を呼ぶ。
//
// 本家の checkInsideBlocks は、1 ティックに 1 ブロック近く以上動いたときは通り道のブロックも調べる（boxTraverseBlocks）。
// ここでは、移動のあとの当たり判定と重なるブロックだけを調べる。
func (p *Player) applyEffectsFromBlocks(w World) {
	if p.OnGround {
		on := p.infoAt(w, p.onPos(w, 0.2))
		if on.slime {
			// SlimeBlock.stepOn。しゃがんでいれば遅くならない（isSteppingCarefully）
			if d := math.Abs(p.Vel.Y); d < 0.1 && !p.Sneaking {
				e := 0.4 + d*0.2
				p.Vel.X *= e
				p.Vel.Z *= e
			}
		}
	}
	b := deflate(p.Box(), float64(float32(1e-5)))
	// BlockPos.betweenClosed と同じ順（x が一番内側、次に y、外が z）
	for z := floor(b.Min.Z); z <= floor(b.Max.Z); z++ {
		for y := floor(b.Min.Y); y <= floor(b.Max.Y); y++ {
			for x := floor(b.Min.X); x <= floor(b.Max.X); x++ {
				in := infoAtXYZ(w, x, y, z)
				if in.air {
					continue
				}
				switch in.kind {
				case kindWeb:
					// WebBlock.entityInside（機織りの効果は扱わない）
					p.makeStuck(r3.Vec{X: 0.25, Y: float64(float32(0.05)), Z: 0.25})
				case kindBerryBush:
					p.makeStuck(r3.Vec{X: float64(float32(0.8)), Y: 0.75, Z: float64(float32(0.8))})
				case kindPowderSnow:
					// 足元のブロックが粉雪のときだけ
					if p.infoAt(w, p.blockPosition()).kind == kindPowderSnow {
						p.makeStuck(r3.Vec{X: float64(float32(0.9)), Y: 1.5, Z: float64(float32(0.9))})
					}
				case kindBubbleColumn:
					if infoAtXYZ(w, x, y+1, z).air {
						p.onAboveBubbleColumn(in.drag)
					} else {
						p.onInsideBubbleColumn(in.drag)
					}
				}
			}
		}
	}
}

// makeStuck は本家の Entity.makeStuckInBlock（次の移動に mult を掛け、速さを 0 にする）。
func (p *Player) makeStuck(mult r3.Vec) { p.stuck = mult }

// onAboveBubbleColumn は本家の Entity.onAboveBubbleCol（泡の柱の一番上）。
func (p *Player) onAboveBubbleColumn(drag bool) {
	if drag {
		p.Vel.Y = math.Max(-0.9, p.Vel.Y-0.03)
	} else {
		p.Vel.Y = math.Min(1.8, p.Vel.Y+0.1)
	}
}

// onInsideBubbleColumn は本家の Entity.onInsideBubbleColumn。
func (p *Player) onInsideBubbleColumn(drag bool) {
	if drag {
		p.Vel.Y = math.Max(-0.3, p.Vel.Y-0.03)
	} else {
		p.Vel.Y = math.Min(0.7, p.Vel.Y+0.06)
	}
}
