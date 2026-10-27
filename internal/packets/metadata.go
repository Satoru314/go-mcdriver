package packets

// Set Entity Metadata（エンティティのメタデータ）の読み込み。
//
// 中身は (番号 UByte, 型 VarInt, 値) の並びで、番号 0xff で終わる。型の番号は 1.21.4 の
// EntityDataSerializers の登録順。使う型（Byte, VarInt, Float, Bool, String, Slot など）は値を作り、
// ほかの型は読み飛ばす。

import (
	"fmt"

	"github.com/Satoru314/go-mcdriver/internal/protocol"
)

// メタデータの型の番号（1.21.4）。
const (
	MetaByte              int32 = 0
	MetaVarInt            int32 = 1
	MetaVarLong           int32 = 2
	MetaFloat             int32 = 3
	MetaString            int32 = 4
	MetaTextComponent     int32 = 5
	MetaOptTextComponent  int32 = 6
	MetaSlot              int32 = 7
	MetaBool              int32 = 8
	MetaRotations         int32 = 9
	MetaPosition          int32 = 10
	MetaOptPosition       int32 = 11
	MetaDirection         int32 = 12
	MetaOptUUID           int32 = 13
	MetaBlockState        int32 = 14
	MetaOptBlockState     int32 = 15
	MetaNBT               int32 = 16
	MetaParticle          int32 = 17
	MetaParticles         int32 = 18
	MetaVillagerData      int32 = 19
	MetaOptVarInt         int32 = 20
	MetaPose              int32 = 21
	MetaCatVariant        int32 = 22
	MetaWolfVariant       int32 = 23
	MetaFrogVariant       int32 = 24
	MetaOptGlobalPosition int32 = 25
	MetaPaintingVariant   int32 = 26
	MetaSnifferState      int32 = 27
	MetaArmadilloState    int32 = 28
	MetaVector3           int32 = 29
	MetaQuaternion        int32 = 30
)

// MetadataEntry はメタデータの 1 項目。
// Value は型によって uint8（Byte）、int32（VarInt, Direction, BlockState, Pose など番号のもの）、int64（VarLong）、
// float32（Float）、bool（Bool）、string（String）、Slot（Slot）。それ以外の型は読み飛ばして nil。
type MetadataEntry struct {
	Index uint8
	Type  int32
	Value any
}

// metadataEnd はメタデータの終わりの印。
const metadataEnd = 0xff

// SetEntityMetadata は Set Entity Metadata。
type SetEntityMetadata struct {
	EntityID int32
	Entries  []MetadataEntry
}

// ReadSetEntityMetadata は Set Entity Metadata を読む。
// 途中で読めない型（ErrUnsupportedData）や壊れたデータに出会ったら、そこまでに読めた項目とエラーを返す。
// パケットの枠は別なので、使う側は読めた項目だけ使ってよい。
func ReadSetEntityMetadata(d *protocol.Decoder) (SetEntityMetadata, error) {
	var p SetEntityMetadata
	var err error
	if p.EntityID, err = d.ReadVarInt(); err != nil {
		return p, err
	}
	for {
		idx, err := d.ReadUByte()
		if err != nil {
			return p, err
		}
		if idx == metadataEnd {
			return p, nil
		}
		t, err := d.ReadVarInt()
		if err != nil {
			return p, err
		}
		v, err := readMetaValue(d, t)
		if err != nil {
			return p, fmt.Errorf("メタデータ %d（型 %d）: %w", idx, t, err)
		}
		p.Entries = append(p.Entries, MetadataEntry{Index: idx, Type: t, Value: v})
	}
}

// readMetaValue は型 t の値を 1 つ読む。
func readMetaValue(d *protocol.Decoder, t int32) (any, error) {
	switch t {
	case MetaByte:
		return d.ReadUByte()
	case MetaVarInt, MetaDirection, MetaBlockState, MetaOptBlockState, MetaOptVarInt, MetaPose,
		MetaCatVariant, MetaFrogVariant, MetaSnifferState, MetaArmadilloState:
		return d.ReadVarInt()
	case MetaVarLong:
		return d.ReadVarLong()
	case MetaFloat:
		return d.ReadFloat()
	case MetaString:
		return d.ReadString(32767)
	case MetaBool:
		return d.ReadBool()
	case MetaSlot:
		return ReadSlot(d)
	}
	s := skipper{d: d}
	switch t {
	case MetaTextComponent, MetaNBT:
		s.nbt()
	case MetaOptTextComponent:
		s.opt(s.nbt)
	case MetaRotations, MetaVector3:
		s.bytes(12)
	case MetaQuaternion:
		s.bytes(16)
	case MetaPosition:
		s.bytes(8)
	case MetaOptPosition:
		s.opt(func() { s.bytes(8) })
	case MetaOptUUID:
		s.opt(func() { s.bytes(16) })
	case MetaParticle:
		s.particle()
	case MetaParticles:
		s.list(s.particle)
	case MetaVillagerData:
		s.varint()
		s.varint()
		s.varint()
	case MetaWolfVariant:
		s.holder("wolf_variant", func() {
			s.ident() // 野生の見た目
			s.ident() // 飼われた見た目
			s.ident() // 怒った見た目
			s.holderSet()
		})
	case MetaPaintingVariant:
		s.holder("painting_variant", func() {
			s.varint() // 幅
			s.varint() // 高さ
			s.ident()  // 絵
			s.opt(s.nbt)
			s.opt(s.nbt)
		})
	case MetaOptGlobalPosition:
		s.opt(func() { s.ident(); s.bytes(8) })
	default:
		return nil, fmt.Errorf("%w: メタデータの型 %d", ErrUnsupportedData, t)
	}
	return nil, s.err
}

// particle は粒子 1 つ（種類の番号 + 種類ごとの中身、1.21.4）を読み飛ばす。
func (s *skipper) particle() {
	t := s.varint()
	if s.err != nil {
		return
	}
	switch registryName(particleTypes, t) {
	case "block", "block_marker", "falling_dust", "dust_pillar", "block_crumble":
		s.varint() // ブロックの状態
	case "dust":
		s.bytes(4 + 4) // 色（int）、大きさ（float）
	case "dust_color_transition":
		s.bytes(4 + 4 + 4) // 始めの色、終わりの色、大きさ
	case "entity_effect":
		s.bytes(4) // 色（ARGB）
	case "item":
		s.slot()
	case "vibration":
		switch registryName(positionSourceTypes, s.varint()) {
		case "block":
			s.bytes(8)
		case "entity":
			s.varint()
			s.bytes(4)
		default:
			s.fail(fmt.Errorf("%w: position_source_type", ErrUnsupportedData))
		}
		s.varint() // 着くまでのティック
	case "trail":
		s.bytes(8 * 3) // 行き先
		s.bytes(4)     // 色
		s.varint()     // 長さ
	case "sculk_charge":
		s.bytes(4)
	case "shriek":
		s.varint()
	case "":
		s.fail(fmt.Errorf("%w: 粒子の種類 %d", ErrUnsupportedData, t))
	default:
		// ほかの粒子は中身なし
	}
}

var positionSourceTypes = registryEntries("position_source_type")

// WriteMetadataEnd はメタデータの終わりの印を書く（テストの偽のサーバー用）。
func WriteMetadataEnd(e *protocol.Encoder) { e.WriteUByte(metadataEnd) }
