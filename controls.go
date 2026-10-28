package mcdriver

// 移動の操作（前後左右・ジャンプ・走る）と向き、毎ティックの物理と位置の送信。
//
// 毎ティック（client.go の tick）に、本家のクライアント（LocalPlayer.tick）と同じ順で次を行う:
//  1. 物理を 1 ティック進める（internal/physics）。周りのチャンクが読み込まれていなければ動かない
//  2. しゃがむキーが変わっていれば Player Command（しゃがむ / しゃがみやめ）を送る（sendShiftKeyState）
//  3. キーの状態が変わっていれば Player Input を送る
//  4. 走る状態が変わっていれば Player Command（走り始め / 走りやめ）を送る
//  5. 位置と向きを、本家の LocalPlayer.sendPosition と同じ決まりで送る:
//     2.0E-4 より動いたか 20 ティック送っていなければ位置、向きが変わっていれば向き、
//     どちらも無く接地か横の衝突が変わっていれば Status Only。何も変わらなければ送らない

import (
	"fmt"
	"math"

	"github.com/Satoru314/go-mcdriver/data"
	"github.com/Satoru314/go-mcdriver/internal/chunk"
	"github.com/Satoru314/go-mcdriver/internal/packets"
	"github.com/Satoru314/go-mcdriver/internal/physics"
	"github.com/Satoru314/go-mcdriver/internal/protocol"
	"gonum.org/v1/gonum/spatial/r3"
)

// Controls は移動の操作（押しているキー）。SetControls で設定すると、変えるまで押し続ける。
type Controls struct {
	Forward, Back, Left, Right bool
	// Jump は押しているあいだ、着地するたびにジャンプする。水の中では浮き上がる
	Jump bool
	// Sprint は前に進んでいるあいだ走る。正面から壁にぶつかったとき、前に押していないとき、しゃがんでいるとき、
	// 満腹度が 6 以下のときは走らない。水に浸かっていても目が水の外なら走らず、目まで水に入っていれば泳ぐ（当たり判定が 0.6 の高さになる）
	Sprint bool
	// Sneak はしゃがむ。遅く（0.3 倍）なり、当たり判定の高さが 1.5 になり、地面の端から落ちない。水の中では沈む
	Sneak bool
}

// moveState は移動の状態。Client の mu で守る（位置 cl.x などと一緒に扱うため）。
type moveState struct {
	ctl Controls
	p   physics.Player

	// 最後に送った値（本家の LocalPlayer の xLast, yRotLast, lastOnGround など）
	lastX, lastY, lastZ          float64
	lastYaw, lastPitch           float32
	lastOnGround, lastHorizontal bool
	positionReminder             int
	sentInput                    uint8
	sentSprint                   bool
	sentShift                    bool
}

var moveKey = newFeature(func(*Client) *moveState { return &moveState{} })

func init() {
	// ディメンションに入り直したら速さなどを捨てる（本家はプレイヤーを作り直す）
	onPlay(data.PlayClientboundRespawn, func(cl *Client, _ *protocol.Decoder) error {
		cl.mu.Lock()
		defer cl.mu.Unlock()
		ms := moveKey.of(cl)
		ms.p = physics.Player{}
		// 本家はプレイヤーを作り直すので、最後に送ったキーと走る・しゃがむの状態も初めに戻る
		ms.sentInput, ms.sentSprint, ms.sentShift = 0, false, false
		return nil
	})
	onPlay(data.PlayClientboundSetEntityMotion, (*Client).moveOnMotion)
}

// moveOnMotion は Set Entity Motion が自分のものなら速さにする（ノックバックなど。本家は setDeltaMovement）。
// 中身: エンティティ ID（VarInt）、速度 x, y, z（Short、1/8000 ブロック/ティック）。
func (cl *Client) moveOnMotion(d *protocol.Decoder) error {
	id, err := d.ReadVarInt()
	if err != nil {
		return fmt.Errorf("mcdriver: Set Entity Motion: %w", err)
	}
	var v [3]int16
	for i := range v {
		if v[i], err = d.ReadShort(); err != nil {
			return fmt.Errorf("mcdriver: Set Entity Motion: %w", err)
		}
	}
	cl.mu.Lock()
	defer cl.mu.Unlock()
	if id != cl.login.EntityID {
		return nil
	}
	moveKey.of(cl).p.Vel = r3.Vec{X: float64(v[0]) / 8000, Y: float64(v[1]) / 8000, Z: float64(v[2]) / 8000}
	return nil
}

// SetControls は移動の操作を設定する。次のティックから効く。
func (cl *Client) SetControls(c Controls) {
	cl.mu.Lock()
	defer cl.mu.Unlock()
	moveKey.of(cl).ctl = c
}

// Controls は今の移動の操作を返す。
func (cl *Client) Controls() Controls {
	cl.mu.Lock()
	defer cl.mu.Unlock()
	return moveKey.of(cl).ctl
}

// Look は向きを変える（度）。yaw は 0 が南（+z）、90 が西（-x）、-90 が東（+x）、180 が北（-z）。
// pitch は 0 が水平、90 が真下、-90 が真上（-90〜90 に収める）。次のティックでサーバーに送る。
func (cl *Client) Look(yaw, pitch float32) {
	cl.mu.Lock()
	defer cl.mu.Unlock()
	cl.yaw = yaw
	cl.pitch = max(-90, min(90, pitch))
}

// LookAt は目の位置から p を向く。目の高さは姿勢で変わる（立って 1.62、しゃがんで 1.27、泳いで 0.4）。
func (cl *Client) LookAt(p r3.Vec) {
	cl.mu.Lock()
	defer cl.mu.Unlock()
	dx := p.X - cl.x
	dy := p.Y - (cl.y + float64(moveKey.of(cl).p.EyeHeight()))
	dz := p.Z - cl.z
	h := math.Hypot(dx, dz)
	if h < 1e-9 && math.Abs(dy) < 1e-9 {
		return
	}
	if h >= 1e-9 {
		// 本家の LookControl と同じ式（atan2(dz, dx) - 90 度）
		cl.yaw = float32(math.Atan2(dz, dx)*180/math.Pi) - 90
	}
	cl.pitch = max(-90, min(90, float32(-math.Atan2(dy, h)*180/math.Pi)))
}

// Sprinting は今走っているかを返す。
func (cl *Client) Sprinting() bool {
	cl.mu.Lock()
	defer cl.mu.Unlock()
	return moveKey.of(cl).p.Sprinting
}

// Swimming は今泳いでいるかを返す。
func (cl *Client) Swimming() bool {
	cl.mu.Lock()
	defer cl.mu.Unlock()
	return moveKey.of(cl).p.Swimming
}

// Pose は今の姿勢（立つ・しゃがむ・泳ぐ）を返す。
func (cl *Client) Pose() Pose {
	cl.mu.Lock()
	defer cl.mu.Unlock()
	return moveKey.of(cl).p.Pose
}

// Pose は姿勢。当たり判定の高さと目の高さが変わる。
type Pose = physics.Pose

// 姿勢の値。
const (
	PoseStanding  = physics.PoseStanding
	PoseCrouching = physics.PoseCrouching
	PoseSwimming  = physics.PoseSwimming
)

// velocityLocked は今の速さ（1 ティックあたり）を返す。mu を持って呼ぶ。
func (cl *Client) velocityLocked() r3.Vec { return moveKey.of(cl).p.Vel }

// physWorld は受け取った地形を physics.World として読む。世界の鍵（読み）を持って使う。
type physWorld struct{ w *chunk.World }

var airState, _ = data.StateByID(0)

func (pw physWorld) Block(x, y, z int) (data.BlockState, bool) {
	if pw.w == nil {
		return data.BlockState{}, false
	}
	col := pw.w.Column(int32(x>>4), int32(z>>4))
	if col == nil {
		return data.BlockState{}, false
	}
	f := pw.w.Format
	if y < f.MinY || y >= f.MinY+f.Height {
		return airState, true
	}
	id, err := col.Block(x&15, y, z&15)
	if err != nil {
		return airState, true
	}
	s, ok := data.StateByID(data.StateID(id))
	if !ok {
		return airState, true
	}
	return s, true
}

// moveTickLocked は毎ティックの物理と送信をする。mu を持って、Play の段階で位置を受け取ったあとに呼ぶ。
func (cl *Client) moveTickLocked() error {
	ms := moveKey.of(cl)
	p := &ms.p
	// 位置と向きは Client の値が元（強制修正や Look が書き換える）
	p.Pos = r3.Vec{X: cl.x, Y: cl.y, Z: cl.z}
	p.Yaw, p.Pitch = cl.yaw, cl.pitch

	in := physics.Input{
		Forward: ms.ctl.Forward, Back: ms.ctl.Back, Left: ms.ctl.Left, Right: ms.ctl.Right,
		Jump: ms.ctl.Jump, Sprint: ms.ctl.Sprint, Sneak: ms.ctl.Sneak,
	}
	// 満腹度と飛べるか（走れるかに効く）。Set Health がまだ届いていない（HP も満腹度も 0）なら、本家の初めの値 20 とする
	ss := selfKey.of(cl)
	ss.mu.RLock()
	p.Food, p.MayFly = ss.s.Food, ss.s.Abilities.AllowFlying
	if ss.s.Health == 0 && ss.s.Food == 0 {
		p.Food = 20
	}
	ss.mu.RUnlock()
	ws := worldKey.of(cl)
	ws.mu.RLock()
	// 溶岩の流れの強さはディメンションの ultrawarm で変わる。公式のディメンションのうちネザーだけが ultrawarm
	p.UltraWarm = ws.dim.Name == "minecraft:the_nether"
	p.Tick(physWorld{ws.w}, in)
	ws.mu.RUnlock()
	cl.x, cl.y, cl.z = p.Pos.X, p.Pos.Y, p.Pos.Z
	cl.onGround = p.OnGround

	// Player Command（しゃがむキーが変わったときだけ。本家の sendShiftKeyState）
	if ms.ctl.Sneak != ms.sentShift {
		action := int32(playerCommandReleaseShiftKey)
		if ms.ctl.Sneak {
			action = playerCommandPressShiftKey
		}
		if err := cl.c.Send(data.PlayServerboundPlayerCommand, func(e *protocol.Encoder) error {
			e.WriteVarInt(cl.login.EntityID)
			e.WriteVarInt(action)
			e.WriteVarInt(0)
			return nil
		}); err != nil {
			return err
		}
		ms.sentShift = ms.ctl.Sneak
	}

	// Player Input（キーが変わったときだけ）
	if k := inputFlags(ms.ctl); k != ms.sentInput {
		if err := cl.c.Send(data.PlayServerboundPlayerInput, func(e *protocol.Encoder) error {
			e.WriteUByte(k)
			return nil
		}); err != nil {
			return err
		}
		ms.sentInput = k
	}
	return cl.sendMovementLocked()
}

// Player Input のフラグ（1.21.2 から）。
const (
	inputForward  uint8 = 0x01
	inputBackward uint8 = 0x02
	inputLeft     uint8 = 0x04
	inputRight    uint8 = 0x08
	inputJump     uint8 = 0x10
	inputShift    uint8 = 0x20
	inputSprint   uint8 = 0x40
)

func inputFlags(c Controls) uint8 {
	var k uint8
	for _, f := range []struct {
		on   bool
		flag uint8
	}{{c.Forward, inputForward}, {c.Back, inputBackward}, {c.Left, inputLeft}, {c.Right, inputRight}, {c.Jump, inputJump}, {c.Sneak, inputShift}, {c.Sprint, inputSprint}} {
		if f.on {
			k |= f.flag
		}
	}
	return k
}

// Player Command の動作（1.21.4）。
const (
	playerCommandPressShiftKey   = 0
	playerCommandReleaseShiftKey = 1
	playerCommandStartSprinting  = 3
	playerCommandStopSprinting   = 4
)

// sendMovementLocked は本家の LocalPlayer.sendPosition（sendIsSprintingIfNeeded を含む）。
func (cl *Client) sendMovementLocked() error {
	ms := moveKey.of(cl)
	p := &ms.p
	if p.Sprinting != ms.sentSprint {
		action := int32(playerCommandStopSprinting)
		if p.Sprinting {
			action = playerCommandStartSprinting
		}
		if err := cl.c.Send(data.PlayServerboundPlayerCommand, func(e *protocol.Encoder) error {
			e.WriteVarInt(cl.login.EntityID)
			e.WriteVarInt(action)
			e.WriteVarInt(0)
			return nil
		}); err != nil {
			return err
		}
		ms.sentSprint = p.Sprinting
	}

	dx, dy, dz := cl.x-ms.lastX, cl.y-ms.lastY, cl.z-ms.lastZ
	ms.positionReminder++
	movedPos := dx*dx+dy*dy+dz*dz > 2.0e-4*2.0e-4 || ms.positionReminder >= 20
	movedRot := cl.yaw != ms.lastYaw || cl.pitch != ms.lastPitch
	var flags uint8
	if p.OnGround {
		flags |= packets.MoveOnGround
	}
	if p.HorizontalCollision {
		flags |= packets.MovePushingAgainstWall
	}
	var err error
	switch {
	case movedPos && movedRot:
		err = cl.c.Send(data.PlayServerboundMovePlayerPosRot, func(e *protocol.Encoder) error {
			packets.WriteMovePlayerPosRot(e, packets.MovePlayerPosRot{
				X: cl.x, Y: cl.y, Z: cl.z, Yaw: cl.yaw, Pitch: cl.pitch, Flags: flags,
			})
			return nil
		})
	case movedPos:
		err = cl.c.Send(data.PlayServerboundMovePlayerPos, func(e *protocol.Encoder) error {
			e.WriteDouble(cl.x)
			e.WriteDouble(cl.y)
			e.WriteDouble(cl.z)
			e.WriteUByte(flags)
			return nil
		})
	case movedRot:
		err = cl.c.Send(data.PlayServerboundMovePlayerRot, func(e *protocol.Encoder) error {
			e.WriteFloat(cl.yaw)
			e.WriteFloat(cl.pitch)
			e.WriteUByte(flags)
			return nil
		})
	case p.OnGround != ms.lastOnGround || p.HorizontalCollision != ms.lastHorizontal:
		err = cl.c.Send(data.PlayServerboundMovePlayerStatusOnly, func(e *protocol.Encoder) error {
			e.WriteUByte(flags)
			return nil
		})
	}
	if err != nil {
		return err
	}
	if movedPos {
		ms.lastX, ms.lastY, ms.lastZ = cl.x, cl.y, cl.z
		ms.positionReminder = 0
	}
	if movedRot {
		ms.lastYaw, ms.lastPitch = cl.yaw, cl.pitch
	}
	ms.lastOnGround = p.OnGround
	ms.lastHorizontal = p.HorizontalCollision
	return nil
}

// moveOnTeleportLocked は位置の強制修正を物理の状態に当てはめる（本家の PositionMoveRotation.calculateAbsolute の速さの部分）。
// cl.x などはすでに新しい値。oldYaw と oldPitch は修正の前の向き。mu を持って呼ぶ。
// 本家と同じく、このあと送る位置は接地も横の衝突も偽にするので、最後に送った値もそれに合わせる。
func (cl *Client) moveOnTeleportLocked(pp packets.PlayerPosition, oldYaw, oldPitch float32) {
	ms := moveKey.of(cl)
	v := ms.p.Vel
	if pp.Flags&packets.RotateVelocity != 0 {
		// 向きの変わった分だけ、今の速さを回す（Vec3.xRot のあと Vec3.yRot）
		a := float64(oldPitch-cl.pitch) * math.Pi / 180
		c, s := math.Cos(a), math.Sin(a)
		v = r3.Vec{X: v.X, Y: v.Y*c + v.Z*s, Z: v.Z*c - v.Y*s}
		b := float64(oldYaw-cl.yaw) * math.Pi / 180
		c, s = math.Cos(b), math.Sin(b)
		v = r3.Vec{X: v.X*c + v.Z*s, Y: v.Y, Z: v.Z*c - v.X*s}
	}
	rel := func(flag int32, cur, add float64) float64 {
		if pp.Flags&flag != 0 {
			return cur + add
		}
		return add
	}
	v = r3.Vec{X: rel(packets.RelVelX, v.X, pp.VelX), Y: rel(packets.RelVelY, v.Y, pp.VelY), Z: rel(packets.RelVelZ, v.Z, pp.VelZ)}
	ms.p.SetTeleport(r3.Vec{X: cl.x, Y: cl.y, Z: cl.z}, v, cl.yaw, cl.pitch)
	ms.lastX, ms.lastY, ms.lastZ = cl.x, cl.y, cl.z
	ms.lastYaw, ms.lastPitch = cl.yaw, cl.pitch
	ms.lastOnGround, ms.lastHorizontal = false, false
	ms.positionReminder = 0
}
