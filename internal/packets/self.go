package packets

// 自分の体（単位 5）に関わるパケット: Set Health、Set Experience、Player Combat Kill、
// Game Event、Player Abilities、Damage Event、Client Command。

import (
	"github.com/Satoru314/go-mcdriver/internal/nbt"
	"github.com/Satoru314/go-mcdriver/internal/protocol"
)

// SetHealth は Set Health。HP、満腹度、隠し満腹度（saturation）。
type SetHealth struct {
	Health     float32 // 0 以下なら死んでいる
	Food       int32   // 0〜20
	Saturation float32 // 0〜5
}

// ReadSetHealth は Set Health を読む。
func ReadSetHealth(d *protocol.Decoder) (SetHealth, error) {
	var p SetHealth
	var err error
	if p.Health, err = d.ReadFloat(); err != nil {
		return p, err
	}
	if p.Food, err = d.ReadVarInt(); err != nil {
		return p, err
	}
	p.Saturation, err = d.ReadFloat()
	return p, err
}

// WriteSetHealth は Set Health を書く（テストの偽のサーバー用）。
func WriteSetHealth(e *protocol.Encoder, p SetHealth) {
	e.WriteFloat(p.Health)
	e.WriteVarInt(p.Food)
	e.WriteFloat(p.Saturation)
}

// SetExperience は Set Experience。
type SetExperience struct {
	Bar   float32 // 今のレベルの中での進み（0〜1）
	Level int32
	Total int32 // 合計の経験値
}

// ReadSetExperience は Set Experience を読む。
func ReadSetExperience(d *protocol.Decoder) (SetExperience, error) {
	var p SetExperience
	var err error
	if p.Bar, err = d.ReadFloat(); err != nil {
		return p, err
	}
	if p.Level, err = d.ReadVarInt(); err != nil {
		return p, err
	}
	p.Total, err = d.ReadVarInt()
	return p, err
}

// WriteSetExperience は Set Experience を書く（テストの偽のサーバー用）。
func WriteSetExperience(e *protocol.Encoder, p SetExperience) {
	e.WriteFloat(p.Bar)
	e.WriteVarInt(p.Level)
	e.WriteVarInt(p.Total)
}

// PlayerCombatKill は Player Combat Kill（自分が死んだときに届く）。
type PlayerCombatKill struct {
	PlayerID int32 // 死んだプレイヤー（自分）のエンティティ ID
	Message  any   // 死んだ理由の文字の部品（NBT を読んだ値）。gamerule showDeathMessages が false なら空
}

// ReadPlayerCombatKill は Player Combat Kill を読む。
func ReadPlayerCombatKill(d *protocol.Decoder) (PlayerCombatKill, error) {
	var p PlayerCombatKill
	var err error
	if p.PlayerID, err = d.ReadVarInt(); err != nil {
		return p, err
	}
	p.Message, err = nbt.ReadNetwork(d)
	return p, err
}

// WritePlayerCombatKill は Player Combat Kill を書く（テストの偽のサーバー用）。
func WritePlayerCombatKill(e *protocol.Encoder, p PlayerCombatKill) error {
	e.WriteVarInt(p.PlayerID)
	return nbt.WriteNetwork(e, p.Message)
}

// Game Event の種類（Event の値）。
const (
	GameEventNoRespawnBlock uint8 = 0
	// 雨の終了と開始。本家の名前（START_RAINING など）と実際の向きが紛らわしいので、
	// Paper 1.21.4 で確かめた値に合わせた（weather rain で 2、weather clear で 1 が届く）
	GameEventEndRaining         uint8 = 1
	GameEventBeginRaining       uint8 = 2
	GameEventChangeGameMode     uint8 = 3 // Value がゲームモード（0 サバイバル, 1 クリエイティブ, 2 アドベンチャー, 3 スペクテイター）
	GameEventWinGame            uint8 = 4
	GameEventDemo               uint8 = 5
	GameEventArrowHitPlayer     uint8 = 6
	GameEventRainLevel          uint8 = 7
	GameEventThunderLevel       uint8 = 8
	GameEventPufferfishSting    uint8 = 9
	GameEventGuardianCurse      uint8 = 10
	GameEventImmediateRespawn   uint8 = 11 // Value 1 なら死んだらすぐリスポーンする（gamerule doImmediateRespawn）
	GameEventLimitedCrafting    uint8 = 12
	GameEventStartWaitingChunks uint8 = 13
)

// GameEvent は Game Event。
type GameEvent struct {
	Event uint8
	Value float32
}

// ReadGameEvent は Game Event を読む。
func ReadGameEvent(d *protocol.Decoder) (GameEvent, error) {
	var p GameEvent
	var err error
	if p.Event, err = d.ReadUByte(); err != nil {
		return p, err
	}
	p.Value, err = d.ReadFloat()
	return p, err
}

// WriteGameEvent は Game Event を書く（テストの偽のサーバー用）。
func WriteGameEvent(e *protocol.Encoder, p GameEvent) {
	e.WriteUByte(p.Event)
	e.WriteFloat(p.Value)
}

// Player Abilities の Flags。
const (
	AbilityInvulnerable uint8 = 0x01
	AbilityFlying       uint8 = 0x02
	AbilityAllowFlying  uint8 = 0x04
	AbilityInstantBuild uint8 = 0x08 // クリエイティブ（ブロックを一瞬で壊せる）
)

// PlayerAbilities は Player Abilities（clientbound）。
type PlayerAbilities struct {
	Flags       uint8
	FlyingSpeed float32
	FOVModifier float32 // 歩く速さ（視野の変化に使う）
}

// ReadPlayerAbilities は Player Abilities を読む。
func ReadPlayerAbilities(d *protocol.Decoder) (PlayerAbilities, error) {
	var p PlayerAbilities
	var err error
	if p.Flags, err = d.ReadUByte(); err != nil {
		return p, err
	}
	if p.FlyingSpeed, err = d.ReadFloat(); err != nil {
		return p, err
	}
	p.FOVModifier, err = d.ReadFloat()
	return p, err
}

// WritePlayerAbilities は Player Abilities を書く（テストの偽のサーバー用）。
func WritePlayerAbilities(e *protocol.Encoder, p PlayerAbilities) {
	e.WriteUByte(p.Flags)
	e.WriteFloat(p.FlyingSpeed)
	e.WriteFloat(p.FOVModifier)
}

// DamageEvent は Damage Event（エンティティがダメージを受けたときに届く）。
type DamageEvent struct {
	EntityID       int32
	SourceTypeID   int32 // minecraft:damage_type の番号
	SourceCauseID  int32 // 原因のエンティティ ID。無ければ -1（通信では +1 した値、0 が無し）
	SourceDirectID int32 // 直接当たったエンティティ ID（矢など）。無ければ -1
	HasSourcePos   bool
	SourceX        float64
	SourceY        float64
	SourceZ        float64
}

// ReadDamageEvent は Damage Event を読む。
func ReadDamageEvent(d *protocol.Decoder) (DamageEvent, error) {
	var p DamageEvent
	var err error
	if p.EntityID, err = d.ReadVarInt(); err != nil {
		return p, err
	}
	if p.SourceTypeID, err = d.ReadVarInt(); err != nil {
		return p, err
	}
	if p.SourceCauseID, err = d.ReadVarInt(); err != nil {
		return p, err
	}
	p.SourceCauseID--
	if p.SourceDirectID, err = d.ReadVarInt(); err != nil {
		return p, err
	}
	p.SourceDirectID--
	if p.HasSourcePos, err = d.ReadBool(); err != nil {
		return p, err
	}
	if p.HasSourcePos {
		for _, f := range []*float64{&p.SourceX, &p.SourceY, &p.SourceZ} {
			if *f, err = d.ReadDouble(); err != nil {
				return p, err
			}
		}
	}
	return p, nil
}

// WriteDamageEvent は Damage Event を書く（テストの偽のサーバー用）。
func WriteDamageEvent(e *protocol.Encoder, p DamageEvent) {
	e.WriteVarInt(p.EntityID)
	e.WriteVarInt(p.SourceTypeID)
	e.WriteVarInt(p.SourceCauseID + 1)
	e.WriteVarInt(p.SourceDirectID + 1)
	e.WriteBool(p.HasSourcePos)
	if p.HasSourcePos {
		e.WriteDouble(p.SourceX)
		e.WriteDouble(p.SourceY)
		e.WriteDouble(p.SourceZ)
	}
}

// Client Command（serverbound）の Action。
const (
	ClientCommandRespawn      int32 = 0 // 死んだあとのリスポーン
	ClientCommandRequestStats int32 = 1
)
