package packets

import (
	"github.com/Satoru314/go-mcdriver/internal/protocol"
)

// Respawn は Respawn（リスポーンや、別のディメンションへの移動のときに届く）。
// 前半（DimensionType から SeaLevel まで）は Login (play) の後半と同じ形。
type Respawn struct {
	DimensionType    int32 // minecraft:dimension_type の番号
	DimensionName    string
	HashedSeed       int64
	GameMode         uint8
	PreviousGameMode int8 // -1 なら無し
	IsDebug          bool
	IsFlat           bool
	HasDeathLocation bool
	DeathDimension   string
	DeathLocation    protocol.Position
	PortalCooldown   int32
	SeaLevel         int32
	DataKept         uint8 // ビット 0: 属性を残す、ビット 1: エンティティのデータを残す
}

// ReadRespawn は Respawn を読む。
func ReadRespawn(d *protocol.Decoder) (Respawn, error) {
	var p Respawn
	var err error
	if p.DimensionType, err = d.ReadVarInt(); err != nil {
		return p, err
	}
	if p.DimensionName, err = d.ReadIdentifier(); err != nil {
		return p, err
	}
	if p.HashedSeed, err = d.ReadLong(); err != nil {
		return p, err
	}
	if p.GameMode, err = d.ReadUByte(); err != nil {
		return p, err
	}
	if p.PreviousGameMode, err = d.ReadInt8(); err != nil {
		return p, err
	}
	if p.IsDebug, err = d.ReadBool(); err != nil {
		return p, err
	}
	if p.IsFlat, err = d.ReadBool(); err != nil {
		return p, err
	}
	if p.HasDeathLocation, err = d.ReadBool(); err != nil {
		return p, err
	}
	if p.HasDeathLocation {
		if p.DeathDimension, err = d.ReadIdentifier(); err != nil {
			return p, err
		}
		if p.DeathLocation, err = d.ReadPosition(); err != nil {
			return p, err
		}
	}
	if p.PortalCooldown, err = d.ReadVarInt(); err != nil {
		return p, err
	}
	if p.SeaLevel, err = d.ReadVarInt(); err != nil {
		return p, err
	}
	p.DataKept, err = d.ReadUByte()
	return p, err
}

// WriteRespawn は Respawn を書く（テストの偽のサーバー用）。
func WriteRespawn(e *protocol.Encoder, p Respawn) error {
	e.WriteVarInt(p.DimensionType)
	if err := e.WriteIdentifier(p.DimensionName); err != nil {
		return err
	}
	e.WriteLong(p.HashedSeed)
	e.WriteUByte(p.GameMode)
	e.WriteInt8(p.PreviousGameMode)
	e.WriteBool(p.IsDebug)
	e.WriteBool(p.IsFlat)
	e.WriteBool(p.HasDeathLocation)
	if p.HasDeathLocation {
		if err := e.WriteIdentifier(p.DeathDimension); err != nil {
			return err
		}
		if err := e.WritePosition(p.DeathLocation); err != nil {
			return err
		}
	}
	e.WriteVarInt(p.PortalCooldown)
	e.WriteVarInt(p.SeaLevel)
	e.WriteUByte(p.DataKept)
	return nil
}
