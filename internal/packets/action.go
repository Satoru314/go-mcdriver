package packets

// 行動（掘る、置く、使う、攻撃する）のパケットと、それに関わる受け取るパケット（1.21.4）。
//
// 送る: Player Action、Use Item On、Use Item、Swing Arm、Interact。
// 受け取る: Acknowledge Block Change、Update Attributes、Entity Effect（Update Mob Effect）、Remove Entity Effect。
// 読む側（Read*）と書く側（Write*）の両方を持つ（偽のサーバーのテストで使うため）。

import (
	"fmt"

	"github.com/Satoru314/go-mcdriver/internal/protocol"
)

// Player Action の Status（本家の ServerboundPlayerActionPacket.Action の順）。
const (
	ActionStartDestroy   int32 = 0 // 掘り始め
	ActionAbortDestroy   int32 = 1 // 掘るのをやめる
	ActionStopDestroy    int32 = 2 // 掘り終わり
	ActionDropAllItems   int32 = 3 // 手に持っているものを全部捨てる
	ActionDropItem       int32 = 4 // 手に持っているものを 1 つ捨てる
	ActionReleaseUseItem int32 = 5 // 使っているもの（弓など）を離す
	ActionSwapItem       int32 = 6 // 左手と入れ替える
)

// 手（InteractionHand）。
const (
	HandMain int32 = 0
	HandOff  int32 = 1
)

// PlayerAction は Player Action（serverbound）。
type PlayerAction struct {
	Status   int32
	Pos      protocol.Position
	Face     int8 // Direction の番号（0 下, 1 上, 2 北, 3 南, 4 西, 5 東）
	Sequence int32
}

// WritePlayerAction は Player Action を書く。
func WritePlayerAction(e *protocol.Encoder, p PlayerAction) error {
	e.WriteVarInt(p.Status)
	if err := e.WritePosition(p.Pos); err != nil {
		return err
	}
	e.WriteInt8(p.Face)
	e.WriteVarInt(p.Sequence)
	return nil
}

// ReadPlayerAction は Player Action を読む。
func ReadPlayerAction(d *protocol.Decoder) (PlayerAction, error) {
	var p PlayerAction
	var err error
	if p.Status, err = d.ReadVarInt(); err != nil {
		return p, err
	}
	if p.Pos, err = d.ReadPosition(); err != nil {
		return p, err
	}
	if p.Face, err = d.ReadInt8(); err != nil {
		return p, err
	}
	p.Sequence, err = d.ReadVarInt()
	return p, err
}

// UseItemOn は Use Item On（serverbound）。ブロックに向かってアイテムを使う（置く、ドアを開けるなど）。
type UseItemOn struct {
	Hand int32
	Pos  protocol.Position
	Face int32
	// CursorX, Y, Z は面の上の指した位置（ブロックの北西の下の角から、0〜1）
	CursorX, CursorY, CursorZ float32
	Inside                    bool // 目がブロックの中にある
	WorldBorderHit            bool // ワールドの境界に当たった（1.21.2 から）
	Sequence                  int32
}

// WriteUseItemOn は Use Item On を書く。
func WriteUseItemOn(e *protocol.Encoder, p UseItemOn) error {
	e.WriteVarInt(p.Hand)
	if err := e.WritePosition(p.Pos); err != nil {
		return err
	}
	e.WriteVarInt(p.Face)
	e.WriteFloat(p.CursorX)
	e.WriteFloat(p.CursorY)
	e.WriteFloat(p.CursorZ)
	e.WriteBool(p.Inside)
	e.WriteBool(p.WorldBorderHit)
	e.WriteVarInt(p.Sequence)
	return nil
}

// ReadUseItemOn は Use Item On を読む。
func ReadUseItemOn(d *protocol.Decoder) (UseItemOn, error) {
	var p UseItemOn
	var err error
	if p.Hand, err = d.ReadVarInt(); err != nil {
		return p, err
	}
	if p.Pos, err = d.ReadPosition(); err != nil {
		return p, err
	}
	if p.Face, err = d.ReadVarInt(); err != nil {
		return p, err
	}
	for _, f := range []*float32{&p.CursorX, &p.CursorY, &p.CursorZ} {
		if *f, err = d.ReadFloat(); err != nil {
			return p, err
		}
	}
	if p.Inside, err = d.ReadBool(); err != nil {
		return p, err
	}
	if p.WorldBorderHit, err = d.ReadBool(); err != nil {
		return p, err
	}
	p.Sequence, err = d.ReadVarInt()
	return p, err
}

// UseItem は Use Item（serverbound）。手に持っているものを使う（食べる、弓を引くなど）。
// 1.21 から向き（Yaw, Pitch）が付き、サーバーはその向きにプレイヤーを向けてから使う。
type UseItem struct {
	Hand       int32
	Sequence   int32
	Yaw, Pitch float32
}

// WriteUseItem は Use Item を書く。
func WriteUseItem(e *protocol.Encoder, p UseItem) {
	e.WriteVarInt(p.Hand)
	e.WriteVarInt(p.Sequence)
	e.WriteFloat(p.Yaw)
	e.WriteFloat(p.Pitch)
}

// ReadUseItem は Use Item を読む。
func ReadUseItem(d *protocol.Decoder) (UseItem, error) {
	var p UseItem
	var err error
	if p.Hand, err = d.ReadVarInt(); err != nil {
		return p, err
	}
	if p.Sequence, err = d.ReadVarInt(); err != nil {
		return p, err
	}
	if p.Yaw, err = d.ReadFloat(); err != nil {
		return p, err
	}
	p.Pitch, err = d.ReadFloat()
	return p, err
}

// Interact の Type。
const (
	InteractInteract   int32 = 0 // 使う（右クリック）
	InteractAttack     int32 = 1 // 攻撃する（左クリック）
	InteractInteractAt int32 = 2 // 指した位置を付けて使う
)

// Interact は Interact（serverbound）。エンティティを使う・攻撃する。
type Interact struct {
	EntityID int32
	Type     int32
	// TargetX, Y, Z は InteractInteractAt のときだけ（エンティティの足元からの位置）
	TargetX, TargetY, TargetZ float32
	// Hand は InteractInteract と InteractInteractAt のときだけ
	Hand     int32
	Sneaking bool
}

// WriteInteract は Interact を書く。
func WriteInteract(e *protocol.Encoder, p Interact) error {
	e.WriteVarInt(p.EntityID)
	e.WriteVarInt(p.Type)
	switch p.Type {
	case InteractInteract:
		e.WriteVarInt(p.Hand)
	case InteractAttack:
	case InteractInteractAt:
		e.WriteFloat(p.TargetX)
		e.WriteFloat(p.TargetY)
		e.WriteFloat(p.TargetZ)
		e.WriteVarInt(p.Hand)
	default:
		return fmt.Errorf("packets: Interact の Type が正しくない: %d", p.Type)
	}
	e.WriteBool(p.Sneaking)
	return nil
}

// ReadInteract は Interact を読む。
func ReadInteract(d *protocol.Decoder) (Interact, error) {
	var p Interact
	var err error
	if p.EntityID, err = d.ReadVarInt(); err != nil {
		return p, err
	}
	if p.Type, err = d.ReadVarInt(); err != nil {
		return p, err
	}
	switch p.Type {
	case InteractInteract:
		if p.Hand, err = d.ReadVarInt(); err != nil {
			return p, err
		}
	case InteractAttack:
	case InteractInteractAt:
		for _, f := range []*float32{&p.TargetX, &p.TargetY, &p.TargetZ} {
			if *f, err = d.ReadFloat(); err != nil {
				return p, err
			}
		}
		if p.Hand, err = d.ReadVarInt(); err != nil {
			return p, err
		}
	default:
		return p, fmt.Errorf("packets: Interact の Type が正しくない: %d", p.Type)
	}
	p.Sneaking, err = d.ReadBool()
	return p, err
}

// 属性の修飾の計算のしかた（AttributeModifier.Operation の番号）。
const (
	ModifierAddValue           int8 = 0 // 足す
	ModifierAddMultipliedBase  int8 = 1 // 元の値 × 合計 を足す
	ModifierAddMultipliedTotal int8 = 2 // (1 + 値) を掛ける
)

// AttributeModifier は属性の修飾 1 つ。
type AttributeModifier struct {
	ID        string // 例 "minecraft:base_attack_speed"
	Amount    float64
	Operation int8
}

// Attribute は属性 1 つ（Update Attributes の 1 行）。
type Attribute struct {
	ID        int32 // minecraft:attribute の番号
	Base      float64
	Modifiers []AttributeModifier
}

// UpdateAttributes は Update Attributes（clientbound）。
type UpdateAttributes struct {
	EntityID   int32
	Attributes []Attribute
}

// maxAttributes と maxModifiers は読むときの数の上限（壊れたパケットで大きな配列を作らないため）。
const (
	maxAttributes = 1024
	maxModifiers  = 1024
)

// ReadUpdateAttributes は Update Attributes を読む。
func ReadUpdateAttributes(d *protocol.Decoder) (UpdateAttributes, error) {
	var p UpdateAttributes
	var err error
	if p.EntityID, err = d.ReadVarInt(); err != nil {
		return p, err
	}
	p.Attributes, err = protocol.ReadPrefixedArray(d, maxAttributes, func(d *protocol.Decoder) (Attribute, error) {
		var a Attribute
		var err error
		if a.ID, err = d.ReadVarInt(); err != nil {
			return a, err
		}
		if a.Base, err = d.ReadDouble(); err != nil {
			return a, err
		}
		a.Modifiers, err = protocol.ReadPrefixedArray(d, maxModifiers, func(d *protocol.Decoder) (AttributeModifier, error) {
			var m AttributeModifier
			var err error
			if m.ID, err = d.ReadIdentifier(); err != nil {
				return m, err
			}
			if m.Amount, err = d.ReadDouble(); err != nil {
				return m, err
			}
			m.Operation, err = d.ReadInt8()
			return m, err
		})
		return a, err
	})
	return p, err
}

// WriteUpdateAttributes は Update Attributes を書く。
func WriteUpdateAttributes(e *protocol.Encoder, p UpdateAttributes) error {
	e.WriteVarInt(p.EntityID)
	return protocol.WritePrefixedArray(e, p.Attributes, func(e *protocol.Encoder, a Attribute) error {
		e.WriteVarInt(a.ID)
		e.WriteDouble(a.Base)
		return protocol.WritePrefixedArray(e, a.Modifiers, func(e *protocol.Encoder, m AttributeModifier) error {
			if err := e.WriteIdentifier(m.ID); err != nil {
				return err
			}
			e.WriteDouble(m.Amount)
			e.WriteInt8(m.Operation)
			return nil
		})
	})
}

// MobEffect は Entity Effect（clientbound、本家の ClientboundUpdateMobEffectPacket）。
type MobEffect struct {
	EntityID  int32
	EffectID  int32 // minecraft:mob_effect の番号
	Amplifier int32 // 強さ（0 が I）
	Duration  int32 // 残りのティック数（-1 は無限）
	Flags     uint8
}

// ReadMobEffect は Entity Effect を読む。
func ReadMobEffect(d *protocol.Decoder) (MobEffect, error) {
	var p MobEffect
	var err error
	if p.EntityID, err = d.ReadVarInt(); err != nil {
		return p, err
	}
	if p.EffectID, err = d.ReadVarInt(); err != nil {
		return p, err
	}
	if p.Amplifier, err = d.ReadVarInt(); err != nil {
		return p, err
	}
	if p.Duration, err = d.ReadVarInt(); err != nil {
		return p, err
	}
	p.Flags, err = d.ReadUByte()
	return p, err
}

// WriteMobEffect は Entity Effect を書く。
func WriteMobEffect(e *protocol.Encoder, p MobEffect) {
	e.WriteVarInt(p.EntityID)
	e.WriteVarInt(p.EffectID)
	e.WriteVarInt(p.Amplifier)
	e.WriteVarInt(p.Duration)
	e.WriteUByte(p.Flags)
}
