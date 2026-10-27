package packets

import (
	"github.com/Satoru314/go-mcdriver/internal/protocol"
)

// LoginPlay は Login (play)。ゲームに入ったときに 1 度届く。
type LoginPlay struct {
	EntityID            int32
	Hardcore            bool
	DimensionNames      []string
	MaxPlayers          int32
	ViewDistance        int32
	SimulationDistance  int32
	ReducedDebugInfo    bool
	EnableRespawnScreen bool
	DoLimitedCrafting   bool
	DimensionType       int32 // minecraft:dimension_type の番号
	DimensionName       string
	HashedSeed          int64
	GameMode            uint8
	PreviousGameMode    int8 // -1 なら無し
	IsDebug             bool
	IsFlat              bool
	HasDeathLocation    bool
	DeathDimension      string
	DeathLocation       protocol.Position
	PortalCooldown      int32
	SeaLevel            int32 // 1.21.2 から
	EnforcesSecureChat  bool
}

// ReadLoginPlay は Login (play) を読む。
func ReadLoginPlay(d *protocol.Decoder) (LoginPlay, error) {
	var p LoginPlay
	var err error
	if p.EntityID, err = d.ReadInt(); err != nil {
		return p, err
	}
	if p.Hardcore, err = d.ReadBool(); err != nil {
		return p, err
	}
	if p.DimensionNames, err = protocol.ReadPrefixedArray(d, 1024, (*protocol.Decoder).ReadIdentifier); err != nil {
		return p, err
	}
	if p.MaxPlayers, err = d.ReadVarInt(); err != nil {
		return p, err
	}
	if p.ViewDistance, err = d.ReadVarInt(); err != nil {
		return p, err
	}
	if p.SimulationDistance, err = d.ReadVarInt(); err != nil {
		return p, err
	}
	if p.ReducedDebugInfo, err = d.ReadBool(); err != nil {
		return p, err
	}
	if p.EnableRespawnScreen, err = d.ReadBool(); err != nil {
		return p, err
	}
	if p.DoLimitedCrafting, err = d.ReadBool(); err != nil {
		return p, err
	}
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
	p.EnforcesSecureChat, err = d.ReadBool()
	return p, err
}

// WriteLoginPlay は Login (play) を書く（テストの偽のサーバー用）。
func WriteLoginPlay(e *protocol.Encoder, p LoginPlay) error {
	e.WriteInt(p.EntityID)
	e.WriteBool(p.Hardcore)
	if err := protocol.WritePrefixedArray(e, p.DimensionNames, (*protocol.Encoder).WriteIdentifier); err != nil {
		return err
	}
	e.WriteVarInt(p.MaxPlayers)
	e.WriteVarInt(p.ViewDistance)
	e.WriteVarInt(p.SimulationDistance)
	e.WriteBool(p.ReducedDebugInfo)
	e.WriteBool(p.EnableRespawnScreen)
	e.WriteBool(p.DoLimitedCrafting)
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
	e.WriteBool(p.EnforcesSecureChat)
	return nil
}

// Teleport Flags（Synchronize Player Position の Flags）。立っているビットの値は相対（今の値に足す）。
const (
	RelX           int32 = 0x0001
	RelY           int32 = 0x0002
	RelZ           int32 = 0x0004
	RelYaw         int32 = 0x0008
	RelPitch       int32 = 0x0010
	RelVelX        int32 = 0x0020
	RelVelY        int32 = 0x0040
	RelVelZ        int32 = 0x0080
	RotateVelocity int32 = 0x0100 // 速度を足す前に、向きの変化に合わせて回す
)

// PlayerPosition は Synchronize Player Position（1.21.2 からの形）。サーバーからの位置の強制修正。
type PlayerPosition struct {
	TeleportID       int32
	X, Y, Z          float64
	VelX, VelY, VelZ float64
	Yaw, Pitch       float32
	Flags            int32
}

// ReadPlayerPosition は Synchronize Player Position を読む。
func ReadPlayerPosition(d *protocol.Decoder) (PlayerPosition, error) {
	var p PlayerPosition
	var err error
	if p.TeleportID, err = d.ReadVarInt(); err != nil {
		return p, err
	}
	for _, f := range []*float64{&p.X, &p.Y, &p.Z, &p.VelX, &p.VelY, &p.VelZ} {
		if *f, err = d.ReadDouble(); err != nil {
			return p, err
		}
	}
	if p.Yaw, err = d.ReadFloat(); err != nil {
		return p, err
	}
	if p.Pitch, err = d.ReadFloat(); err != nil {
		return p, err
	}
	p.Flags, err = d.ReadInt()
	return p, err
}

// WritePlayerPosition は Synchronize Player Position を書く（テストの偽のサーバー用）。
func WritePlayerPosition(e *protocol.Encoder, p PlayerPosition) {
	e.WriteVarInt(p.TeleportID)
	for _, f := range []float64{p.X, p.Y, p.Z, p.VelX, p.VelY, p.VelZ} {
		e.WriteDouble(f)
	}
	e.WriteFloat(p.Yaw)
	e.WriteFloat(p.Pitch)
	e.WriteInt(p.Flags)
}

// MovePlayer の Flags（1.21.2 から）。
const (
	MoveOnGround           uint8 = 0x01
	MovePushingAgainstWall uint8 = 0x02
)

// MovePlayerPosRot は Set Player Position and Rotation（serverbound）。Y は足の高さ。
type MovePlayerPosRot struct {
	X, Y, Z    float64
	Yaw, Pitch float32
	Flags      uint8
}

// WriteMovePlayerPosRot は Set Player Position and Rotation を書く。
func WriteMovePlayerPosRot(e *protocol.Encoder, p MovePlayerPosRot) {
	e.WriteDouble(p.X)
	e.WriteDouble(p.Y)
	e.WriteDouble(p.Z)
	e.WriteFloat(p.Yaw)
	e.WriteFloat(p.Pitch)
	e.WriteUByte(p.Flags)
}

// ReadMovePlayerPosRot は Set Player Position and Rotation を読む（テストの偽のサーバー用）。
func ReadMovePlayerPosRot(d *protocol.Decoder) (MovePlayerPosRot, error) {
	var p MovePlayerPosRot
	var err error
	if p.X, err = d.ReadDouble(); err != nil {
		return p, err
	}
	if p.Y, err = d.ReadDouble(); err != nil {
		return p, err
	}
	if p.Z, err = d.ReadDouble(); err != nil {
		return p, err
	}
	if p.Yaw, err = d.ReadFloat(); err != nil {
		return p, err
	}
	if p.Pitch, err = d.ReadFloat(); err != nil {
		return p, err
	}
	p.Flags, err = d.ReadUByte()
	return p, err
}

// Apply はサーバーからの位置の強制修正を、今の位置と向きに当てはめた結果を返す。
// 速度は今は扱わない（物理の単位で扱う）。
func (p PlayerPosition) Apply(x, y, z float64, yaw, pitch float32) (nx, ny, nz float64, nyaw, npitch float32) {
	nx, ny, nz, nyaw, npitch = p.X, p.Y, p.Z, p.Yaw, p.Pitch
	if p.Flags&RelX != 0 {
		nx += x
	}
	if p.Flags&RelY != 0 {
		ny += y
	}
	if p.Flags&RelZ != 0 {
		nz += z
	}
	if p.Flags&RelYaw != 0 {
		nyaw += yaw
	}
	if p.Flags&RelPitch != 0 {
		npitch += pitch
	}
	// 公式のクライアントと同じく、縦の向きは -90〜90 に収める
	npitch = max(-90, min(90, npitch))
	return
}
