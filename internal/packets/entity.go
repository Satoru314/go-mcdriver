package packets

// エンティティ（生き物と物）とプレイヤーの一覧のパケット（1.21.4）。

import (
	"github.com/Satoru314/go-mcdriver/internal/protocol"
)

// SpawnEntity は Spawn Entity。速度は 1/8000 ブロック/ティックの整数。
type SpawnEntity struct {
	EntityID            int32
	UUID                protocol.UUID
	Type                int32 // entity_type の番号
	X, Y, Z             float64
	Pitch, Yaw, HeadYaw protocol.Angle
	Data                int32 // 種類ごとの追加の値（落ちてくるブロックの状態など）
	VelX, VelY, VelZ    int16
}

// ReadSpawnEntity は Spawn Entity を読む。
func ReadSpawnEntity(d *protocol.Decoder) (SpawnEntity, error) {
	var p SpawnEntity
	var err error
	if p.EntityID, err = d.ReadVarInt(); err != nil {
		return p, err
	}
	if p.UUID, err = d.ReadUUID(); err != nil {
		return p, err
	}
	if p.Type, err = d.ReadVarInt(); err != nil {
		return p, err
	}
	for _, f := range []*float64{&p.X, &p.Y, &p.Z} {
		if *f, err = d.ReadDouble(); err != nil {
			return p, err
		}
	}
	for _, a := range []*protocol.Angle{&p.Pitch, &p.Yaw, &p.HeadYaw} {
		if *a, err = d.ReadAngle(); err != nil {
			return p, err
		}
	}
	if p.Data, err = d.ReadVarInt(); err != nil {
		return p, err
	}
	for _, v := range []*int16{&p.VelX, &p.VelY, &p.VelZ} {
		if *v, err = d.ReadShort(); err != nil {
			return p, err
		}
	}
	return p, nil
}

// WriteSpawnEntity は Spawn Entity を書く（テストの偽のサーバー用）。
func WriteSpawnEntity(e *protocol.Encoder, p SpawnEntity) {
	e.WriteVarInt(p.EntityID)
	e.WriteUUID(p.UUID)
	e.WriteVarInt(p.Type)
	e.WriteDouble(p.X)
	e.WriteDouble(p.Y)
	e.WriteDouble(p.Z)
	e.WriteAngle(p.Pitch)
	e.WriteAngle(p.Yaw)
	e.WriteAngle(p.HeadYaw)
	e.WriteVarInt(p.Data)
	e.WriteShort(p.VelX)
	e.WriteShort(p.VelY)
	e.WriteShort(p.VelZ)
}

// maxRemoveEntities は Remove Entities 1 つで消すエンティティの数の上限（壊れたデータで大きな配列を作らないため）。
const maxRemoveEntities = 1 << 16

// ReadRemoveEntities は Remove Entities を読み、消すエンティティ ID を返す。
func ReadRemoveEntities(d *protocol.Decoder) ([]int32, error) {
	return protocol.ReadPrefixedArray(d, maxRemoveEntities, (*protocol.Decoder).ReadVarInt)
}

// WriteRemoveEntities は Remove Entities を書く（テストの偽のサーバー用）。
func WriteRemoveEntities(e *protocol.Encoder, ids []int32) {
	e.WriteVarInt(int32(len(ids)))
	for _, id := range ids {
		e.WriteVarInt(id)
	}
}

// MoveEntity は Update Entity Position / Position and Rotation / Rotation のどれか。
// 位置の差分は 1/4096 ブロックの整数。
type MoveEntity struct {
	EntityID   int32
	HasPos     bool // 位置の差分がある（Position, Position and Rotation）
	HasRot     bool // 向きがある（Position and Rotation, Rotation）
	DX, DY, DZ int16
	Yaw, Pitch protocol.Angle
	OnGround   bool
}

// ReadMoveEntity は Update Entity Position（pos）、Rotation（rot）、Position and Rotation（両方）を読む。
func ReadMoveEntity(d *protocol.Decoder, pos, rot bool) (MoveEntity, error) {
	p := MoveEntity{HasPos: pos, HasRot: rot}
	var err error
	if p.EntityID, err = d.ReadVarInt(); err != nil {
		return p, err
	}
	if pos {
		for _, v := range []*int16{&p.DX, &p.DY, &p.DZ} {
			if *v, err = d.ReadShort(); err != nil {
				return p, err
			}
		}
	}
	if rot {
		if p.Yaw, err = d.ReadAngle(); err != nil {
			return p, err
		}
		if p.Pitch, err = d.ReadAngle(); err != nil {
			return p, err
		}
	}
	p.OnGround, err = d.ReadBool()
	return p, err
}

// WriteMoveEntity は p.HasPos / p.HasRot に合った形で書く（テストの偽のサーバー用）。
func WriteMoveEntity(e *protocol.Encoder, p MoveEntity) {
	e.WriteVarInt(p.EntityID)
	if p.HasPos {
		e.WriteShort(p.DX)
		e.WriteShort(p.DY)
		e.WriteShort(p.DZ)
	}
	if p.HasRot {
		e.WriteAngle(p.Yaw)
		e.WriteAngle(p.Pitch)
	}
	e.WriteBool(p.OnGround)
}

// EntityPositionSync は Entity Position Sync（1.21.2 から）と Teleport Entity の共通の形。
// 位置は絶対、速度はブロック/ティック、向きは度。Flags は Teleport Entity だけ（RelX などの組み合わせ）。
type EntityPositionSync struct {
	EntityID         int32
	X, Y, Z          float64
	VelX, VelY, VelZ float64
	Yaw, Pitch       float32
	Flags            int32
	OnGround         bool
}

// ReadEntityPositionSync は Entity Position Sync を読む。teleport なら Teleport Entity（向きのあとに Flags（Int）がある）を読む。
func ReadEntityPositionSync(d *protocol.Decoder, teleport bool) (EntityPositionSync, error) {
	var p EntityPositionSync
	var err error
	if p.EntityID, err = d.ReadVarInt(); err != nil {
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
	if teleport {
		if p.Flags, err = d.ReadInt(); err != nil {
			return p, err
		}
	}
	p.OnGround, err = d.ReadBool()
	return p, err
}

// WriteEntityPositionSync は Entity Position Sync（teleport なら Teleport Entity）を書く（テストの偽のサーバー用）。
func WriteEntityPositionSync(e *protocol.Encoder, p EntityPositionSync, teleport bool) {
	e.WriteVarInt(p.EntityID)
	for _, f := range []float64{p.X, p.Y, p.Z, p.VelX, p.VelY, p.VelZ} {
		e.WriteDouble(f)
	}
	e.WriteFloat(p.Yaw)
	e.WriteFloat(p.Pitch)
	if teleport {
		e.WriteInt(p.Flags)
	}
	e.WriteBool(p.OnGround)
}

// SetEntityMotion は Set Entity Velocity。速度は 1/8000 ブロック/ティックの整数。
type SetEntityMotion struct {
	EntityID         int32
	VelX, VelY, VelZ int16
}

// ReadSetEntityMotion は Set Entity Velocity を読む。
func ReadSetEntityMotion(d *protocol.Decoder) (SetEntityMotion, error) {
	var p SetEntityMotion
	var err error
	if p.EntityID, err = d.ReadVarInt(); err != nil {
		return p, err
	}
	for _, v := range []*int16{&p.VelX, &p.VelY, &p.VelZ} {
		if *v, err = d.ReadShort(); err != nil {
			return p, err
		}
	}
	return p, nil
}

// WriteSetEntityMotion は Set Entity Velocity を書く（テストの偽のサーバー用）。
func WriteSetEntityMotion(e *protocol.Encoder, p SetEntityMotion) {
	e.WriteVarInt(p.EntityID)
	e.WriteShort(p.VelX)
	e.WriteShort(p.VelY)
	e.WriteShort(p.VelZ)
}

// RotateHead は Set Head Rotation。
type RotateHead struct {
	EntityID int32
	HeadYaw  protocol.Angle
}

// ReadRotateHead は Set Head Rotation を読む。
func ReadRotateHead(d *protocol.Decoder) (RotateHead, error) {
	var p RotateHead
	var err error
	if p.EntityID, err = d.ReadVarInt(); err != nil {
		return p, err
	}
	p.HeadYaw, err = d.ReadAngle()
	return p, err
}

// WriteRotateHead は Set Head Rotation を書く（テストの偽のサーバー用）。
func WriteRotateHead(e *protocol.Encoder, p RotateHead) {
	e.WriteVarInt(p.EntityID)
	e.WriteAngle(p.HeadYaw)
}

// ---- プレイヤーの一覧（タブの一覧）----

// Player Info Update の Actions のビット（1.21.4）。
const (
	PlayerInfoAddPlayer         uint8 = 0x01
	PlayerInfoInitializeChat    uint8 = 0x02
	PlayerInfoUpdateGameMode    uint8 = 0x04
	PlayerInfoUpdateListed      uint8 = 0x08
	PlayerInfoUpdateLatency     uint8 = 0x10
	PlayerInfoUpdateDisplayName uint8 = 0x20
	PlayerInfoUpdateListOrder   uint8 = 0x40
	PlayerInfoUpdateHat         uint8 = 0x80
)

// PlayerInfoEntry は Player Info Update の 1 人分。Actions に無い項目は 0 のまま。
type PlayerInfoEntry struct {
	UUID protocol.UUID
	// Name は名前（PlayerInfoAddPlayer のとき）
	Name     string
	GameMode int32
	Listed   bool
	Latency  int32
	// 一覧に出す名前（Update Display Name）、並び順、帽子の表示は読み飛ばす
}

// PlayerInfoUpdate は Player Info Update。
type PlayerInfoUpdate struct {
	Actions uint8
	Entries []PlayerInfoEntry
}

// maxPlayerInfo は 1 つのパケットのプレイヤーの数と、プロフィールの属性の数の上限。
const (
	maxPlayerInfo      = 1 << 14
	maxProfileProperty = 16
)

// ReadPlayerInfoUpdate は Player Info Update を読む。
func ReadPlayerInfoUpdate(d *protocol.Decoder) (PlayerInfoUpdate, error) {
	var p PlayerInfoUpdate
	var err error
	if p.Actions, err = d.ReadUByte(); err != nil {
		return p, err
	}
	n, err := d.ReadLength(maxPlayerInfo)
	if err != nil {
		return p, err
	}
	p.Entries = make([]PlayerInfoEntry, 0, min(n, d.Remaining()/16))
	for range n {
		var en PlayerInfoEntry
		if en.UUID, err = d.ReadUUID(); err != nil {
			return p, err
		}
		s := skipper{d: d}
		if p.Actions&PlayerInfoAddPlayer != 0 {
			if en.Name, err = d.ReadString(16); err != nil {
				return p, err
			}
			pn, err := d.ReadLength(maxProfileProperty)
			if err != nil {
				return p, err
			}
			for range pn {
				s.str(64)
				s.str(32767)
				s.opt(func() { s.str(1024) })
			}
		}
		if p.Actions&PlayerInfoInitializeChat != 0 {
			s.opt(func() {
				s.bytes(16) // 会話の ID
				s.bytes(8)  // 期限
				if _, err := d.ReadPrefixedBytes(512); err != nil {
					s.fail(err)
				}
				if s.err == nil {
					if _, err := d.ReadPrefixedBytes(4096); err != nil {
						s.fail(err)
					}
				}
			})
		}
		if s.err != nil {
			return p, s.err
		}
		if p.Actions&PlayerInfoUpdateGameMode != 0 {
			if en.GameMode, err = d.ReadVarInt(); err != nil {
				return p, err
			}
		}
		if p.Actions&PlayerInfoUpdateListed != 0 {
			if en.Listed, err = d.ReadBool(); err != nil {
				return p, err
			}
		}
		if p.Actions&PlayerInfoUpdateLatency != 0 {
			if en.Latency, err = d.ReadVarInt(); err != nil {
				return p, err
			}
		}
		if p.Actions&PlayerInfoUpdateDisplayName != 0 {
			s.opt(s.nbt)
			if s.err != nil {
				return p, s.err
			}
		}
		if p.Actions&PlayerInfoUpdateListOrder != 0 {
			if _, err = d.ReadVarInt(); err != nil {
				return p, err
			}
		}
		if p.Actions&PlayerInfoUpdateHat != 0 {
			if _, err = d.ReadBool(); err != nil {
				return p, err
			}
		}
		p.Entries = append(p.Entries, en)
	}
	return p, nil
}

// WritePlayerInfoUpdate は Player Info Update を書く（テストの偽のサーバー用）。
// 属性・会話・一覧に出す名前は「無し」で書く。
func WritePlayerInfoUpdate(e *protocol.Encoder, p PlayerInfoUpdate) error {
	e.WriteUByte(p.Actions)
	e.WriteVarInt(int32(len(p.Entries)))
	for _, en := range p.Entries {
		e.WriteUUID(en.UUID)
		if p.Actions&PlayerInfoAddPlayer != 0 {
			if err := e.WriteString(en.Name, 16); err != nil {
				return err
			}
			e.WriteVarInt(0)
		}
		if p.Actions&PlayerInfoInitializeChat != 0 {
			e.WriteBool(false)
		}
		if p.Actions&PlayerInfoUpdateGameMode != 0 {
			e.WriteVarInt(en.GameMode)
		}
		if p.Actions&PlayerInfoUpdateListed != 0 {
			e.WriteBool(en.Listed)
		}
		if p.Actions&PlayerInfoUpdateLatency != 0 {
			e.WriteVarInt(en.Latency)
		}
		if p.Actions&PlayerInfoUpdateDisplayName != 0 {
			e.WriteBool(false)
		}
		if p.Actions&PlayerInfoUpdateListOrder != 0 {
			e.WriteVarInt(0)
		}
		if p.Actions&PlayerInfoUpdateHat != 0 {
			e.WriteBool(false)
		}
	}
	return nil
}

// ReadPlayerInfoRemove は Player Info Remove を読み、一覧から消すプレイヤーの UUID を返す。
func ReadPlayerInfoRemove(d *protocol.Decoder) ([]protocol.UUID, error) {
	return protocol.ReadPrefixedArray(d, maxPlayerInfo, (*protocol.Decoder).ReadUUID)
}

// WritePlayerInfoRemove は Player Info Remove を書く（テストの偽のサーバー用）。
func WritePlayerInfoRemove(e *protocol.Encoder, ids []protocol.UUID) {
	e.WriteVarInt(int32(len(ids)))
	for _, u := range ids {
		e.WriteUUID(u)
	}
}
