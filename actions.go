package mcdriver

// 行動: 掘る（Dig）、置く（Place）、ブロックを使う（UseBlock）、アイテムを使う（StartUseItem / StopUseItem）、
// 攻撃する（Attack / AttackCharge）。
//
// どの行動も、本家のクライアントと同じく毎ティックの処理（client.go の tick）の中で送る。
// 1 ティックの中では、物理と位置・向きの送信（controls.go）のあとに行動のパケットを送る。
// 行動の前に向きを変えるので、変えた向きは同じティックの位置の送信でサーバーに届き、そのあとに行動が届く。
//
// ブロックに対する行動（掘る、置く、使う）は、本家の BlockStatePredictionHandler と同じくシーケンス番号を付けて送る。
// サーバーはパケットを処理したあと、そのティックの終わりに Acknowledge Block Change でその番号を知らせる。
// 変わったブロックの Block Update はそれより前に届くので、知らせが届いたら地形（Block）を見て結果を確かめる。
// このドライバは本家のような予測（先に手元の地形を変える）はしない。
//
// 受け取るパケット: Acknowledge Block Change、Update Attributes（自分の属性: 届く距離、攻撃速度、採掘の速さなど）、
// Entity Effect / Remove Entity Effect（採掘速度上昇・低下）、Respawn。

import (
	"context"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/Satoru314/go-mcdriver/data"
	"github.com/Satoru314/go-mcdriver/internal/packets"
	"github.com/Satoru314/go-mcdriver/internal/physics"
	"github.com/Satoru314/go-mcdriver/internal/protocol"
	"gonum.org/v1/gonum/spatial/r3"
)

var (
	// ErrOutOfReach は、目からの距離が届く距離（属性 block_interaction_range / entity_interaction_range）より遠いときのエラー。
	ErrOutOfReach = errors.New("mcdriver: 届かない（遠すぎる）")
	// ErrNotLoaded は、その場所のチャンクを受け取っていないときのエラー。
	ErrNotLoaded = errors.New("mcdriver: その場所の地形をまだ受け取っていない")
	// ErrNotDiggable は、掘れないブロック（空気、水、岩盤など）を掘ろうとしたときのエラー。
	ErrNotDiggable = errors.New("mcdriver: 掘れないブロック")
	// ErrCannotDig は、今のゲームモード（アドベンチャー、スペクテイター）では掘れないときのエラー。
	ErrCannotDig = errors.New("mcdriver: 今のゲームモードでは掘れない")
	// ErrBlockChanged は、掘っているあいだにブロックが（自分以外の理由で）変わったときのエラー。
	ErrBlockChanged = errors.New("mcdriver: 掘っているあいだにブロックが変わった")
	// ErrNotBroken は、掘り終わりを送ったのにサーバーでブロックが壊れなかったときのエラー。
	ErrNotBroken = errors.New("mcdriver: サーバーでブロックが壊れなかった")
	// ErrNotPlaced は、置こうとしたのにサーバーでブロックが変わらなかったときのエラー。
	ErrNotPlaced = errors.New("mcdriver: サーバーでブロックが置かれなかった")
	// ErrNoScreen は、容器を使ったのにサーバーから窓（Open Screen）が届かなかったときのエラー。
	ErrNoScreen = errors.New("mcdriver: 容器の窓が開かなかった")
	// ErrNoAck は、ブロックに対する行動のあと、サーバーから確認（Acknowledge Block Change）が届かなかったときのエラー。
	ErrNoAck = errors.New("mcdriver: サーバーから確認（Acknowledge Block Change）が届かない")
	// ErrBusy は、ほかの Dig がまだ終わっていないときのエラー。
	ErrBusy = errors.New("mcdriver: ほかの Dig が終わっていない")
	// ErrNotSpawned は、位置をまだ受け取っていない（ログインやリスポーンの直後）ため、行動を送れないまま時間が過ぎたときのエラー。
	ErrNotSpawned = errors.New("mcdriver: 位置をまだ受け取っていないので行動を送れない")
	// ErrNoEntity は、知らないエンティティ ID を攻撃しようとしたときのエラー。
	ErrNoEntity = errors.New("mcdriver: そのエンティティを知らない")
)

// 時間の上限（ctx がもっと短ければそちら）。
const (
	// ackTimeout は、ブロックに対する行動を送ってから確認を待つ長さ
	ackTimeout = 5 * time.Second
	// resultGrace は、確認が届いたあと、結果（ブロックの変化、容器の窓）を待つ長さ。
	// 公式のサーバーは確認より前に送るが、念のため少し待つ
	resultGrace = time.Second
)

// digDelayTicks は、掘り終わり（とクリエイティブで壊したとき）から次に掘り始めるまでのティック数
// （本家 MultiPlayerGameMode の destroyDelay = 5）。
const digDelayTicks = 5

// ---- 属性 ----

// attrDef は属性の既定値と範囲（本家の Attributes の RangedAttribute）。
type attrDef struct {
	name          string
	def, min, max float64
}

// 使う属性。
var (
	attrAttackSpeed     = attrDef{"attack_speed", 4.0, 0, 1024}
	attrBlockRange      = attrDef{"block_interaction_range", 4.5, 0, 64}
	attrEntityRange     = attrDef{"entity_interaction_range", 3.0, 0, 64}
	attrBlockBreakSpeed = attrDef{"block_break_speed", 1.0, 0, 1024}
	attrMiningEff       = attrDef{"mining_efficiency", 0, 0, 1024}
	attrSubmergedMining = attrDef{"submerged_mining_speed", 0.2, 0, 20}
)

// attrValue は属性の値を計算する（本家の AttributeInstance.calculateValue と同じ）。
// a が nil（届いていない）なら既定値。
func (def attrDef) value(a *packets.Attribute) float64 {
	if a == nil {
		return def.def
	}
	v := a.Base
	for _, m := range a.Modifiers {
		if m.Operation == packets.ModifierAddValue {
			v += m.Amount
		}
	}
	t := v
	for _, m := range a.Modifiers {
		if m.Operation == packets.ModifierAddMultipliedBase {
			t += v * m.Amount
		}
	}
	for _, m := range a.Modifiers {
		if m.Operation == packets.ModifierAddMultipliedTotal {
			t *= 1 + m.Amount
		}
	}
	if math.IsNaN(t) {
		return def.min
	}
	return max(def.min, min(def.max, t))
}

// registryIndex は登録簿 reg の名前 → 番号。
func registryIndex(reg string) map[string]int32 {
	m := map[string]int32{}
	if r, ok := data.RegistryByName(reg); ok {
		for i, n := range r.Entries {
			m[n] = int32(i)
		}
	}
	return m
}

var (
	attributeIDs = registryIndex("attribute")
	mobEffectIDs = registryIndex("mob_effect")
)

// ---- 状態 ----

// actState は行動の状態。Client の mu で守る（毎ティックの処理と位置・向きと一緒に扱うため）。
type actState struct {
	// seq は最後に使ったシーケンス番号（本家の BlockStatePredictionHandler.currentSequenceNr。1 から）
	seq int32
	// acked はサーバーが Acknowledge Block Change で知らせた一番大きい番号
	acked int32
	// ackCh は確認が届くたびに閉じて作り直す
	ackCh chan struct{}

	// queue は次のティックで、位置の送信のあとに行うこと
	queue []*actTask
	// dig は掘っている途中のもの（無ければ nil）
	dig *digJob
	// digDelay は次に掘り始められるまでのティック数
	digDelay int

	// attackTicks は最後に攻撃した・手に持つ物が変わってからのティック数（本家の attackStrengthTicker）
	attackTicks int
	// lastHeld は前のティックで手に持っていた物（素手は nil）。heldKnown が false ならまだ見ていない
	lastHeld  *data.Item
	heldKnown bool

	// attrs は自分の属性（Update Attributes。minecraft:attribute の番号 → 最後に届いた値）
	attrs map[int32]packets.Attribute
	// effects は自分にかかっている効果（minecraft:mob_effect の番号 → 強さ amplifier）
	effects map[int32]int32

	// using はアイテムを使っているか（本家の LocalPlayer.startedUsingItem）。物理の Input.UsingItem になる
	using bool
	// useItem は使っている物（使い始めたときに手に持っていた物）。手に持つ物が変わったらやめる
	useItem *data.Item
	// useUnconfirmed は、StartUseItem で使い始めたとみなしてから、サーバーの「使っている」の印
	// （自分の Set Entity Metadata の LivingEntity の印 0x01）がまだ届いていないティックの数。-1 なら届いた
	useUnconfirmed int
}

var actKey = newFeature(func(*Client) *actState {
	return &actState{
		ackCh:   make(chan struct{}),
		attrs:   map[int32]packets.Attribute{},
		effects: map[int32]int32{},
	}
})

func init() {
	onPlay(data.PlayClientboundBlockChangedAck, (*Client).actOnAck)
	onPlay(data.PlayClientboundUpdateAttributes, (*Client).actOnAttributes)
	onPlay(data.PlayClientboundUpdateMobEffect, (*Client).actOnEffect)
	onPlay(data.PlayClientboundRemoveMobEffect, (*Client).actOnRemoveEffect)
	onPlay(data.PlayClientboundRespawn, (*Client).actOnRespawn)
	onPlay(data.PlayClientboundSetEntityData, (*Client).actOnMetadata)
}

// attrLocked は自分の属性 def の今の値を返す。cl.mu を持って呼ぶ。
func (cl *Client) attrLocked(def attrDef) float64 {
	as := actKey.of(cl)
	id, ok := attributeIDs[def.name]
	if !ok {
		return def.def
	}
	a, ok := as.attrs[id]
	if !ok {
		return def.value(nil)
	}
	return def.value(&a)
}

// effectLocked は効果 name の強さ（amplifier）を返す。かかっていなければ ok は false。cl.mu を持って呼ぶ。
func (cl *Client) effectLocked(name string) (amp int32, ok bool) {
	id, known := mobEffectIDs[name]
	if !known {
		return 0, false
	}
	amp, ok = actKey.of(cl).effects[id]
	return amp, ok
}

// ---- 受け取る ----

func (cl *Client) actOnAck(d *protocol.Decoder) error {
	seq, err := d.ReadVarInt()
	if err != nil {
		return fmt.Errorf("mcdriver: Acknowledge Block Change: %w", err)
	}
	cl.mu.Lock()
	defer cl.mu.Unlock()
	as := actKey.of(cl)
	if seq > as.acked {
		as.acked = seq
	}
	close(as.ackCh)
	as.ackCh = make(chan struct{})
	return nil
}

func (cl *Client) actOnAttributes(d *protocol.Decoder) error {
	p, err := packets.ReadUpdateAttributes(d)
	if err != nil {
		return fmt.Errorf("mcdriver: Update Attributes: %w", err)
	}
	cl.mu.Lock()
	defer cl.mu.Unlock()
	if p.EntityID != cl.login.EntityID {
		return nil
	}
	as := actKey.of(cl)
	// 本家の handleUpdateAttributes と同じく、届いた属性ごとに元の値と修飾を丸ごと置き換える
	for _, a := range p.Attributes {
		as.attrs[a.ID] = a
	}
	return nil
}

func (cl *Client) actOnEffect(d *protocol.Decoder) error {
	p, err := packets.ReadMobEffect(d)
	if err != nil {
		return fmt.Errorf("mcdriver: Entity Effect: %w", err)
	}
	cl.mu.Lock()
	defer cl.mu.Unlock()
	if p.EntityID == cl.login.EntityID {
		actKey.of(cl).effects[p.EffectID] = p.Amplifier
	}
	return nil
}

func (cl *Client) actOnRemoveEffect(d *protocol.Decoder) error {
	ent, err := d.ReadVarInt()
	if err != nil {
		return fmt.Errorf("mcdriver: Remove Entity Effect: %w", err)
	}
	eff, err := d.ReadVarInt()
	if err != nil {
		return fmt.Errorf("mcdriver: Remove Entity Effect: %w", err)
	}
	cl.mu.Lock()
	defer cl.mu.Unlock()
	if ent == cl.login.EntityID {
		delete(actKey.of(cl).effects, eff)
	}
	return nil
}

// actOnRespawn は、リスポーンやディメンションの移動で、本家と同じくプレイヤーを作り直した扱いにする。
// 効果は消え（サーバーが送り直す）、属性は「属性を残す」の印が無ければ既定値に戻る。
func (cl *Client) actOnRespawn(d *protocol.Decoder) error {
	rp, err := packets.ReadRespawn(d)
	if err != nil {
		return fmt.Errorf("mcdriver: Respawn: %w", err)
	}
	cl.mu.Lock()
	defer cl.mu.Unlock()
	as := actKey.of(cl)
	clear(as.effects)
	if rp.DataKept&1 == 0 {
		clear(as.attrs)
	}
	as.attackTicks = 0
	as.stopUsingLocked()
	return nil
}

// ---- 毎ティックの処理 ----

// actTask は次のティックで行うこと 1 つ。
type actTask struct {
	run      func(cl *Client) error
	done     chan error // 容量 1。run の結果
	canceled bool       // 待つのをやめた（実行しない）
}

// actHeld は手に持っている物（素手は nil）を返す。毎ティックの処理の前に、cl.mu を持たずに呼ぶ
// （持ち物の鍵 → cl.mu の順でしか鍵を重ねないため）。
func (cl *Client) actHeld() *data.Item {
	st := invKey.of(cl)
	st.mu.Lock()
	defer st.mu.Unlock()
	s := st.slots[SlotHotbar+st.selected]
	if s.Empty() {
		return nil
	}
	it, ok := data.ItemByID(s.Item)
	if !ok {
		return nil
	}
	return it
}

// actTickLocked は毎ティックの行動の処理。位置と向きを送ったあとに、cl.mu を持って呼ぶ。
// held はこのティックの初めに手に持っていた物。
func (cl *Client) actTickLocked(held *data.Item) error {
	as := actKey.of(cl)
	// 攻撃の溜め（本家の Player.tick: 手に持つ物の種類が変わったら溜め直し、毎ティック 1 進む）
	if as.heldKnown && held != as.lastHeld {
		as.attackTicks = 0
	}
	as.lastHeld, as.heldKnown = held, true
	as.attackTicks++

	q := as.queue
	as.queue = nil
	for _, t := range q {
		if t.canceled {
			continue
		}
		t.done <- t.run(cl)
	}
	if as.dig != nil {
		cl.digTickLocked(held)
	} else if as.digDelay > 0 {
		as.digDelay--
	}
	return nil
}

// nextTick は、次のティックで位置と向きを送ったあとに run を実行し、その結果を返す。
// setup は今すぐ（cl.mu を持って）呼ぶ（向きを変えるなど。変えた向きは run より前に送られる）。
func (cl *Client) nextTick(ctx context.Context, setup func(), run func(cl *Client) error) error {
	t := &actTask{run: run, done: make(chan error, 1)}
	cl.mu.Lock()
	if cl.state != data.StatePlay {
		cl.mu.Unlock()
		return ErrNotPlaying
	}
	if setup != nil {
		setup()
	}
	as := actKey.of(cl)
	as.queue = append(as.queue, t)
	cl.mu.Unlock()
	select {
	case err := <-t.done:
		return cl.sendErr(err)
	case <-ctx.Done():
	case <-cl.done:
	}
	cl.mu.Lock()
	defer cl.mu.Unlock()
	select {
	case err := <-t.done:
		return cl.sendErr(err)
	default:
	}
	t.canceled = true
	if ctx.Err() != nil {
		return context.Cause(ctx)
	}
	return cl.closedErr()
}

// closedErr は接続が終わっているときのエラー。
func (cl *Client) closedErr() error {
	return fmt.Errorf("mcdriver: 接続が終わった: %w", cl.Err())
}

// nextSeqLocked は新しいシーケンス番号を返す。cl.mu を持って呼ぶ。
func (cl *Client) nextSeqLocked() int32 {
	as := actKey.of(cl)
	as.seq++
	return as.seq
}

// waitAck はシーケンス番号 seq の確認が届くまで待つ。
func (cl *Client) waitAck(ctx context.Context, seq int32) error {
	timer := time.NewTimer(ackTimeout)
	defer timer.Stop()
	for {
		cl.mu.Lock()
		as := actKey.of(cl)
		if as.acked >= seq {
			cl.mu.Unlock()
			return nil
		}
		ch := as.ackCh
		cl.mu.Unlock()
		select {
		case <-ch:
		case <-ctx.Done():
			return context.Cause(ctx)
		case <-cl.done:
			return cl.closedErr()
		case <-timer.C:
			return fmt.Errorf("%w（シーケンス番号 %d）", ErrNoAck, seq)
		}
	}
}

// waitFor は cond が真になるまで（最長 d）待つ。真になれば true。
func (cl *Client) waitFor(ctx context.Context, d time.Duration, cond func() bool) (bool, error) {
	end := time.Now().Add(d)
	for {
		if cond() {
			return true, nil
		}
		if time.Now().After(end) {
			return false, nil
		}
		select {
		case <-ctx.Done():
			return false, context.Cause(ctx)
		case <-cl.done:
			return false, cl.closedErr()
		case <-time.After(10 * time.Millisecond):
		}
	}
}

// ---- 送る ----

// swingLocked は腕を振る（Swing Arm、利き手）。cl.mu を持って呼ぶ。
func (cl *Client) swingLocked() error {
	return cl.c.Send(data.PlayServerboundSwing, func(e *protocol.Encoder) error {
		e.WriteVarInt(packets.HandMain)
		return nil
	})
}

func (cl *Client) sendPlayerActionLocked(status int32, p BlockPos, f Face, seq int32) error {
	return cl.c.Send(data.PlayServerboundPlayerAction, func(e *protocol.Encoder) error {
		return packets.WritePlayerAction(e, packets.PlayerAction{
			Status: status, Pos: protocol.Position{X: p.X, Y: p.Y, Z: p.Z}, Face: int8(f), Sequence: seq,
		})
	})
}

// eyeLocked は目の位置を返す。cl.mu を持って呼ぶ。
func (cl *Client) eyeLocked() r3.Vec {
	return r3.Vec{X: cl.x, Y: cl.y + float64(physics.EyeHeight), Z: cl.z}
}

// aimLocked は目の位置から p を向く（controls.go の lookAtLocked。yaw は -180 以上 180 未満）。cl.mu を持って呼ぶ。
func (cl *Client) aimLocked(p r3.Vec) {
	cl.lookAtLocked(cl.eyeLocked(), p)
}

// faceCenter はブロック p の面 f の真ん中の位置を返す。
func faceCenter(p BlockPos, f Face) r3.Vec {
	return r3.Add(p.Center(), r3.Scale(0.5, f.Normal()))
}

// boxDistance は点 v から箱 b までの距離（中なら 0）。
func boxDistance(v r3.Vec, b r3.Box) float64 {
	d := func(x, lo, hi float64) float64 { return max(lo-x, 0, x-hi) }
	return math.Sqrt(math.Pow(d(v.X, b.Min.X, b.Max.X), 2) + math.Pow(d(v.Y, b.Min.Y, b.Max.Y), 2) + math.Pow(d(v.Z, b.Min.Z, b.Max.Z), 2))
}

// blockLocked は地形のブロックを読む。cl.mu を持って呼んでよい（世界の鍵は cl.mu のあとに取る）。
func (cl *Client) blockLocked(p BlockPos) (data.BlockState, bool) {
	return cl.Block(p)
}

// ---- 掘る ----

// digJob は掘っている途中のもの。cl.mu で守る。
type digJob struct {
	pos      BlockPos
	face     Face
	aim      r3.Vec
	orig     data.BlockState
	creative bool

	started  bool
	progress float32    // 本家の destroyProgress
	tool     *data.Item // 掘り始めたときに持っていた物
	finalSeq int32      // 最後に送ったパケット（掘り終わり、または一瞬で壊れた掘り始め）の番号
	err      error
	done     chan struct{} // 最後のパケットを送ったか、失敗したら閉じる
}

func (j *digJob) finish(err error) {
	j.err = err
	close(j.done)
}

// Dig はブロック p を面 f の側から掘る。サーバーでブロックが壊れたことを確かめてから nil を返す。
//
// 本家のクライアント（MultiPlayerGameMode.startDestroyBlock / continueDestroyBlock）と同じく、
// 面の真ん中を向き、Player Action（掘り始め）を送り、毎ティック掘る進み（destroyProgress）を足して腕を振り、
// 進みが 1 以上になったら Player Action（掘り終わり）を送る。進みが掘り始めで 1 以上ならそれだけで壊れる。
// 掘る速さの計算は DigTicks を見よ。
//
// 届く距離（属性 block_interaction_range。ふつう 4.5）より遠い、掘っているあいだにブロックが変わった、
// 届かなくなったときはエラー。ctx が終わったら掘るのをやめ（Player Action の中止を送る）、ctx のエラーを返す。
// 同時に掘れるのは 1 つだけ。
func (cl *Client) Dig(ctx context.Context, p BlockPos, f Face) error {
	if !f.Valid() {
		return fmt.Errorf("mcdriver: 正しくない面 %d", int(f))
	}
	s, ok := cl.Block(p)
	if !ok {
		return ErrNotLoaded
	}
	if !s.Block.Diggable {
		return fmt.Errorf("%w: %v", ErrNotDiggable, s)
	}
	mode := cl.Self().GameMode
	if mode == Adventure || mode == Spectator {
		return ErrCannotDig
	}
	job := &digJob{pos: p, face: f, aim: faceCenter(p, f), orig: s, creative: mode == Creative, done: make(chan struct{})}

	cl.mu.Lock()
	if cl.state != data.StatePlay {
		cl.mu.Unlock()
		return ErrNotPlaying
	}
	as := actKey.of(cl)
	if as.dig != nil {
		cl.mu.Unlock()
		return ErrBusy
	}
	if d := r3.Norm(r3.Sub(job.aim, cl.eyeLocked())); d > cl.attrLocked(attrBlockRange) {
		cl.mu.Unlock()
		return fmt.Errorf("%w: %v の面 %v まで %.2f", ErrOutOfReach, p, f, d)
	}
	cl.aimLocked(job.aim)
	as.dig = job
	cl.mu.Unlock()

	select {
	case <-job.done:
	case <-ctx.Done():
		cl.abortDig(job)
		return context.Cause(ctx)
	case <-cl.done:
		return cl.closedErr()
	}
	if job.err != nil {
		return cl.sendErr(job.err)
	}
	if err := cl.waitAck(ctx, job.finalSeq); err != nil {
		return err
	}
	var last data.BlockState
	broken, err := cl.waitFor(ctx, resultGrace, func() bool {
		last, ok = cl.Block(p)
		return ok && last.ID != s.ID
	})
	if err != nil {
		return err
	}
	if !broken {
		return fmt.Errorf("%w: %v は %v のまま", ErrNotBroken, p, last)
	}
	return nil
}

// abortDig は、まだ終わっていない job をやめる（掘り始めを送っていれば中止を送る）。
func (cl *Client) abortDig(job *digJob) {
	cl.mu.Lock()
	defer cl.mu.Unlock()
	as := actKey.of(cl)
	if as.dig != job {
		return
	}
	as.dig = nil
	if job.started && cl.state == data.StatePlay {
		// 本家の stopDestroyBlock と同じく、面は下、シーケンス番号は 0
		cl.sendPlayerActionLocked(packets.ActionAbortDestroy, job.pos, Down, 0)
	}
	job.finish(context.Canceled)
}

// digTickLocked は掘っている途中のものを 1 ティック進める。cl.mu を持って呼ぶ。
func (cl *Client) digTickLocked(held *data.Item) {
	as := actKey.of(cl)
	job := as.dig
	fail := func(err error) {
		if job.started {
			cl.sendPlayerActionLocked(packets.ActionAbortDestroy, job.pos, Down, 0)
		}
		as.dig = nil
		job.finish(err)
	}
	if as.digDelay > 0 {
		as.digDelay--
		return
	}
	cur, ok := cl.blockLocked(job.pos)
	if !ok || cur.ID != job.orig.ID {
		fail(fmt.Errorf("%w: %v が %v から %v に", ErrBlockChanged, job.pos, job.orig, cur))
		return
	}
	if d := r3.Norm(r3.Sub(job.aim, cl.eyeLocked())); d > cl.attrLocked(attrBlockRange) {
		fail(fmt.Errorf("%w: 掘っているあいだに %v の面 %v まで %.2f になった", ErrOutOfReach, job.pos, job.face, d))
		return
	}
	// 掘っているあいだは向き続ける（本家は照準がブロックから外れると掘るのをやめる）
	cl.aimLocked(job.aim)

	send := func(status int32) (int32, bool) {
		seq := cl.nextSeqLocked()
		if err := cl.sendPlayerActionLocked(status, job.pos, job.face, seq); err != nil {
			as.dig = nil
			job.finish(err)
			return 0, false
		}
		return seq, true
	}
	done := func(seq int32) {
		job.finalSeq = seq
		as.dig = nil
		job.finish(nil)
	}

	if job.started && held != job.tool {
		// 本家は持つ物が変わると、前のを中止して掘り始めからやり直す
		cl.sendPlayerActionLocked(packets.ActionAbortDestroy, job.pos, Down, 0)
		job.started = false
	}
	step := cl.destroyProgressLocked(job.orig, held)

	if !job.started {
		seq, ok := send(packets.ActionStartDestroy)
		if !ok {
			return
		}
		cl.swingLocked()
		if job.creative {
			as.digDelay = digDelayTicks
			done(seq)
			return
		}
		if step >= 1 {
			// 一瞬で壊れる（本家は掘り終わりを送らない。次を掘るまでの待ちも無い）
			done(seq)
			return
		}
		job.started, job.tool = true, held
		// 本家は掘り始めと同じティックで continueDestroyBlock も呼ぶので、進みが 1 回分足される
		job.progress = step
		return
	}
	job.progress += step
	cl.swingLocked()
	if job.progress >= 1 {
		seq, ok := send(packets.ActionStopDestroy)
		if !ok {
			return
		}
		as.digDelay = digDelayTicks
		done(seq)
	}
}

// DigTicks は、今の状態（手に持っている物、地面に立っているか、水中か、効果、属性）でブロック p を掘ると
// 何ティックかかるかを返す（本家と同じ計算。一瞬で壊れるなら 1）。掘れないブロックや、
// 今の状態では進まないなら ok は false。
func (cl *Client) DigTicks(p BlockPos) (ticks int, ok bool) {
	s, ok := cl.Block(p)
	if !ok || !s.Block.Diggable {
		return 0, false
	}
	held := cl.actHeld()
	cl.mu.Lock()
	step := cl.destroyProgressLocked(s, held)
	cl.mu.Unlock()
	return digTicksFor(step)
}

// digTicksFor は 1 ティックの進み step で、進みの合計が 1 以上になるまでのティック数（掘り始めのティックを含む）。
// 本家と同じく float32 で足していく。
func digTicksFor(step float32) (int, bool) {
	if step >= 1 {
		return 1, true
	}
	if !(step > 0) {
		return 0, false
	}
	var sum float32
	n := 0
	for sum < 1 {
		sum += step
		n++
		if n > 1_000_000 {
			return 0, false
		}
	}
	return n, true
}

// digEnv は掘る速さに効く自分の状態。
type digEnv struct {
	held       *data.Item // 手に持っている物。素手は nil
	onGround   bool
	eyeInWater bool
	efficiency float32 // 属性 mining_efficiency（効率強化のエンチャントで増える）
	breakSpeed float32 // 属性 block_break_speed
	submerged  float32 // 属性 submerged_mining_speed（水中採掘で増える）
	haste      int32   // 採掘速度上昇（haste と conduit_power の強さの大きい方）。無ければ -1
	fatigue    int32   // 採掘速度低下（mining_fatigue）の強さ。無ければ -1
}

// destroyProgressLocked は今の状態で s を 1 ティック掘ったときの進みを返す。cl.mu を持って呼ぶ。
func (cl *Client) destroyProgressLocked(s data.BlockState, held *data.Item) float32 {
	env := digEnv{
		held:       held,
		onGround:   cl.onGround,
		eyeInWater: cl.eyeInWaterLocked(),
		efficiency: float32(cl.attrLocked(attrMiningEff)),
		breakSpeed: float32(cl.attrLocked(attrBlockBreakSpeed)),
		submerged:  float32(cl.attrLocked(attrSubmergedMining)),
		haste:      -1,
		fatigue:    -1,
	}
	if a, ok := cl.effectLocked("haste"); ok {
		env.haste = a
	}
	if a, ok := cl.effectLocked("conduit_power"); ok && a > env.haste {
		env.haste = a
	}
	if a, ok := cl.effectLocked("mining_fatigue"); ok {
		env.fatigue = a
	}
	return destroyProgress(s, env)
}

// destroyProgress は 1 ティックの掘る進み（本家 BlockBehaviour.getDestroyProgress）。
//
//	硬さが -1（壊せない）なら 0
//	進み = 掘る速さ / 硬さ / （適した道具なら 30、でなければ 100）
//
// 「適した道具」は本家の Player.hasCorrectToolForDrops: ブロックが道具を必要としない（data の HarvestTools が nil）か、
// 手に持っている道具の規則（data の Tool.CorrectForDrops）が当てはまる。硬さ 0 のブロックは進みが無限大（一瞬で壊れる）。
func destroyProgress(s data.BlockState, env digEnv) float32 {
	h := s.Block.Hardness
	if h == -1 {
		return 0
	}
	div := float32(100)
	if hasCorrectToolForDrops(s.Block, env.held) {
		div = 30
	}
	return playerDestroySpeed(s.Block, env) / h / div
}

// hasCorrectToolForDrops は本家の Player.hasCorrectToolForDrops。
func hasCorrectToolForDrops(b *data.Block, held *data.Item) bool {
	if !b.NeedsTool() {
		return true
	}
	return held != nil && held.Tool != nil && held.Tool.CorrectForDrops(b.ID)
}

// playerDestroySpeed は掘る速さ（本家 1.21.4 の Player.getDestroySpeed）。
//
//	f = 道具の速さ（Tool の規則で速さを持つ最初の当てはまるもの。無ければ default_mining_speed（既定 1）。道具でなければ 1）
//	f > 1 なら f += 属性 mining_efficiency（効率強化 n で n²+1）
//	採掘速度上昇（haste か conduit_power）の強さ a なら f *= 1 + (a+1)×0.2
//	採掘速度低下の強さ 0, 1, 2, 3 以上で f *= 0.3, 0.09, 0.0027, 8.1E-4
//	f *= 属性 block_break_speed
//	目が水の中なら f *= 属性 submerged_mining_speed（既定 0.2。水中採掘で 1）
//	地面に立っていなければ f /= 5
func playerDestroySpeed(b *data.Block, env digEnv) float32 {
	f := float32(1)
	if t := heldTool(env.held); t != nil {
		if sp, ok := t.MiningSpeed(b.ID); ok {
			f = sp
		} else if t.DefaultMiningSpeed != 0 {
			f = t.DefaultMiningSpeed
		}
	}
	if f > 1 {
		f += env.efficiency
	}
	if env.haste >= 0 {
		f *= 1 + float32(env.haste+1)*0.2
	}
	if env.fatigue >= 0 {
		switch env.fatigue {
		case 0:
			f *= 0.3
		case 1:
			f *= 0.09
		case 2:
			f *= 0.0027
		default:
			f *= 8.1e-4
		}
	}
	f *= env.breakSpeed
	if env.eyeInWater {
		f *= env.submerged
	}
	if !env.onGround {
		f /= 5
	}
	return f
}

func heldTool(it *data.Item) *data.Tool {
	if it == nil {
		return nil
	}
	return it.Tool
}

// eyeInWaterLocked は目が水の中かを返す（本家 Entity.updateFluidOnEyes: 目の高さ - 0.11111111 の位置の
// ブロックの水の高さより低いか）。cl.mu を持って呼ぶ。
func (cl *Client) eyeInWaterLocked() bool {
	y := cl.y + float64(physics.EyeHeight) - float64(float32(0.11111111))
	p := BlockPosOf(r3.Vec{X: cl.x, Y: y, Z: cl.z})
	s, ok := cl.blockLocked(p)
	if !ok {
		return false
	}
	above, _ := cl.blockLocked(p.Offset(0, 1, 0))
	h, ok := waterHeight(s, above)
	return ok && float64(p.Y)+h > y
}

// waterHeight は、水を含むブロック s の水の高さ（0〜1）を返す（本家 FlowingFluid.getHeight）。
// 上も水なら 1、そうでなければ 量/9（水源と落ちる水は 8/9、流れる水 level n は (8-n)/9）。水を含まなければ ok は false。
func waterHeight(s, above data.BlockState) (float64, bool) {
	amount, ok := waterAmount(s)
	if !ok {
		return 0, false
	}
	if above.Block != nil {
		if _, w := waterAmount(above); w {
			return 1, true
		}
	}
	return float64(float32(amount) / 9), true
}

// waterAmount は水の量（1〜8）を返す。水を含まなければ ok は false。
func waterAmount(s data.BlockState) (int, bool) {
	switch s.Block.Name {
	case "water":
		lv, _ := s.Get("level")
		n := 0
		fmt.Sscan(lv, &n)
		if n == 0 || n >= 8 {
			return 8, true
		}
		return 8 - n, true
	case "bubble_column", "kelp", "kelp_plant", "seagrass", "tall_seagrass":
		return 8, true
	}
	if v, ok := s.Get("waterlogged"); ok && v == "true" {
		return 8, true
	}
	return 0, false
}

// ---- 置く・使う ----

// PlaceOpts は Place の指定。
type PlaceOpts struct {
	// Yaw は置く瞬間に向く横の向き（度。0 が南、90 が西、180 が北、-90 が東）。階段やドアなど、
	// 置く人の向きで向きが決まるブロックに効く。nil なら指す位置を向いたまま
	Yaw *float64
	// Cursor は面の上の指す位置（against の北西の下の角から、x, y, z それぞれ 0〜1）。
	// 階段やハーフブロックの上下（横の面の上半分を指すと上付き）に効く。nil なら面の真ん中
	Cursor *r3.Vec
}

// Placed は Place で置けたブロック。
type Placed struct {
	// Pos は置かれた位置（ふつうは against の面 f の側の隣。against が草などの置き換えられるブロックや、
	// ハーフブロックを重ねて 2 段にしたときは against そのもの）
	Pos BlockPos
	// State は置かれたブロックの状態（サーバーから届いたもの）
	State data.BlockState
}

// Place は手に持っているブロックを、ブロック against の面 f に向かって置く（Use Item On）。
// サーバーでブロックが置かれたことを確かめ、置かれた位置と状態を返す。
//
// 指す位置を向き、opt.Yaw があればその向きに変え、その向きがサーバーに届いたあと（同じティックの位置の送信のあと）に
// Use Item On（利き手、面、指す位置、シーケンス番号）と Swing Arm を送る。サーバーの確認のあと、
// against と隣のどちらも変わらなければ ErrNotPlaced。
func (cl *Client) Place(ctx context.Context, against BlockPos, f Face, opt PlaceOpts) (Placed, error) {
	var yaw *float32
	if opt.Yaw != nil {
		y := float32(*opt.Yaw)
		yaw = &y
	}
	before, err := cl.useOn(ctx, against, f, opt.Cursor, yaw)
	if err != nil {
		return Placed{}, err
	}
	n := against.Neighbor(f)
	var res Placed
	placed, err := cl.waitFor(ctx, resultGrace, func() bool {
		if s, ok := cl.Block(n); ok && s.ID != before[1].ID {
			res = Placed{Pos: n, State: s}
			return true
		}
		if s, ok := cl.Block(against); ok && s.ID != before[0].ID {
			res = Placed{Pos: against, State: s}
			return true
		}
		return false
	})
	if err != nil {
		return Placed{}, err
	}
	if !placed {
		return Placed{}, fmt.Errorf("%w: %v の面 %v（%v と %v のまま）", ErrNotPlaced, against, f, before[0], before[1])
	}
	return res, nil
}

// UseBlock はブロック p の面 f を使う（右クリック。ドアやレバーを切り替える、ベッドで寝る、容器を開ける）。
// 面の真ん中を向き、Use Item On と Swing Arm を送り、サーバーの確認を待つ。
// 容器（チェスト、かまど、作業台など）なら、さらに窓（Open Screen）が届くまで待ち、届かなければ ErrNoScreen。
// 開いた容器は Container で読める。ドアなどの結果は Block で確かめる。
func (cl *Client) UseBlock(ctx context.Context, p BlockPos, f Face) error {
	s, ok := cl.Block(p)
	if !ok {
		return ErrNotLoaded
	}
	st := invKey.of(cl)
	st.mu.Lock()
	prev := st.win
	st.mu.Unlock()
	if _, err := cl.useOn(ctx, p, f, nil, nil); err != nil {
		return err
	}
	if !opensScreen(s.Block) {
		return nil
	}
	opened, err := cl.waitFor(ctx, resultGrace, func() bool {
		st.mu.Lock()
		defer st.mu.Unlock()
		return st.win != nil && st.win != prev
	})
	if err != nil {
		return err
	}
	if !opened {
		return fmt.Errorf("%w: %v", ErrNoScreen, s)
	}
	return nil
}

// screenTypes は使うと窓が開くブロックの種類（公式データの definition.type）。
var screenTypes = map[string]bool{
	"chest": true, "trapped_chest": true, "ender_chest": true, "barrel": true, "shulker_box": true,
	"furnace": true, "blast_furnace": true, "smoker": true, "hopper": true, "dispenser": true, "dropper": true,
	"crafter": true, "crafting_table": true, "anvil": true, "enchantment_table": true, "brewing_stand": true,
	"loom": true, "stonecutter": true, "grindstone": true, "cartography_table": true, "smithing_table": true,
	"beacon": true,
}

func opensScreen(b *data.Block) bool { return screenTypes[b.Type] }

// useOn は against の面 f を指して Use Item On を送り、確認を待つ。cursor が nil なら面の真ん中。
// yaw があれば、指す位置を向いたあと横の向きをそれに変える。送る直前の against と隣の状態を返す。
func (cl *Client) useOn(ctx context.Context, against BlockPos, f Face, cursor *r3.Vec, yaw *float32) ([2]data.BlockState, error) {
	var before [2]data.BlockState
	if !f.Valid() {
		return before, fmt.Errorf("mcdriver: 正しくない面 %d", int(f))
	}
	c := r3.Add(r3.Vec{X: 0.5, Y: 0.5, Z: 0.5}, r3.Scale(0.5, f.Normal()))
	if cursor != nil {
		c = *cursor
		if c.X < 0 || c.X > 1 || c.Y < 0 || c.Y > 1 || c.Z < 0 || c.Z > 1 {
			return before, fmt.Errorf("mcdriver: Cursor %v はブロックの外（0〜1）", c)
		}
	}
	if _, ok := cl.Block(against); !ok {
		return before, ErrNotLoaded
	}
	aim := r3.Add(against.Vec(), c)

	cl.mu.Lock()
	d := r3.Norm(r3.Sub(aim, cl.eyeLocked()))
	rng := cl.attrLocked(attrBlockRange)
	cl.mu.Unlock()
	if d > rng {
		return before, fmt.Errorf("%w: %v の %v まで %.2f", ErrOutOfReach, against, c, d)
	}

	var seq int32
	err := cl.nextTick(ctx, func() {
		cl.aimLocked(aim)
		if yaw != nil {
			cl.yaw = wrapDegrees(*yaw)
		}
	}, func(cl *Client) error {
		var ok0, ok1 bool
		before[0], ok0 = cl.blockLocked(against)
		before[1], ok1 = cl.blockLocked(against.Neighbor(f))
		if !ok0 || !ok1 {
			return ErrNotLoaded
		}
		seq = cl.nextSeqLocked()
		if err := cl.c.Send(data.PlayServerboundUseItemOn, func(e *protocol.Encoder) error {
			return packets.WriteUseItemOn(e, packets.UseItemOn{
				Hand:    packets.HandMain,
				Pos:     protocol.Position{X: against.X, Y: against.Y, Z: against.Z},
				Face:    int32(f),
				CursorX: float32(c.X), CursorY: float32(c.Y), CursorZ: float32(c.Z),
				Sequence: seq,
			})
		}); err != nil {
			return err
		}
		return cl.swingLocked()
	})
	if err != nil {
		return before, err
	}
	return before, cl.waitAck(ctx, seq)
}

// ---- アイテムを使う ----

// StartUseItem は手に持っている物を使い始める（Use Item。食べる、飲む、弓を引く、盾を構えるなど）。
// 送るだけで待たない。食べ物は使い続けると（ふつう 1.6 秒）サーバーが食べ終わりにする。
// 弓は StopUseItem で放つ。今の向きで使う（向きはパケットにも乗り、サーバーはその向きにしてから使う）。
//
// 使っているあいだは、本家と同じく歩く速さが 0.2 倍になり、走れない（物理の Input.UsingItem）。
// 使い始めたとみなすのは、本家のクライアントが使い始める物（食べ物・飲み物は食べられるとき、弓は矢があるとき、
// 盾・望遠鏡・トライデント・ヤギの角笛）を手に持って呼んだときと、サーバーが自分の「使っている」の印を送ってきたとき。
// やめたとみなすのは、StopUseItem、サーバーの印が消えたとき（食べ終わり など）、手に持つ物が変わったとき、リスポーン。
func (cl *Client) StartUseItem() error {
	// 持ち物の鍵は cl.mu より先に取る（SelectHotbar は持ち物の鍵を持ったまま cl.mu を取る）
	held := cl.actHeld()
	hasArrow := false
	if held != nil && held.Name == "bow" {
		inv := cl.Inventory()
		hasArrow = inv.Count("arrow")+inv.Count("tipped_arrow")+inv.Count("spectral_arrow") > 0
	}
	self := cl.Self()
	cl.mu.Lock()
	defer cl.mu.Unlock()
	if cl.state != data.StatePlay {
		return ErrNotPlaying
	}
	seq := cl.nextSeqLocked()
	err := cl.c.Send(data.PlayServerboundUseItem, func(e *protocol.Encoder) error {
		packets.WriteUseItem(e, packets.UseItem{Hand: packets.HandMain, Sequence: seq, Yaw: cl.yaw, Pitch: cl.pitch})
		return nil
	})
	if err != nil {
		return cl.sendErr(err)
	}
	as := actKey.of(cl)
	if !as.using && startsUsing(held, self, hasArrow) {
		as.using, as.useItem, as.useUnconfirmed = true, held, 0
	}
	return nil
}

// useConfirmTicks は、StartUseItem で使い始めたとみなしてから、サーバーの「使っている」の印を待つティック数。
// 届かなければ（サーバーが使わせなかった）やめたとみなす。
const useConfirmTicks = 20

// startsUsing は、本家のクライアントが Use Item で使い始める（Item.use が CONSUME を返して startUsingItem する）物かを返す。
// 食べ物は満腹でないか、いつでも食べられるもの（クリエイティブなら満腹でも）。弓は矢があるとき（クリエイティブなら無くても）。
// 石弓は弾を込めたかが分からないので、ここでは決めない（サーバーの印で分かる）。
func startsUsing(held *data.Item, self Self, hasArrow bool) bool {
	if held == nil {
		return false
	}
	creative := self.GameMode == Creative
	if held.Consumable != nil {
		if held.Food != nil && !held.Food.CanAlwaysEat && self.Food >= 20 && !creative {
			return false
		}
		return true
	}
	switch held.Name {
	case "bow":
		return hasArrow || creative
	case "shield", "spyglass", "trident", "goat_horn":
		return true
	}
	return false
}

// StopUseItem は使っている物を離す（Player Action の Release Use Item。弓を放つなど）。
func (cl *Client) StopUseItem() error {
	cl.mu.Lock()
	defer cl.mu.Unlock()
	if cl.state != data.StatePlay {
		return ErrNotPlaying
	}
	actKey.of(cl).stopUsingLocked()
	// 本家の releaseUsingItem と同じく、位置 (0, 0, 0)、面は下、シーケンス番号は 0
	return cl.sendErr(cl.sendPlayerActionLocked(packets.ActionReleaseUseItem, BlockPos{}, Down, 0))
}

// stopUsingLocked はアイテムを使うのをやめたとする。cl.mu を持って呼ぶ。
func (as *actState) stopUsingLocked() {
	as.using, as.useItem, as.useUnconfirmed = false, nil, -1
}

// metaIndexLivingFlags は LivingEntity の印（Byte。0x01 がアイテムを使っている、0x02 が左手）。
const metaIndexLivingFlags = 8

// actOnMetadata は自分の Set Entity Metadata から「アイテムを使っている」の印を読む
// （本家の LocalPlayer.onSyncedDataUpdated: 印が立って使っていなければ使い始め、印が消えて使っていればやめる）。
func (cl *Client) actOnMetadata(d *protocol.Decoder) error {
	p, _ := packets.ReadSetEntityMetadata(d) // 読めないところは entities.go が記録する。読めた項目だけ使う
	ss := selfKey.of(cl)
	ss.mu.RLock()
	self := ss.s.EntityID
	ss.mu.RUnlock()
	if p.EntityID != self {
		return nil
	}
	for _, m := range p.Entries {
		if m.Index != metaIndexLivingFlags || m.Type != packets.MetaByte {
			continue
		}
		flag := m.Value.(uint8)&0x01 != 0
		held := cl.actHeld()
		cl.mu.Lock()
		as := actKey.of(cl)
		switch {
		case flag && !as.using:
			as.using, as.useItem = true, held
			as.useUnconfirmed = -1
		case flag:
			as.useUnconfirmed = -1
		case as.using:
			as.stopUsingLocked()
		}
		cl.mu.Unlock()
	}
	return nil
}

// useTickLocked は毎ティック、物理の前にアイテムを使っているかを決め直す。cl.mu を持って呼ぶ。
// held はこのティックの初めに手に持っていた物。
func (cl *Client) useTickLocked(held *data.Item) {
	as := actKey.of(cl)
	if !as.using {
		return
	}
	// 本家の LivingEntity.updatingUsingItem: 手に持つ物が変わったらやめる
	if held != as.useItem {
		as.stopUsingLocked()
		return
	}
	if as.useUnconfirmed >= 0 {
		as.useUnconfirmed++
		if as.useUnconfirmed > useConfirmTicks {
			as.stopUsingLocked()
		}
	}
}

// usingItemLocked はアイテムを使っているか（物理の Input.UsingItem）を返す。cl.mu を持って呼ぶ。
func (cl *Client) usingItemLocked() bool { return actKey.of(cl).using }

// ---- 攻撃 ----

// Attack はエンティティ id を攻撃する。体の真ん中を向き、次のティックで（向きを送ったあとに）
// Interact（attack）と Swing Arm を送る。送るまで待つ（1 ティックほど）。
// 届く距離（属性 entity_interaction_range。ふつう 3）は、目からエンティティの当たり判定の箱までで測る。
// 攻撃すると溜め（AttackCharge）は 0 に戻る。ダメージは溜めの 2 乗くらいに比例する（本家）ので、
// 全力で攻撃するには AttackCharge が 1 になってから呼ぶ。
// 位置をまだ受け取っていない（リスポーンの直後など）まま 5 秒たつと ErrNotSpawned。
func (cl *Client) Attack(id int32) error {
	e, ok := cl.Entity(id)
	if !ok {
		return fmt.Errorf("%w: %d", ErrNoEntity, id)
	}
	cl.mu.Lock()
	d := boxDistance(cl.eyeLocked(), e.Box)
	rng := cl.attrLocked(attrEntityRange)
	cl.mu.Unlock()
	if d > rng {
		return fmt.Errorf("%w: エンティティ %d まで %.2f", ErrOutOfReach, id, d)
	}
	center := r3.Scale(0.5, r3.Add(e.Box.Min, e.Box.Max))
	// ふつうは次のティックで送れるが、位置が届いていない（リスポーン直後など）あいだは毎ティックの処理が止まるので、待つ長さに上限を置く
	ctx, cancel := context.WithTimeoutCause(context.Background(), ackTimeout, ErrNotSpawned)
	defer cancel()
	return cl.nextTick(ctx, func() { cl.aimLocked(center) }, func(cl *Client) error {
		if err := cl.c.Send(data.PlayServerboundInteract, func(e *protocol.Encoder) error {
			return packets.WriteInteract(e, packets.Interact{EntityID: id, Type: packets.InteractAttack})
		}); err != nil {
			return err
		}
		// 本家は攻撃を送ったあと溜めを 0 に戻し、腕を振る
		actKey.of(cl).attackTicks = 0
		return cl.swingLocked()
	})
}

// AttackCharge は今攻撃したときの溜め（0〜1。1 なら全力）を返す。
// 本家の Player.getAttackStrengthScale(0.5): (最後に攻撃・持ち替えしてからのティック数 + 0.5) / (20 / 攻撃速度)。
// 攻撃速度は属性 attack_speed（素手は 4、剣は 1.6 など。サーバーが手に持つ物の修飾付きで送ってくる値）。
func (cl *Client) AttackCharge() float64 {
	cl.mu.Lock()
	defer cl.mu.Unlock()
	return float64(attackStrength(actKey.of(cl).attackTicks, cl.attrLocked(attrAttackSpeed)))
}

// attackStrength は本家の getAttackStrengthScale(0.5)。
func attackStrength(ticks int, attackSpeed float64) float32 {
	delay := float32(1.0 / attackSpeed * 20.0)
	return max(0, min(1, (float32(ticks)+0.5)/delay))
}
