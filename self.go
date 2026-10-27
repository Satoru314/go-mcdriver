package mcdriver

// 自分の体（位置、速度、向き、接地、HP、満腹度、経験値、死亡、ゲームモード）の受け取りと読み出し、
// 死んだあとのリスポーンの要求。
//
// 受け取るパケット: Login (play)（エンティティ ID とゲームモード）、Respawn、Set Health、Set Experience、
// Player Combat Kill（死亡）、Game Event（ゲームモードの変更、すぐリスポーンするか）、Player Abilities、
// Damage Event（自分へのダメージ）、Synchronize Player Position と Set Entity Motion（速度）。
// 位置と向きと接地は client.go が持っている値（位置の強制修正で当てはめたもの）を読む。

import (
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/Satoru314/go-mcdriver/data"
	"github.com/Satoru314/go-mcdriver/internal/conn"
	"github.com/Satoru314/go-mcdriver/internal/packets"
	"github.com/Satoru314/go-mcdriver/internal/protocol"
	"gonum.org/v1/gonum/spatial/r3"
)

// GameMode はゲームモード。
type GameMode uint8

const (
	Survival  GameMode = 0
	Creative  GameMode = 1
	Adventure GameMode = 2
	Spectator GameMode = 3
)

// String は "survival" などの名前を返す（/gamemode の引数と同じ）。
func (m GameMode) String() string {
	switch m {
	case Survival:
		return "survival"
	case Creative:
		return "creative"
	case Adventure:
		return "adventure"
	case Spectator:
		return "spectator"
	}
	return fmt.Sprintf("GameMode(%d)", uint8(m))
}

// Experience は経験値。
type Experience struct {
	// Bar は今のレベルの中での進み（0〜1）
	Bar float32
	// Level はレベル
	Level int
	// Total は合計の経験値
	Total int
}

// Abilities は Player Abilities で届く、自分にできること。
type Abilities struct {
	// Invulnerable はダメージを受けない（クリエイティブなど）
	Invulnerable bool
	// Flying は今飛んでいる
	Flying bool
	// AllowFlying は飛べる
	AllowFlying bool
	// InstantBuild はブロックを一瞬で壊せる（クリエイティブ）
	InstantBuild bool
	// FlyingSpeed は飛ぶ速さ（ふつう 0.05）
	FlyingSpeed float32
	// WalkingSpeed は歩く速さ（ふつう 0.1。Player Abilities の FOV Modifier）
	WalkingSpeed float32
}

// Damage は自分が受けたダメージ（Damage Event）。
type Damage struct {
	// Type はダメージの種類（minecraft:damage_type の名前。例 "minecraft:magic"）。分からなければ空
	Type string
	// TypeID は minecraft:damage_type の番号
	TypeID int
	// CauseEntityID は原因のエンティティ ID（攻撃したモブなど）。無ければ -1
	CauseEntityID int
	// DirectEntityID は直接当たったエンティティ ID（矢など）。無ければ -1
	DirectEntityID int
	// SourcePos はダメージの出どころの位置。HasSourcePos が false なら意味がない
	SourcePos    r3.Vec
	HasSourcePos bool
	// At は受け取った時刻
	At time.Time
}

// Self は自分の体の状態の写し。
type Self struct {
	// EntityID は自分のエンティティ ID（Login (play) で決まる）
	EntityID int
	// Spawned は位置を受け取った（Pos が意味を持つ）か。ログイン直後やリスポーン直後の位置が届く前は false
	Spawned bool
	// Pos は足の位置
	Pos r3.Vec
	// Vel は速度（ブロック/ティック）。サーバーから届いた値（Set Entity Motion、位置の強制修正）。
	// 物理（単位 7）ができるまでは、届いた値をそのまま持つだけ
	Vel r3.Vec
	// Yaw, Pitch は向き（度）。Yaw は 0 が南（+z）で時計回り、Pitch は下向きが正（-90〜90）
	Yaw, Pitch float32
	// OnGround は地面に立っているか
	OnGround bool

	// Health は HP（ふつう 0〜20）。0 以下なら死んでいる
	Health float32
	// Food は満腹度（0〜20）
	Food int
	// Saturation は隠し満腹度（0〜Food）
	Saturation float32
	// Experience は経験値
	Experience Experience

	// Dead は死んでいるか（Respawn を呼ぶとサーバーがリスポーンさせる）
	Dead bool
	// DeathMessage は最後に死んだときの理由（Player Combat Kill の文字の部品を文字列にしたもの）
	DeathMessage string
	// ImmediateRespawn は gamerule doImmediateRespawn が true か（true でもこのドライバは自分では
	// リスポーンしないので、Respawn を呼ぶ）
	ImmediateRespawn bool

	// GameMode はゲームモード
	GameMode GameMode
	// Abilities は飛べるかなど
	Abilities Abilities

	// LastDamage は最後に受けたダメージ。HasDamage が false なら一度も受けていない
	LastDamage Damage
	HasDamage  bool
}

// ErrNotDead は死んでいないのに Respawn を呼んだときのエラー。
var ErrNotDead = errors.New("mcdriver: 死んでいないのでリスポーンできない")

// selfState は自分の体の状態（位置と向きと接地は client.go の cl.mu の中）。鍵 mu で守る。
type selfState struct {
	mu sync.RWMutex
	s  Self
}

var selfKey = newFeature(func(*Client) *selfState { return &selfState{} })

func init() {
	onPlay(data.PlayClientboundLogin, (*Client).selfOnLogin)
	onPlay(data.PlayClientboundRespawn, (*Client).selfOnRespawn)
	onPlay(data.PlayClientboundSetHealth, (*Client).selfOnSetHealth)
	onPlay(data.PlayClientboundSetExperience, (*Client).selfOnSetExperience)
	onPlay(data.PlayClientboundPlayerCombatKill, (*Client).selfOnCombatKill)
	onPlay(data.PlayClientboundGameEvent, (*Client).selfOnGameEvent)
	onPlay(data.PlayClientboundPlayerAbilities, (*Client).selfOnAbilities)
	onPlay(data.PlayClientboundDamageEvent, (*Client).selfOnDamage)
	onPlay(data.PlayClientboundPlayerPosition, (*Client).selfOnPosition)
	onPlay(data.PlayClientboundSetEntityMotion, (*Client).selfOnMotion)
}

// ---- 公開 API ----

// Self は自分の体の状態の写しを返す。
func (cl *Client) Self() Self {
	cl.mu.Lock()
	pos := r3.Vec{X: cl.x, Y: cl.y, Z: cl.z}
	yaw, pitch, onGround, spawned := cl.yaw, cl.pitch, cl.onGround, cl.hasPos
	cl.mu.Unlock()

	ss := selfKey.of(cl)
	ss.mu.RLock()
	s := ss.s
	ss.mu.RUnlock()
	s.Pos, s.Yaw, s.Pitch, s.OnGround, s.Spawned = pos, yaw, pitch, onGround, spawned
	return s
}

// Respawn は死んだあとにリスポーンを要求する（Client Command の action 0 を送る）。
// 要求を送るだけで、生き返るのを待たない。生き返ると Self().Dead が false になり、
// 新しい位置が届くと Self().Spawned が true になる。死んでいないときは ErrNotDead を返す。
func (cl *Client) Respawn() error {
	ss := selfKey.of(cl)
	ss.mu.RLock()
	dead := ss.s.Dead
	ss.mu.RUnlock()
	if !dead {
		return ErrNotDead
	}
	select {
	case <-cl.done:
		return fmt.Errorf("mcdriver: リスポーン: 接続が終わっている: %w", cl.Err())
	default:
	}
	// 段階の切り替えと順番を保つため、cl.mu を持って送る
	cl.mu.Lock()
	defer cl.mu.Unlock()
	if cl.state != data.StatePlay {
		return errors.New("mcdriver: リスポーン: 設定の段階なので送れない")
	}
	if err := cl.c.Send(data.PlayServerboundClientCommand, func(e *protocol.Encoder) error {
		e.WriteVarInt(packets.ClientCommandRespawn)
		return nil
	}); err != nil {
		return fmt.Errorf("mcdriver: リスポーン: %w", err)
	}
	return nil
}

// ---- 受け取ったパケットの処理 ----

func (cl *Client) selfOnLogin(d *protocol.Decoder) error {
	lp, err := packets.ReadLoginPlay(d)
	if err != nil {
		return fmt.Errorf("mcdriver: Login (play): %w", err)
	}
	ss := selfKey.of(cl)
	ss.mu.Lock()
	defer ss.mu.Unlock()
	ss.s.EntityID = int(lp.EntityID)
	ss.s.GameMode = GameMode(lp.GameMode)
	ss.s.Dead = false
	return nil
}

func (cl *Client) selfOnRespawn(d *protocol.Decoder) error {
	rp, err := packets.ReadRespawn(d)
	if err != nil {
		return fmt.Errorf("mcdriver: Respawn: %w", err)
	}
	// リスポーン（または別のディメンションへの移動）のあとは、サーバーが位置を送り直す。
	// 1.21.4 のサーバーはリスポーンのあと Player Loaded を待つ（来ないと 60 ティックのあいだダメージを受けない）ので、
	// 次の位置の強制修正のあとにもう一度送るようにする（client.go の onTeleport が送る）
	cl.mu.Lock()
	cl.hasPos = false
	cl.loadedSent = false
	cl.mu.Unlock()

	ss := selfKey.of(cl)
	ss.mu.Lock()
	defer ss.mu.Unlock()
	ss.s.GameMode = GameMode(rp.GameMode)
	ss.s.Dead = false
	ss.s.Vel = r3.Vec{}
	return nil
}

func (cl *Client) selfOnSetHealth(d *protocol.Decoder) error {
	p, err := packets.ReadSetHealth(d)
	if err != nil {
		return fmt.Errorf("mcdriver: Set Health: %w", err)
	}
	ss := selfKey.of(cl)
	ss.mu.Lock()
	defer ss.mu.Unlock()
	ss.s.Health, ss.s.Food, ss.s.Saturation = p.Health, int(p.Food), p.Saturation
	if p.Health <= 0 {
		ss.s.Dead = true
	}
	return nil
}

func (cl *Client) selfOnSetExperience(d *protocol.Decoder) error {
	p, err := packets.ReadSetExperience(d)
	if err != nil {
		return fmt.Errorf("mcdriver: Set Experience: %w", err)
	}
	ss := selfKey.of(cl)
	ss.mu.Lock()
	defer ss.mu.Unlock()
	ss.s.Experience = Experience{Bar: p.Bar, Level: int(p.Level), Total: int(p.Total)}
	return nil
}

func (cl *Client) selfOnCombatKill(d *protocol.Decoder) error {
	p, err := packets.ReadPlayerCombatKill(d)
	if err != nil {
		return fmt.Errorf("mcdriver: Player Combat Kill: %w", err)
	}
	ss := selfKey.of(cl)
	ss.mu.Lock()
	defer ss.mu.Unlock()
	if int(p.PlayerID) != ss.s.EntityID {
		return nil // 自分のことでなければ無視（サーバーは自分にしか送らない）
	}
	// Set Health（HP 0）は次のティックに届くので、ここで 0 にしておく（Dead なのに HP が残って見えないように）
	ss.s.Dead = true
	ss.s.Health = 0
	ss.s.DeathMessage = conn.TextPlain(p.Message)
	return nil
}

func (cl *Client) selfOnGameEvent(d *protocol.Decoder) error {
	p, err := packets.ReadGameEvent(d)
	if err != nil {
		return fmt.Errorf("mcdriver: Game Event: %w", err)
	}
	ss := selfKey.of(cl)
	ss.mu.Lock()
	defer ss.mu.Unlock()
	switch p.Event {
	case packets.GameEventChangeGameMode:
		ss.s.GameMode = GameMode(int(p.Value))
	case packets.GameEventImmediateRespawn:
		ss.s.ImmediateRespawn = p.Value == 1
	}
	return nil
}

func (cl *Client) selfOnAbilities(d *protocol.Decoder) error {
	p, err := packets.ReadPlayerAbilities(d)
	if err != nil {
		return fmt.Errorf("mcdriver: Player Abilities: %w", err)
	}
	ss := selfKey.of(cl)
	ss.mu.Lock()
	defer ss.mu.Unlock()
	ss.s.Abilities = Abilities{
		Invulnerable: p.Flags&packets.AbilityInvulnerable != 0,
		Flying:       p.Flags&packets.AbilityFlying != 0,
		AllowFlying:  p.Flags&packets.AbilityAllowFlying != 0,
		InstantBuild: p.Flags&packets.AbilityInstantBuild != 0,
		FlyingSpeed:  p.FlyingSpeed,
		WalkingSpeed: p.FOVModifier,
	}
	return nil
}

func (cl *Client) selfOnDamage(d *protocol.Decoder) error {
	p, err := packets.ReadDamageEvent(d)
	if err != nil {
		return fmt.Errorf("mcdriver: Damage Event: %w", err)
	}
	ss := selfKey.of(cl)
	ss.mu.RLock()
	mine := int(p.EntityID) == ss.s.EntityID
	ss.mu.RUnlock()
	if !mine {
		return nil // ほかのエンティティへのダメージは単位 6・10 で扱う
	}
	dmg := Damage{
		TypeID:         int(p.SourceTypeID),
		CauseEntityID:  int(p.SourceCauseID),
		DirectEntityID: int(p.SourceDirectID),
		HasSourcePos:   p.HasSourcePos,
		SourcePos:      r3.Vec{X: p.SourceX, Y: p.SourceY, Z: p.SourceZ},
		At:             time.Now(),
	}
	if types := cl.reg.Entries("minecraft:damage_type"); p.SourceTypeID >= 0 && int(p.SourceTypeID) < len(types) {
		dmg.Type = types[p.SourceTypeID].ID
	}
	ss.mu.Lock()
	defer ss.mu.Unlock()
	ss.s.LastDamage, ss.s.HasDamage = dmg, true
	return nil
}

// selfOnPosition は位置の強制修正の速度を当てはめる（位置と向きは client.go が当てはめる）。
// RotateVelocity（向きの変化に合わせて速度を回す）は扱わない（物理の単位 7 で扱う）。
func (cl *Client) selfOnPosition(d *protocol.Decoder) error {
	pp, err := packets.ReadPlayerPosition(d)
	if err != nil {
		return fmt.Errorf("mcdriver: Synchronize Player Position: %w", err)
	}
	ss := selfKey.of(cl)
	ss.mu.Lock()
	defer ss.mu.Unlock()
	v := r3.Vec{X: pp.VelX, Y: pp.VelY, Z: pp.VelZ}
	if pp.Flags&packets.RelVelX != 0 {
		v.X += ss.s.Vel.X
	}
	if pp.Flags&packets.RelVelY != 0 {
		v.Y += ss.s.Vel.Y
	}
	if pp.Flags&packets.RelVelZ != 0 {
		v.Z += ss.s.Vel.Z
	}
	ss.s.Vel = v
	return nil
}

// selfOnMotion は Set Entity Motion が自分のものなら速度にする（ノックバックなど）。
// 中身: エンティティ ID（VarInt）、速度 x, y, z（Short、1/8000 ブロック/ティック）。
func (cl *Client) selfOnMotion(d *protocol.Decoder) error {
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
	ss := selfKey.of(cl)
	ss.mu.Lock()
	defer ss.mu.Unlock()
	if int(id) != ss.s.EntityID {
		return nil
	}
	ss.s.Vel = r3.Vec{X: float64(v[0]) / 8000, Y: float64(v[1]) / 8000, Z: float64(v[2]) / 8000}
	return nil
}
