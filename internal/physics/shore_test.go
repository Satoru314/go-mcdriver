package physics

import (
	"math"
	"testing"
)

// 岸に上がるときの水の物理を、本家 1.21.4 のコードの写し（shoreRef）とティックごとに見比べるテスト。
//
// 地形は deepPool と同じ: 水の源が y=1〜4（一番上の水のブロックの上面は y=5、水面は 4 + 8/9 = 4.889）、
// x=6 から東が岸の石で、上面は top（5 = 水のブロックと同じ高さ、6 = 1 段高い）。
// プレイヤーは岸の壁に東の面をつけ（x = 6 - 0.3）、東を向いて前とジャンプを押し続ける。
//
// 本家で効くところ（Mojang のマッピングでの名前）:
//   - Entity.baseTick → updateFluidHeightAndDoFluidPushing: 0.001 縮めた箱と重なる水のブロックの
//     (float)y + getHeight（上が水なら 1、無ければ 8/9F）が箱の下以上なら水の中。深さはその差の最大
//   - LivingEntity.aiStep のジャンプ: 水の中（isInWater && 深さ > 0）で地面にいなければ jumpInLiquid（y に +0.04F）。
//     水の外で地面にいなければ何もしない
//   - LivingEntity.travelInFluid: moveRelative(0.02F) → move → (x, y, z) に (0.8F, 0.8F, 0.8F) を掛ける →
//     getFluidFallingAdjustedMovement（y から 0.08/16 を引く）→ horizontalCollision && isFree(dx, dy + 0.6F - getY() + 動く前の y, dz)
//     なら y を 0.3F にする
//   - Entity.isFree = noCollision && !containsAnyLiquid（箱と重なるブロックのどれかに液体があれば自由でない。液体の高さは見ない）
//   - Entity.move: 横にぶつかった軸の速さは 0 にする。段差の上り（maxUpStep 0.6）は onGround か、下に動いて着地したときだけ
//   - LivingEntity.travelInAir: y は (vy - 0.08) * 0.98F、横は 0.91F

// shoreState は 1 ティックの終わりの状態（比べる値）。
type shoreState struct {
	Y, VY, X       float64
	InWater        bool    // そのティックの初めの isInWater（baseTick で決まる）
	FluidHeight    float64 // そのティックの初めの水の深さ
	HorizontalColl bool
	ClimbOut       bool // そのティックの終わりに 0.3 をもらった
	OnGround       bool
}

// shoreRef は本家の式をこの地形だけに絞って写したもの（physics の部品を使わない）。
type shoreRef struct {
	top          int // 岸の上面
	x, y, vx, vy float64
	onGround     bool
}

const (
	refHalf     = float64(float32(0.6) / 2) // EntityDimensions.makeBoundingBox の幅の半分（0.3F）
	refHeight   = float64(float32(1.8))
	refWaterTop = 4 // 一番上の水のブロック
)

// fluid は Entity.updateFluidHeightAndDoFluidPushing（流れは無い。全部が源なので getFlow は 0）。
func (r *shoreRef) fluid() (bool, float64) {
	minY, maxY := r.y+0.001, r.y+refHeight-0.001
	in, h := false, 0.0
	// 箱の x は 5 の列だけ（壁は x=6 から）。水は y=1〜4
	if r.x+refHalf-0.001 > 6 {
		return false, 0 // 岸の上（水の列から出た）
	}
	for k := int(math.Floor(minY)); k < int(math.Ceil(maxY)); k++ {
		if k < 1 || k > refWaterTop {
			continue
		}
		hk := float32(1)
		if k == refWaterTop {
			hk = float32(8) / 9 // FlowingFluid.getOwnHeight = amount / 9.0F（源は 8）
		}
		t := float64(float32(k) + hk)
		if t >= minY {
			in = true
			h = math.Max(t-minY, h)
		}
	}
	return in, h
}

// wallAt は箱の下が y のとき、岸の壁（x=6〜、y=1〜top-1）と y で重なるか（VoxelShape.collideX の 1.0E-7 の幅つき）。
func (r *shoreRef) wallAt(y float64) bool { return y+1e-7 < float64(r.top) }

// move は Entity.move（この地形だけ）。y を先に、次に x（|dx| > |dz| = 0）。
func (r *shoreRef) move(dx, dy float64) (hc bool) {
	ry := dy
	// 岸の上面に着地する（箱が x=6 より東に掛かっているとき）
	if dy < 0 && r.x+refHalf > 6+1e-7 && r.y >= float64(r.top) && r.y+dy < float64(r.top) {
		ry = float64(r.top) - r.y
	}
	rx := dx
	if dx > 0 && r.x+refHalf <= 6+1e-7 && r.wallAt(r.y+ry) {
		d := 6 - (r.x + refHalf)
		if d >= -1e-7 {
			rx = math.Min(dx, d)
		}
		if math.Abs(rx) < 1e-7 {
			rx = 0
		}
	}
	r.x += rx
	r.y += ry
	hc = math.Abs(rx-dx) >= float64(float32(1e-5)) // !Mth.equal
	r.onGround = ry != dy && dy < 0
	if hc {
		r.vx = 0
	}
	if ry != dy {
		r.vy = 0 // Block.updateEntityMovementAfterFallOn
	}
	return hc
}

// free は Entity.isFree(dx, dy, 0)。x は 0（ぶつかって 0 になっている）なので壁とは重ならない。液体のブロックと重なるかだけ見る。
func (r *shoreRef) free(dy float64) bool {
	return math.Floor(r.y+dy) > refWaterTop
}

func (r *shoreRef) tick() shoreState {
	in, h := r.fluid()
	// LivingEntity.aiStep: 小さな速さを 0 に
	if math.Abs(r.vx) < 0.003 {
		r.vx = 0
	}
	if math.Abs(r.vy) < 0.003 {
		r.vy = 0
	}
	// ジャンプ（地面にいないので、水の中なら jumpInLiquid、外なら何もしない）
	if in && h > 0 && !r.onGround {
		r.vy += float64(float32(0.04))
	}
	// moveRelative: 東を向いて前（zza = 1 * 0.98F）。Mth.sin(-90 度) = -1、Mth.cos = 0 なので x に足す
	zza := float64(float32(0.98))
	st := shoreState{InWater: in, FluidHeight: h}
	if in {
		// travelInFluid（水）
		falling := r.vy <= 0
		y0 := r.y
		r.vx += zza * float64(float32(0.02))
		st.HorizontalColl = r.move(r.vx, r.vy)
		slow := float64(float32(0.8))
		r.vx *= slow
		r.vy *= float64(float32(0.8))
		// getFluidFallingAdjustedMovement
		if falling && math.Abs(r.vy-0.005) >= 0.003 && math.Abs(r.vy-0.08/16) < 0.003 {
			r.vy = -0.003
		} else {
			r.vy -= 0.08 / 16
		}
		if st.HorizontalColl && r.free(r.vy+float64(float32(0.6))-r.y+y0) {
			r.vy = float64(float32(0.3))
			st.ClimbOut = true
		}
	} else {
		// travelInAir（地面にいないので滑りやすさ 1、加速 0.02F）
		if r.onGround {
			return shoreState{Y: r.y, X: r.x, OnGround: true}
		}
		r.vx += zza * float64(float32(0.02))
		st.HorizontalColl = r.move(r.vx, r.vy)
		r.vx *= float64(float32(1) * float32(0.91))
		r.vy = (r.vy - 0.08) * float64(float32(0.98))
	}
	st.Y, st.VY, st.X, st.OnGround = r.y, r.vy, r.x, r.onGround
	return st
}

// shoreStart は壁に東の面をつけ、水の中（足 y=3）に立たせたプレイヤー。
func shoreStart() (*Player, *shoreRef) {
	x := 6 - refHalf
	p := newPlayer(x, 3, 0.5)
	p.Yaw = -90
	return p, &shoreRef{x: x, y: 3}
}

// 岸が水のブロックと同じ高さ（top=5）と 1 段高い（top=6）とき、移植したものと本家の写しが、
// 岸の上に出るまで毎ティック同じ値になる。同じ高さなら上がれ、1 段高いと上がれない。
func TestShoreClimbTrace(t *testing.T) {
	for _, top := range []int{5, 6} {
		w := deepPool(t, top == 6)
		p, ref := shoreStart()
		ref.top = top
		climbed := -1
		maxY := 0.0
		for n := range 200 {
			if ref.onGround {
				// 本家の写しは岸の上に立ったところまで
				climbed = n
				break
			}
			p.Tick(w, Input{Forward: true, Jump: true})
			got := shoreState{
				Y: p.Pos.Y, VY: p.Vel.Y, X: p.Pos.X,
				InWater: p.InWater, FluidHeight: p.WaterHeight,
				HorizontalColl: p.HorizontalCollision, OnGround: p.OnGround,
			}
			want := ref.tick()
			got.ClimbOut = want.ClimbOut && p.Vel.Y == float64(FluidClimbOut)
			if n < 40 || want.ClimbOut || !want.InWater && want.VY > 0 {
				t.Logf("top=%d n=%3d y=%.6f vy=%+.6f 水=%v 深さ=%.6f 横=%v 0.3=%v 地面=%v",
					top, n, got.Y, got.VY, got.InWater, got.FluidHeight, got.HorizontalColl, want.ClimbOut, got.OnGround)
			}
			if !shoreEqual(got, want) {
				t.Fatalf("top=%d n=%d ずれ:\n移植 %+v\n本家 %+v", top, n, got, want)
			}
			maxY = max(maxY, got.Y)
		}
		switch top {
		case 5:
			if climbed < 0 || p.Pos.Y != 5 || !p.OnGround || p.Pos.X <= 6 {
				t.Errorf("同じ高さの岸に上がれない: %v 地面 %v", p.Pos, p.OnGround)
			}
			t.Logf("top=5: %d ティックで岸に立った", climbed)
		case 6:
			if climbed >= 0 || maxY >= 6 {
				t.Errorf("1 段高い岸に上がった（一番高い足 %.6f）", maxY)
			}
			t.Logf("top=6: 一番高い足 %.6f（岸の上面 6 に %.6f 足りない）", maxY, 6-maxY)
		}
	}
}

func shoreEqual(a, b shoreState) bool {
	const eps = 1e-12
	return near(a.Y, b.Y, eps) && near(a.VY, b.VY, eps) && near(a.X, b.X, eps) &&
		a.InWater == b.InWater && near(a.FluidHeight, b.FluidHeight, eps) &&
		a.HorizontalColl == b.HorizontalColl && a.ClimbOut == b.ClimbOut && a.OnGround == b.OnGround
}

// 1 段高い岸: 水から出る直前のティック（初めに水の中で、そのティックの移動で水面より上に出る）の足の高さ y0 が、
// 取りうる一番高い値（水面 4.8889 - 0.001 ちょうど）でも、足は 6 に届かない。
// 本家の写しで、水から出る最後のティックの y0 を水の中の全範囲で細かく変えて、一番高く上がる足を求める。
func TestShoreOneBlockHigherBound(t *testing.T) {
	surface := float64(float32(refWaterTop) + float32(8)/9) // 水面 4.8888...
	// 水の中である条件は surface >= y0 + 0.001。前のティックに 0.3 をもらっている（壁で上がり続けている）とする
	best, bestY0 := 0.0, 0.0
	for i := 0; i <= 10000; i++ {
		y0 := surface - 0.001 - 0.34*float64(i)/10000
		r := &shoreRef{top: 6, x: 6 - refHalf, y: y0, vy: float64(float32(0.3))}
		top := 0.0
		for range 30 {
			st := r.tick()
			top = max(top, st.Y)
			if st.InWater && st.VY < 0 {
				break
			}
		}
		if top > best {
			best, bestY0 = top, y0
		}
	}
	t.Logf("水から出る最後のティックの足 %.6f から、一番高い足 %.6f（6 に %.6f 足りない）", bestY0, best, 6-best)
	if best >= 6 {
		t.Errorf("本家の写しで 1 段高い岸に届いた（%.6f）", best)
	}
	// 内わけ: 最後の水のティックで 0.3 + 0.04 = 0.34 上がり、0.34 * 0.8 - 0.005 = 0.267 < 0.3 なので 0.3 をもらう
	// （その箱を 0.6 + 0.267 上げると下が y=5 以上で水と重ならない）。そこから空中で 0.3、0.2156、0.1329、0.0518 上がる
	rise := 0.0
	v := float64(float32(0.3))
	for v > 0 {
		rise += v
		v = (v - 0.08) * float64(float32(0.98))
	}
	want := surface - 0.001 + float64(float32(0.3)) + float64(float32(0.04)) + rise
	if !near(best, want, 1e-4) {
		t.Errorf("一番高い足 %.6f、内わけの計算では %.6f", best, want)
	}
}
