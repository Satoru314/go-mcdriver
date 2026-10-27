package mcdriver

// 出来事（イベント）の知らせ。Events() のチャネルに、起きた順に届く。
//
// 届くもの: 死亡、リスポーン、ダメージ、位置の強制修正、ブロックの変化、生き物の出現と消滅、
// 容器が開いた・閉じた、天気の変化、切断。
//
// あふれたときの決まり:
//   - チャネルには Config.EventBuffer 個（既定 256）までためる
//   - いっぱいのときに新しい出来事が来たら、一番古いものを捨てて新しいものを入れる（受信の goroutine は止めない）。
//     捨てた数は EventsDropped() で分かる
//   - 切断したら最後に DisconnectEvent を入れて、チャネルを閉じる（for range で読んでいれば抜ける）
//
// 読まなくてもよい（たまるのは EventBuffer 個まで）。

import (
	"sync"
	"time"

	"github.com/Satoru314/go-mcdriver/data"
	"github.com/Satoru314/go-mcdriver/internal/conn"
	"github.com/Satoru314/go-mcdriver/internal/packets"
	"github.com/Satoru314/go-mcdriver/internal/protocol"
	"gonum.org/v1/gonum/spatial/r3"
)

// DefaultEventBuffer は Config.EventBuffer が 0 のときのチャネルの大きさ。
const DefaultEventBuffer = 256

// Event は出来事 1 つ。中身は *Event で終わる名前の型（DeathEvent など）のどれか。type switch で見分ける。
//
//	for ev := range c.Events() {
//		switch ev := ev.(type) {
//		case mcdriver.DeathEvent:
//			log.Println("死んだ:", ev.Message)
//		case mcdriver.BlockChangeEvent:
//			...
//		}
//	}
type Event interface {
	// Time は受け取った時刻。
	Time() time.Time
	isEvent()
}

// eventBase はどの出来事にも入っている部分。
type eventBase struct {
	// At は受け取った時刻
	At time.Time
}

func (b eventBase) Time() time.Time { return b.At }
func (eventBase) isEvent()          {}

// DeathEvent は自分が死んだ（Player Combat Kill）。
type DeathEvent struct {
	eventBase
	// Message は死んだ理由（例 "bot1 fell out of the world"）。gamerule showDeathMessages が false なら空のことがある
	Message string
}

// RespawnEvent はリスポーンした、または別のディメンションに移った（Respawn）。
// このあと新しい位置が TeleportEvent で届く。
type RespawnEvent struct {
	eventBase
	// Dimension は新しいディメンションの名前（例 "minecraft:overworld"）
	Dimension string
	// GameMode は新しいゲームモード
	GameMode GameMode
	// AfterDeath は死んだあとのリスポーンか（false ならディメンションの移動）
	AfterDeath bool
}

// DamageEvent は自分がダメージを受けた。
//
// サーバーは種類（Damage Event）と HP（Set Health）を別々に送るので、HP が減ったときに、
// その前に届いた種類と合わせて 1 つにする。種類が分からない減り方（属性の変更など）なら Type は空。
// 種類は届いたが HP が減らなかった（防具や無敵時間で 0 になった）ときは、1 秒たってから Amount 0 で知らせる。
type DamageEvent struct {
	eventBase
	// Type はダメージの種類（minecraft:damage_type の名前。例 "minecraft:magic"）。分からなければ空
	Type string
	// Amount は減った HP（吸収のハートが減った分は入らない）
	Amount float32
	// Health は減ったあとの HP
	Health float32
	// CauseEntityID は原因のエンティティ ID（攻撃したモブなど）。無ければ -1
	CauseEntityID int32
}

// TeleportEvent はサーバーから位置を強制的に直された（Synchronize Player Position。/tp、
// ログインやリスポーンのとき、動きをサーバーが認めなかったとき）。
type TeleportEvent struct {
	eventBase
	// Pos, Yaw, Pitch は直されたあとの位置と向き
	Pos        r3.Vec
	Yaw, Pitch float32
	// TeleportID はサーバーが付けた番号
	TeleportID int32
}

// BlockChangeEvent はブロックが変わった（Block Update、Update Section Blocks）。
// 読み込んでいるチャンクの中で、前と違う状態になったものだけ知らせる（チャンクを受け取ったときは知らせない）。
type BlockChangeEvent struct {
	eventBase
	Pos BlockPos
	// Old は前の状態、New は新しい状態。知らない状態 ID なら ID だけが入り Block は nil（Name() を呼ばない）
	Old, New data.BlockState
}

// EntitySpawnEvent は生き物や物が見えるところに現れた（Spawn Entity）。
// 名前や体力など、あとから届くものはまだ入っていないことがある。
type EntitySpawnEvent struct {
	eventBase
	Entity Entity
}

// EntityRemoveEvent は生き物や物が見えなくなった（Remove Entities。死んだ、遠くに行った、拾われた など）。
// Entity は消える直前の写し。ログインやディメンションの移動でまとめて消えたときは知らせない。
type EntityRemoveEvent struct {
	eventBase
	Entity Entity
}

// ContainerOpenEvent は容器（チェスト、かまど など）の窓が開いた（Open Screen）。
type ContainerOpenEvent struct {
	eventBase
	WindowID int32
	// Type は窓の種類（minecraft:menu の名前。例 "generic_9x3"）
	Type string
	// Title は窓の題（例 "Chest"）
	Title string
}

// ContainerCloseEvent は容器の窓が閉じた。
type ContainerCloseEvent struct {
	eventBase
	WindowID int32
	// ByServer はサーバーが閉じた（Close Container を受け取った）か。false なら CloseContainer で自分で閉じた
	ByServer bool
}

// WeatherEvent は天気（晴れ・雨・雷雨）が変わった。雨の強さだけの変化では知らせない。
type WeatherEvent struct {
	eventBase
	Weather Weather
}

// DisconnectEvent は接続が終わった。チャネルに入る最後の出来事。
type DisconnectEvent struct {
	eventBase
	// Err は終わった理由（Client.Err と同じ）。Close で閉じたなら ErrClosed
	Err error
}

// eventState は出来事のチャネル。
type eventState struct {
	mu      sync.Mutex
	ch      chan Event
	closed  bool
	dropped uint64

	// ダメージを合わせるための値（受信の goroutine だけが触るので鍵は要らない）
	health     float32
	hasHealth  bool
	dead       bool
	pending    *DamageEvent // 種類は届いたが、HP がまだ減っていないダメージ
	pendingAt  time.Time
	selfEntity int32
}

var eventKey = newFeature(func(cl *Client) *eventState {
	n := cl.cfg.EventBuffer
	if n <= 0 {
		n = DefaultEventBuffer
	}
	return &eventState{ch: make(chan Event, n)}
})

func init() {
	onPlay(data.PlayClientboundLogin, (*Client).evOnLogin)
	onPlay(data.PlayClientboundPlayerCombatKill, (*Client).evOnCombatKill)
	onPlay(data.PlayClientboundRespawn, (*Client).evOnRespawn)
	onPlay(data.PlayClientboundDamageEvent, (*Client).evOnDamage)
	onPlay(data.PlayClientboundSetHealth, (*Client).evOnSetHealth)
	onPlay(data.PlayClientboundPlayerPosition, (*Client).evOnTeleport)
}

// ---- 公開 API ----

// Events は出来事のチャネルを返す。何度呼んでも同じチャネル。切断すると最後に DisconnectEvent が入って閉じる。
// 読むのが遅れていっぱいになると、古いものから捨てる（EventsDropped で数が分かる）。
func (cl *Client) Events() <-chan Event { return eventKey.of(cl).ch }

// EventsDropped は、チャネルがいっぱいで捨てた出来事の数を返す。
func (cl *Client) EventsDropped() uint64 {
	es := eventKey.of(cl)
	es.mu.Lock()
	defer es.mu.Unlock()
	return es.dropped
}

// ---- 中で使うもの ----

// emit は出来事をチャネルに入れる。いっぱいなら一番古いものを捨てる。止まらない。
// どの goroutine から呼んでもよく、ほかの鍵を持ったまま呼んでもよい（es.mu しか取らない）。
func (cl *Client) emit(ev Event) {
	es := eventKey.of(cl)
	es.mu.Lock()
	defer es.mu.Unlock()
	es.pushLocked(ev)
}

func (es *eventState) pushLocked(ev Event) {
	if es.closed {
		return
	}
	for {
		select {
		case es.ch <- ev:
			return
		default:
		}
		// いっぱい。一番古いものを捨てる（読む側が先に取っていれば捨てずにもう一度入れてみる）
		select {
		case <-es.ch:
			es.dropped++
		default:
		}
	}
}

// emitDisconnect は DisconnectEvent を入れてチャネルを閉じる。client.go の shutdown から 1 度だけ呼ばれる。
func (cl *Client) emitDisconnect(err error) {
	es := eventKey.of(cl)
	es.mu.Lock()
	defer es.mu.Unlock()
	es.pushLocked(DisconnectEvent{eventBase: evNow(), Err: err})
	es.closed = true
	close(es.ch)
}

func evNow() eventBase { return eventBase{At: time.Now()} }

// emitBlockChange は前と違う状態になったブロックを知らせる（world.go から呼ぶ）。
func (cl *Client) emitBlockChange(p BlockPos, old, new int32) {
	if old == new {
		return
	}
	o, _ := data.StateByID(data.StateID(old))
	n, _ := data.StateByID(data.StateID(new))
	o.ID, n.ID = data.StateID(old), data.StateID(new) // 知らない番号でも番号は残す
	cl.emit(BlockChangeEvent{eventBase: evNow(), Pos: p, Old: o, New: n})
}

// ---- 受け取ったパケットの処理（自分の体の出来事） ----

func (cl *Client) evOnLogin(d *protocol.Decoder) error {
	lp, err := packets.ReadLoginPlay(d)
	if err != nil {
		return nil // 読めないものは client.go が切断にする
	}
	es := eventKey.of(cl)
	es.selfEntity = int32(lp.EntityID)
	es.dead = false
	return nil
}

func (cl *Client) evOnCombatKill(d *protocol.Decoder) error {
	p, err := packets.ReadPlayerCombatKill(d)
	if err != nil {
		return nil // self.go が切断にする
	}
	es := eventKey.of(cl)
	if int32(p.PlayerID) != es.selfEntity {
		return nil
	}
	es.dead = true
	cl.emit(DeathEvent{eventBase: evNow(), Message: conn.TextPlain(p.Message)})
	return nil
}

func (cl *Client) evOnRespawn(d *protocol.Decoder) error {
	rp, err := packets.ReadRespawn(d)
	if err != nil {
		return nil // self.go が切断にする
	}
	es := eventKey.of(cl)
	cl.emit(RespawnEvent{eventBase: evNow(), Dimension: rp.DimensionName, GameMode: GameMode(rp.GameMode), AfterDeath: es.dead})
	es.dead = false
	es.pending = nil
	return nil
}

// damageFlushAfter は、HP が減らないままのダメージの種類を Amount 0 で知らせるまでの時間。
const damageFlushAfter = time.Second

func (cl *Client) evOnDamage(d *protocol.Decoder) error {
	p, err := packets.ReadDamageEvent(d)
	if err != nil {
		return nil // self.go が切断にする
	}
	es := eventKey.of(cl)
	if int32(p.EntityID) != es.selfEntity {
		return nil
	}
	if es.pending != nil {
		// 前の種類は HP が減らないまま次が来た
		es.pending.Health = es.health
		cl.emit(*es.pending)
	}
	ev := DamageEvent{eventBase: evNow(), CauseEntityID: p.SourceCauseID}
	if types := cl.reg.Entries("minecraft:damage_type"); p.SourceTypeID >= 0 && int(p.SourceTypeID) < len(types) {
		ev.Type = types[p.SourceTypeID].ID
	}
	es.pending, es.pendingAt = &ev, time.Now()
	return nil
}

func (cl *Client) evOnSetHealth(d *protocol.Decoder) error {
	p, err := packets.ReadSetHealth(d)
	if err != nil {
		return nil // self.go が切断にする
	}
	es := eventKey.of(cl)
	prev, had := es.health, es.hasHealth
	es.health, es.hasHealth = p.Health, true
	switch {
	case had && p.Health < prev:
		ev := DamageEvent{eventBase: evNow(), CauseEntityID: -1}
		if es.pending != nil {
			ev = *es.pending
			ev.At = time.Now()
			es.pending = nil
		}
		ev.Amount, ev.Health = prev-p.Health, p.Health
		cl.emit(ev)
	default:
		cl.flushStaleDamage()
	}
	return nil
}

// flushStaleDamage は、種類は届いたが damageFlushAfter たっても HP が減らないダメージを Amount 0 で知らせる。
// 受信の goroutine から呼ぶ（Set Health と、1 秒ごとに届く Update Time のとき）。
func (cl *Client) flushStaleDamage() {
	es := eventKey.of(cl)
	if es.pending == nil || time.Since(es.pendingAt) < damageFlushAfter {
		return
	}
	es.pending.Health = es.health
	cl.emit(*es.pending)
	es.pending = nil
}

func (cl *Client) evOnTeleport(d *protocol.Decoder) error {
	pp, err := packets.ReadPlayerPosition(d)
	if err != nil {
		return nil // client.go が切断にする
	}
	// client.go の onTeleport が当てはめたあとに呼ばれるので、今の値が直されたあとの位置
	cl.mu.Lock()
	ev := TeleportEvent{eventBase: evNow(), Pos: r3.Vec{X: cl.x, Y: cl.y, Z: cl.z}, Yaw: cl.yaw, Pitch: cl.pitch, TeleportID: pp.TeleportID}
	cl.mu.Unlock()
	cl.emit(ev)
	return nil
}
