// Package physics はプレイヤーの物理（本家 Java 版 1.21.4 のクライアントの動き）を計算する。
//
// 本家の LocalPlayer.aiStep → LivingEntity.aiStep → LivingEntity.travel → Entity.move の流れを、
// 1 ティックずつ同じ順番でなぞる。値は本家に合わせて float（float32）で計算するところは float32 にしている。
//
// 扱うもの: 当たり判定（幅 0.6、高さ 1.8）、重力と空気抵抗、地面の滑りやすさ、歩く・走る、ジャンプ（走りジャンプ）、
// 段差の自動の上り（0.6）、ブロックとの衝突、水と溶岩の中の動き、梯子・つる、スライムとベッドの跳ね返り、
// ソウルサンドとハチミツブロックの遅さ。
//
// 扱わないもの: しゃがみ、泳ぎの姿勢（水中の走り）、飛行、エリトラ、乗り物、ステータス効果、エンチャント、
// 水の流れに押される力、クモの巣・甘い実の茂み・粉雪、足場（scaffolding）の特別な当たり判定、ほかのエンティティとの衝突。
package physics

import (
	"math"

	"github.com/Satoru314/go-mcdriver/data"
	"gonum.org/v1/gonum/spatial/r3"
)

// 本家と合わせた定数。出典は本家 1.21.4 のソース（Mojang のマッピングでの名前）。
const (
	// PlayerWidth と PlayerHeight は立っているプレイヤーの当たり判定（Player.STANDING_DIMENSIONS = 0.6F × 1.8F）
	PlayerWidth  float32 = 0.6
	PlayerHeight float32 = 1.8
	// EyeHeight は立っているときの目の高さ（Player.DEFAULT_EYE_HEIGHT = 1.62F）
	EyeHeight float32 = 1.62

	// Gravity は重力（Attributes.GRAVITY の既定 0.08）
	Gravity = 0.08
	// AirDrag は y の速さに毎ティック掛ける値（LivingEntity.travelInAir の 0.98F）
	AirDrag float32 = 0.98
	// DefaultFriction はふつうのブロックの滑りやすさ（BlockBehaviour.Properties の friction 0.6F）
	DefaultFriction float32 = 0.6
	// FrictionFactor は滑りやすさに掛けて横の速さの減り方にする値（travelInAir の 0.91F）
	FrictionFactor float32 = 0.91
	// GroundAccelBase は地上の加速の計算の分子（LivingEntity.getFrictionInfluencedSpeed の 0.21600002F = 0.6^3）
	GroundAccelBase float32 = 0.21600002
	// WalkSpeed は歩く速さ（Attributes.MOVEMENT_SPEED のプレイヤーの既定 0.1F）
	WalkSpeed float32 = 0.1
	// SprintModifier は走るときに速さに掛ける割合（LivingEntity.SPEED_MODIFIER_SPRINTING: +0.3F, ADD_MULTIPLIED_TOTAL）
	SprintModifier float32 = 0.3
	// AirAccel と SprintAirAccel は空中の加速（Player.getFlyingSpeed の 0.02F と 0.025999999F）
	AirAccel       float32 = 0.02
	SprintAirAccel float32 = 0.025999999
	// InputScale は入力（前後左右の ±1）に掛ける値（LivingEntity.aiStep の xxa *= 0.98F）
	InputScale float32 = 0.98
	// JumpPower はジャンプの初速（Attributes.JUMP_STRENGTH の既定 0.42F）
	JumpPower float32 = 0.42
	// SprintJumpBoost は走りジャンプで前に足す速さ（LivingEntity.jumpFromGround の 0.2）
	SprintJumpBoost = 0.2
	// JumpDelay は続けてジャンプできるまでのティック（LivingEntity.noJumpDelay = 10）
	JumpDelay = 10
	// StepHeight は自動で上れる段差の高さ（Attributes.STEP_HEIGHT の既定 0.6）
	StepHeight float32 = 0.6

	// WaterSlowDown は水の中で速さに掛ける値（Player は 0.8F。走っていれば 0.9F）
	WaterSlowDown float32 = 0.8
	// FluidAccel は液体の中の加速（travelInFluid の 0.02F）
	FluidAccel float32 = 0.02
	// FluidVerticalDrag は液体の中で y の速さに掛ける値（travelInFluid の 0.8F）
	FluidVerticalDrag float32 = 0.8
	// FluidJump は液体の中でジャンプしたときに足す上向きの速さ（LivingEntity.jumpInLiquid の 0.04）
	FluidJump = 0.04
	// FluidJumpThreshold は液体から地面のジャンプに切り替わる液体の深さ（LivingEntity.getFluidJumpThreshold = 0.4）
	FluidJumpThreshold = 0.4
	// FluidClimbOut は液体の中で壁にぶつかったとき、上に出られるなら y に入れる速さ（travelInFluid の 0.3F）
	FluidClimbOut float32 = 0.3
	// LavaHorizontalDrag は溶岩の中の横の速さに掛ける値（0.5）
	LavaHorizontalDrag = 0.5

	// ClimbMaxSpeed は梯子の上の横と下向きの速さの上限（LivingEntity.handleOnClimbable の 0.15F）
	ClimbMaxSpeed float32 = 0.15
	// ClimbUpSpeed は梯子の上で前に押しているかジャンプしているときの上向きの速さ（0.2）
	ClimbUpSpeed = 0.2
)

// World はブロックを読むための口。
type World interface {
	// Block は (x, y, z) のブロックの状態を返す。そのチャンクが読み込まれていなければ ok は false。
	// 世界の高さの外は空気（ok は true）を返すこと。
	Block(x, y, z int) (s data.BlockState, ok bool)
}

// Input は 1 ティックの入力（キー）。
type Input struct {
	Forward, Back, Left, Right, Jump, Sprint bool
}

// BlockPos は整数の座標（このパッケージの中で使う）。
type BlockPos struct{ X, Y, Z int }

// Player はプレイヤーの物理の状態。ゼロ値から Pos を決めて使う。
type Player struct {
	// Pos は足元の中心の位置、Vel は 1 ティックあたりの速さ（本家の deltaMovement）
	Pos, Vel r3.Vec
	// Yaw と Pitch は向き（度）。Yaw 0 が南（+z）、90 が西（-x）。Pitch は下向きが正
	Yaw, Pitch float32

	// OnGround は地面に立っているか（直前の移動で下にぶつかった）
	OnGround bool
	// HorizontalCollision は直前の移動で横にぶつかったか
	HorizontalCollision bool
	// MinorHorizontalCollision は横にぶつかったが、向きがほぼ壁に沿っている（走りが止まらない）か
	MinorHorizontalCollision bool
	// VerticalCollision は直前の移動で上か下にぶつかったか
	VerticalCollision bool
	// Sprinting は走っているか
	Sprinting bool

	// InWater と InLava は液体に触れているか。WaterHeight と LavaHeight は足元からの液体の深さ
	InWater, InLava         bool
	WaterHeight, LavaHeight float64

	jumping     bool
	noJumpDelay int
	xxa, zza    float32

	// 立っているブロック（本家の mainSupportingBlockPos）
	support          BlockPos
	hasSupport       bool
	onGroundNoBlocks bool
}

// HalfWidth は当たり判定の幅の半分（本家と同じく float の 0.3F を double にした値）。
var HalfWidth = float64(PlayerWidth / 2)

// BoxAt は足元の中心が pos のときの当たり判定の箱を返す（本家の EntityDimensions.makeBoundingBox）。
func BoxAt(pos r3.Vec) r3.Box {
	h := HalfWidth
	return r3.Box{
		Min: r3.Vec{X: pos.X - h, Y: pos.Y, Z: pos.Z - h},
		Max: r3.Vec{X: pos.X + h, Y: pos.Y + float64(PlayerHeight), Z: pos.Z + h},
	}
}

// Box は今の当たり判定の箱を返す。
func (p *Player) Box() r3.Box { return BoxAt(p.Pos) }

// Loaded は p の周り（当たり判定の範囲と、その 2 ブロック外まで）のチャンクが読み込まれていれば true を返す。
func Loaded(w World, pos r3.Vec) bool {
	b := BoxAt(pos)
	y := floor(pos.Y)
	for x := floor(b.Min.X) - 2; ; x += 16 {
		if x > floor(b.Max.X)+2 {
			x = floor(b.Max.X) + 2
		}
		for z := floor(b.Min.Z) - 2; ; z += 16 {
			if z > floor(b.Max.Z)+2 {
				z = floor(b.Max.Z) + 2
			}
			if _, ok := w.Block(x, y, z); !ok {
				return false
			}
			if z == floor(b.Max.Z)+2 {
				break
			}
		}
		if x == floor(b.Max.X)+2 {
			break
		}
	}
	return true
}

// Tick は 1 ティック分、物理を進める。周りのチャンクが読み込まれていなければ何もせず false を返す。
func (p *Player) Tick(w World, in Input) bool {
	if !Loaded(w, p.Pos) {
		return false
	}
	// Entity.baseTick: 液体に触れているか
	p.updateFluids(w)

	// LocalPlayer.aiStep: 走るかどうか
	forward := impulse(in.Forward, in.Back)
	left := impulse(in.Left, in.Right)
	// 本家と同じく、走り始める判定のあとに、やめる判定をする（壁に向かって押し続けても走らない）
	if !p.Sprinting && in.Sprint && forward >= 0.8 && !p.InWater {
		// 泳ぎの姿勢を扱わないので、水の中では走らない（本家は水中なら泳ぎになる）
		p.Sprinting = true
	}
	if p.Sprinting {
		// 前に押していない、正面から壁にぶつかった、水に入った、走るのをやめた、なら走るのをやめる
		if !in.Sprint || forward <= 1e-5 || (p.HorizontalCollision && !p.MinorHorizontalCollision) || p.InWater {
			p.Sprinting = false
		}
	}

	// LivingEntity.aiStep
	if p.noJumpDelay > 0 {
		p.noJumpDelay--
	}
	// プレイヤーは横の速さが小さければ 0 にする（9.0E-6 = 0.003^2）
	if horizontalDistSqr(p.Vel) < 9.0e-6 {
		p.Vel.X, p.Vel.Z = 0, 0
	}
	if math.Abs(p.Vel.Y) < 0.003 {
		p.Vel.Y = 0
	}
	// LocalPlayer.serverAiStep
	p.xxa, p.zza = left, forward
	p.jumping = in.Jump

	if p.jumping {
		d3 := p.WaterHeight
		if p.InLava {
			d3 = p.LavaHeight
		}
		inWaterDeep := p.InWater && d3 > 0
		if !inWaterDeep || (p.OnGround && !(d3 > FluidJumpThreshold)) {
			if !p.InLava || (p.OnGround && !(d3 > FluidJumpThreshold)) {
				if (p.OnGround || inWaterDeep && d3 <= FluidJumpThreshold) && p.noJumpDelay == 0 {
					p.jumpFromGround(w)
					p.noJumpDelay = JumpDelay
				}
			} else {
				p.Vel.Y += FluidJump
			}
		} else {
			p.Vel.Y += FluidJump
		}
	} else {
		p.noJumpDelay = 0
	}
	p.xxa *= InputScale
	p.zza *= InputScale

	p.travel(w, r3.Vec{X: float64(p.xxa), Z: float64(p.zza)})
	return true
}

// impulse は本家の KeyboardInput.calculateImpulse（両方押していれば 0）。
func impulse(pos, neg bool) float32 {
	if pos == neg {
		return 0
	}
	if pos {
		return 1
	}
	return -1
}

// speed は今の歩く速さ（Attributes.MOVEMENT_SPEED。本家は double で計算して float にする）。
func (p *Player) speed() float32 {
	base := float64(WalkSpeed)
	if p.Sprinting {
		return float32(base * (1 + float64(SprintModifier)))
	}
	return float32(base)
}

// jumpFromGround は本家の LivingEntity.jumpFromGround。
func (p *Player) jumpFromGround(w World) {
	f := JumpPower * p.blockJumpFactor(w)
	if f <= 1e-5 {
		return
	}
	p.Vel.Y = math.Max(float64(f), p.Vel.Y)
	if p.Sprinting {
		r := float64(p.Yaw * (math.Pi / 180))
		p.Vel.X += -float64(mthSin(r)) * SprintJumpBoost
		p.Vel.Z += float64(mthCos(r)) * SprintJumpBoost
	}
}

// travel は本家の LivingEntity.travel（液体の中か、それ以外か）。
func (p *Player) travel(w World, input r3.Vec) {
	if p.InWater || p.InLava {
		p.travelInFluid(w, input)
	} else {
		p.travelInAir(w, input)
	}
}

func (p *Player) travelInAir(w World, input r3.Vec) {
	below := p.onPos(w, 0.500001)
	friction := float32(1)
	if p.OnGround {
		friction = p.infoAt(w, below).friction
	}
	f1 := friction * FrictionFactor

	// handleRelativeFrictionAndCalculateMovement
	p.moveRelative(p.frictionInfluencedSpeed(friction), input)
	p.handleOnClimbable(w)
	p.move(w, p.Vel)
	v := p.Vel
	if (p.HorizontalCollision || p.jumping) && p.onClimbable(w) {
		v.Y = ClimbUpSpeed
	}

	dy := v.Y - Gravity
	p.Vel = r3.Vec{X: v.X * float64(f1), Y: dy * float64(AirDrag), Z: v.Z * float64(f1)}
}

func (p *Player) frictionInfluencedSpeed(friction float32) float32 {
	if p.OnGround {
		return p.speed() * (GroundAccelBase / (friction * friction * friction))
	}
	if p.Sprinting {
		return SprintAirAccel
	}
	return AirAccel
}

func (p *Player) travelInFluid(w World, input r3.Vec) {
	falling := p.Vel.Y <= 0
	y0 := p.Pos.Y
	if p.InWater {
		slow := WaterSlowDown
		if p.Sprinting {
			slow = 0.9
		}
		p.moveRelative(FluidAccel, input)
		p.move(w, p.Vel)
		v := p.Vel
		if p.HorizontalCollision && p.onClimbable(w) {
			v.Y = ClimbUpSpeed
		}
		v = r3.Vec{X: v.X * float64(slow), Y: v.Y * float64(FluidVerticalDrag), Z: v.Z * float64(slow)}
		p.Vel = p.fluidFallingAdjusted(falling, v)
	} else {
		p.moveRelative(FluidAccel, input)
		p.move(w, p.Vel)
		if p.LavaHeight <= FluidJumpThreshold {
			p.Vel = r3.Vec{X: p.Vel.X * LavaHorizontalDrag, Y: p.Vel.Y * float64(FluidVerticalDrag), Z: p.Vel.Z * LavaHorizontalDrag}
			p.Vel = p.fluidFallingAdjusted(falling, p.Vel)
		} else {
			p.Vel = r3.Scale(0.5, p.Vel)
		}
		p.Vel.Y += -Gravity / 4
	}
	v := p.Vel
	if p.HorizontalCollision && p.isFree(w, v.X, v.Y+float64(StepHeight)-p.Pos.Y+y0, v.Z) {
		p.Vel.Y = float64(FluidClimbOut)
	}
}

// fluidFallingAdjusted は本家の LivingEntity.getFluidFallingAdjustedMovement。
func (p *Player) fluidFallingAdjusted(falling bool, v r3.Vec) r3.Vec {
	if p.Sprinting {
		return v
	}
	if falling && math.Abs(v.Y-0.005) >= 0.003 && math.Abs(v.Y-Gravity/16) < 0.003 {
		v.Y = -0.003
	} else {
		v.Y -= Gravity / 16
	}
	return v
}

// moveRelative は本家の Entity.moveRelative（入力を向きに合わせて回し、speed を掛けて速さに足す）。
func (p *Player) moveRelative(speed float32, input r3.Vec) {
	d := lengthSqr(input)
	if d < 1e-7 {
		return
	}
	v := input
	if d > 1 {
		l := math.Sqrt(d)
		if l < 1e-5 {
			return
		}
		v = r3.Scale(1/l, v)
	}
	v = r3.Scale(float64(speed), v)
	r := float64(p.Yaw * (math.Pi / 180))
	s, c := float64(mthSin(r)), float64(mthCos(r))
	p.Vel = r3.Add(p.Vel, r3.Vec{X: v.X*c - v.Z*s, Y: v.Y, Z: v.Z*c + v.X*s})
}

// handleOnClimbable は梯子の上での速さの制限（本家の LivingEntity.handleOnClimbable。しゃがみは扱わない）。
func (p *Player) handleOnClimbable(w World) {
	if !p.onClimbable(w) {
		return
	}
	m := float64(ClimbMaxSpeed)
	p.Vel.X = max(-m, min(m, p.Vel.X))
	p.Vel.Z = max(-m, min(m, p.Vel.Z))
	p.Vel.Y = max(p.Vel.Y, -m)
}

// onClimbable は本家の LivingEntity.onClimbable（足元のブロックが梯子・つるか、梯子の上の開いたトラップドア）。
func (p *Player) onClimbable(w World) bool {
	pos := p.blockPosition()
	in := p.infoAt(w, pos)
	if in.climbable {
		return true
	}
	if in.trapdoor && in.open {
		below := p.infoAt(w, BlockPos{pos.X, pos.Y - 1, pos.Z})
		return below.ladder && below.facing == in.facing
	}
	return false
}

// move は本家の Entity.move（MoverType.SELF）。
func (p *Player) move(w World, mv r3.Vec) {
	res := p.collide(w, p.Box(), mv)
	d0 := lengthSqr(res)
	if d0 > 1e-7 || lengthSqr(mv)-d0 < 1e-7 {
		p.Pos = r3.Add(p.Pos, res)
	}
	hx := !mthEqual(mv.X, res.X)
	hz := !mthEqual(mv.Z, res.Z)
	p.HorizontalCollision = hx || hz
	p.VerticalCollision = mv.Y != res.Y
	p.OnGround = p.VerticalCollision && mv.Y < 0
	p.checkSupportingBlock(w, p.OnGround, res)
	if p.HorizontalCollision {
		p.MinorHorizontalCollision = p.isHorizontalCollisionMinor(res)
	} else {
		p.MinorHorizontalCollision = false
	}
	on := p.infoAt(w, p.onPos(w, 0.2))
	if p.HorizontalCollision {
		if hx {
			p.Vel.X = 0
		}
		if hz {
			p.Vel.Z = 0
		}
	}
	if mv.Y != res.Y {
		// Block.updateEntityMovementAfterFallOn
		switch {
		case on.slime && p.Vel.Y < 0:
			p.Vel.Y = -p.Vel.Y
		case on.bed && p.Vel.Y < 0:
			p.Vel.Y = -p.Vel.Y * float64(float32(0.66))
		default:
			if !on.slime && !on.bed {
				p.Vel.Y = 0
			}
		}
	}
	if p.OnGround && on.slime {
		// SlimeBlock.stepOn
		if d := math.Abs(p.Vel.Y); d < 0.1 {
			f := 0.4 + d*0.2
			p.Vel.X *= f
			p.Vel.Z *= f
		}
	}
	f := float64(p.blockSpeedFactor(w))
	p.Vel.X *= f
	p.Vel.Z *= f
}

// collide は本家の Entity.collide（ぶつかったら止まり、地面にいれば段差を上る）。
func (p *Player) collide(w World, box r3.Box, mv r3.Vec) r3.Vec {
	res := mv
	if lengthSqr(mv) != 0 {
		res = collideWithShapes(mv, box, blockBoxes(w, expandTowards(box, mv)))
	}
	cx, cy, cz := mv.X != res.X, mv.Y != res.Y, mv.Z != res.Z
	landed := cy && mv.Y < 0
	if (landed || p.OnGround) && (cx || cz) {
		b1 := box
		if landed {
			b1 = moveBox(box, 0, res.Y, 0)
		}
		b2 := expandTowards(b1, r3.Vec{X: mv.X, Y: float64(StepHeight), Z: mv.Z})
		if !landed {
			b2 = expandTowards(b2, r3.Vec{Y: float64(float32(-1e-5))})
		}
		shapes := blockBoxes(w, b2)
		for _, h := range stepCandidates(b1, shapes, StepHeight, float32(res.Y)) {
			v := collideWithShapes(r3.Vec{X: mv.X, Y: float64(h), Z: mv.Z}, b1, shapes)
			if horizontalDistSqr(v) > horizontalDistSqr(res) {
				d := box.Min.Y - b1.Min.Y
				return r3.Add(v, r3.Vec{Y: -d})
			}
		}
	}
	return res
}

// isHorizontalCollisionMinor は本家の LivingEntity.isHorizontalCollisionMinor（入力の向きと動いた向きの差が 8 度未満）。
func (p *Player) isHorizontalCollisionMinor(v r3.Vec) bool {
	r := float64(p.Yaw * (math.Pi / 180))
	s, c := float64(mthSin(r)), float64(mthCos(r))
	d2 := float64(p.xxa)*c - float64(p.zza)*s
	d3 := float64(p.zza)*c + float64(p.xxa)*s
	d4 := d2*d2 + d3*d3
	d5 := v.X*v.X + v.Z*v.Z
	if d4 < float64(float32(1e-5)) || d5 < float64(float32(1e-5)) {
		return false
	}
	d6 := d2*v.X + d3*v.Z
	return math.Acos(d6/math.Sqrt(d4*d5)) < float64(float32(0.13962634))
}

// checkSupportingBlock は本家の Entity.checkSupportingBlock（立っているブロックを決める）。
func (p *Player) checkSupportingBlock(w World, onGround bool, mv r3.Vec) {
	if !onGround {
		p.onGroundNoBlocks = false
		p.hasSupport = false
		return
	}
	b := p.Box()
	q := r3.Box{Min: r3.Vec{X: b.Min.X, Y: b.Min.Y - 1e-6, Z: b.Min.Z}, Max: r3.Vec{X: b.Max.X, Y: b.Min.Y, Z: b.Max.Z}}
	pos, ok := p.findSupportingBlock(w, q)
	if ok || p.onGroundNoBlocks {
		p.support, p.hasSupport = pos, ok
	} else {
		pos, ok = p.findSupportingBlock(w, moveBox(q, -mv.X, 0, -mv.Z))
		p.support, p.hasSupport = pos, ok
	}
	p.onGroundNoBlocks = !ok
}

// findSupportingBlock は q と重なる当たり判定を持つブロックのうち、中心が自分の位置に一番近いものを返す
// （本家の Level.findSupportingBlock）。
func (p *Player) findSupportingBlock(w World, q r3.Box) (BlockPos, bool) {
	var best BlockPos
	bestD := math.Inf(1)
	found := false
	for y := floor(q.Min.Y-collisionEpsilon) - 1; y <= floor(q.Max.Y+collisionEpsilon)+1; y++ {
		for z := floor(q.Min.Z-collisionEpsilon) - 1; z <= floor(q.Max.Z+collisionEpsilon)+1; z++ {
			for x := floor(q.Min.X-collisionEpsilon) - 1; x <= floor(q.Max.X+collisionEpsilon)+1; x++ {
				s, ok := w.Block(x, y, z)
				if !ok || s.Block == nil {
					continue
				}
				hit := false
				for _, b := range s.CollisionBoxes() {
					if intersects(moveBox(b, float64(x), float64(y), float64(z)), q) {
						hit = true
						break
					}
				}
				if !hit {
					continue
				}
				pos := BlockPos{x, y, z}
				dx, dy, dz := float64(x)+0.5-p.Pos.X, float64(y)+0.5-p.Pos.Y, float64(z)+0.5-p.Pos.Z
				d := dx*dx + dy*dy + dz*dz
				if d < bestD || d == bestD && comparePos(pos, best) < 0 {
					best, bestD, found = pos, d, true
				}
			}
		}
	}
	return best, found
}

// comparePos は本家の Vec3i.compareTo（y、z、x の順）。
func comparePos(a, b BlockPos) int {
	switch {
	case a.Y != b.Y:
		return a.Y - b.Y
	case a.Z != b.Z:
		return a.Z - b.Z
	}
	return a.X - b.X
}

// onPos は本家の Entity.getOnPos（立っているブロックから offset 下の位置）。
func (p *Player) onPos(w World, offset float32) BlockPos {
	if p.hasSupport {
		if offset <= 1e-5 {
			return p.support
		}
		in := p.infoAt(w, p.support)
		if (float64(offset) <= 0.5 && in.fenceLike) || in.wallLike {
			return p.support
		}
		return BlockPos{p.support.X, floor(p.Pos.Y - float64(offset)), p.support.Z}
	}
	return BlockPos{floor(p.Pos.X), floor(p.Pos.Y - float64(offset)), floor(p.Pos.Z)}
}

func (p *Player) blockPosition() BlockPos {
	return BlockPos{floor(p.Pos.X), floor(p.Pos.Y), floor(p.Pos.Z)}
}

// blockSpeedFactor は本家の Entity.getBlockSpeedFactor。
func (p *Player) blockSpeedFactor(w World) float32 {
	pos := p.blockPosition()
	s, _ := w.Block(pos.X, pos.Y, pos.Z)
	in := infoOf(s)
	if s.Block != nil && (s.Block.Name == "water" || s.Block.Name == "bubble_column") {
		return in.speedFactor
	}
	if in.speedFactor == 1 {
		return p.infoAt(w, p.onPos(w, 0.500001)).speedFactor
	}
	return in.speedFactor
}

// blockJumpFactor は本家の Entity.getBlockJumpFactor。
func (p *Player) blockJumpFactor(w World) float32 {
	f := p.infoAt(w, p.blockPosition()).jumpFactor
	if f == 1 {
		return p.infoAt(w, p.onPos(w, 0.500001)).jumpFactor
	}
	return f
}

func (p *Player) infoAt(w World, pos BlockPos) *stateInfo {
	s, ok := w.Block(pos.X, pos.Y, pos.Z)
	if !ok {
		return &airInfo
	}
	return infoOf(s)
}

// updateFluids は本家の Entity.updateFluidHeightAndDoFluidPushing（流れに押される力は扱わない）。
func (p *Player) updateFluids(w World) {
	b := deflate(p.Box(), 0.001)
	p.InWater, p.WaterHeight = false, 0
	p.InLava, p.LavaHeight = false, 0
	for x := floor(b.Min.X); x < ceil(b.Max.X); x++ {
		for y := floor(b.Min.Y); y < ceil(b.Max.Y); y++ {
			for z := floor(b.Min.Z); z < ceil(b.Max.Z); z++ {
				in := p.infoAt(w, BlockPos{x, y, z})
				if in.fluid == noFluid {
					continue
				}
				h := in.ownHeight()
				if p.infoAt(w, BlockPos{x, y + 1, z}).fluid == in.fluid {
					h = 1
				}
				top := float64(float32(y) + h)
				if top < b.Min.Y {
					continue
				}
				d := top - b.Min.Y
				if in.fluid == water {
					p.InWater = true
					p.WaterHeight = max(p.WaterHeight, d)
				} else {
					p.LavaHeight = max(p.LavaHeight, d)
				}
			}
		}
	}
	p.InLava = p.LavaHeight > 0
}

// isFree は箱を (dx, dy, dz) 動かした先に、当たり判定も液体も無ければ true（本家の Entity.isFree）。
func (p *Player) isFree(w World, dx, dy, dz float64) bool {
	b := moveBox(p.Box(), dx, dy, dz)
	if len(blockBoxes(w, b)) > 0 {
		return false
	}
	for x := floor(b.Min.X); x < ceil(b.Max.X); x++ {
		for y := floor(b.Min.Y); y < ceil(b.Max.Y); y++ {
			for z := floor(b.Min.Z); z < ceil(b.Max.Z); z++ {
				if p.infoAt(w, BlockPos{x, y, z}).fluid != noFluid {
					return false
				}
			}
		}
	}
	return true
}

// SetTeleport はサーバーからの位置の強制修正を当てはめる（位置・速さ・向き）。
func (p *Player) SetTeleport(pos, vel r3.Vec, yaw, pitch float32) {
	p.Pos, p.Vel, p.Yaw, p.Pitch = pos, vel, yaw, pitch
}
