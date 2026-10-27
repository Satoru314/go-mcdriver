package packets

// 持ち物と容器のパケット（1.21.4）。1.21.2 から窓の番号（Window ID）は VarInt。

import (
	"github.com/Satoru314/go-mcdriver/internal/nbt"
	"github.com/Satoru314/go-mcdriver/internal/protocol"
)

// MaxWindowSlots は 1 つの窓のスロットの数の上限（読むときの確かめ）。
const MaxWindowSlots = 1024

// OpenScreen は Open Screen。容器の窓が開いた。
type OpenScreen struct {
	WindowID int32
	Type     int32 // minecraft:menu の番号
	Title    any   // 文字の部品（NBT を読んだ値）
}

// ReadOpenScreen は Open Screen を読む。
func ReadOpenScreen(d *protocol.Decoder) (OpenScreen, error) {
	var p OpenScreen
	var err error
	if p.WindowID, err = d.ReadVarInt(); err != nil {
		return p, err
	}
	if p.Type, err = d.ReadVarInt(); err != nil {
		return p, err
	}
	p.Title, err = nbt.ReadNetwork(d)
	return p, err
}

// WriteOpenScreen は Open Screen を書く（テストの偽のサーバー用）。
func WriteOpenScreen(e *protocol.Encoder, p OpenScreen) error {
	e.WriteVarInt(p.WindowID)
	e.WriteVarInt(p.Type)
	return nbt.WriteNetwork(e, p.Title)
}

// ContainerContent は Set Container Content。窓の全スロットと、カーソルが持っているもの。
type ContainerContent struct {
	WindowID int32
	StateID  int32
	Slots    []Slot
	Carried  Slot
}

// ReadContainerContent は Set Container Content を読む。
func ReadContainerContent(d *protocol.Decoder) (ContainerContent, error) {
	var p ContainerContent
	var err error
	if p.WindowID, err = d.ReadVarInt(); err != nil {
		return p, err
	}
	if p.StateID, err = d.ReadVarInt(); err != nil {
		return p, err
	}
	if p.Slots, err = ReadSlots(d, MaxWindowSlots); err != nil {
		return p, err
	}
	p.Carried, err = ReadSlot(d)
	return p, err
}

// WriteContainerContent は Set Container Content を書く（テストの偽のサーバー用）。
func WriteContainerContent(e *protocol.Encoder, p ContainerContent) {
	e.WriteVarInt(p.WindowID)
	e.WriteVarInt(p.StateID)
	WriteSlots(e, p.Slots)
	WriteSlot(e, p.Carried)
}

// ContainerSlot は Set Container Slot。窓のスロット 1 つが変わった。
// WindowID が -1 でスロットが -1 ならカーソル（古い形）、-2 なら Slot は持ち物の番号（古い形）。
type ContainerSlot struct {
	WindowID int32
	StateID  int32
	Slot     int16
	Item     Slot
}

// ReadContainerSlot は Set Container Slot を読む。
func ReadContainerSlot(d *protocol.Decoder) (ContainerSlot, error) {
	var p ContainerSlot
	var err error
	if p.WindowID, err = d.ReadVarInt(); err != nil {
		return p, err
	}
	if p.StateID, err = d.ReadVarInt(); err != nil {
		return p, err
	}
	if p.Slot, err = d.ReadShort(); err != nil {
		return p, err
	}
	p.Item, err = ReadSlot(d)
	return p, err
}

// WriteContainerSlot は Set Container Slot を書く（テストの偽のサーバー用）。
func WriteContainerSlot(e *protocol.Encoder, p ContainerSlot) {
	e.WriteVarInt(p.WindowID)
	e.WriteVarInt(p.StateID)
	e.WriteShort(p.Slot)
	WriteSlot(e, p.Item)
}

// ContainerProperty は Set Container Property（かまどの燃焼・進み具合など）。
type ContainerProperty struct {
	WindowID int32
	Property int16
	Value    int16
}

// ReadContainerProperty は Set Container Property を読む。
func ReadContainerProperty(d *protocol.Decoder) (ContainerProperty, error) {
	var p ContainerProperty
	var err error
	if p.WindowID, err = d.ReadVarInt(); err != nil {
		return p, err
	}
	if p.Property, err = d.ReadShort(); err != nil {
		return p, err
	}
	p.Value, err = d.ReadShort()
	return p, err
}

// WriteContainerProperty は Set Container Property を書く（テストの偽のサーバー用）。
func WriteContainerProperty(e *protocol.Encoder, p ContainerProperty) {
	e.WriteVarInt(p.WindowID)
	e.WriteShort(p.Property)
	e.WriteShort(p.Value)
}

// PlayerInventorySlot は Set Player Inventory Slot（1.21.2 から）。Slot は持ち物の番号
// （0〜8 ホットバー, 9〜35 中, 36〜39 防具の足〜頭, 40 左手）。
type PlayerInventorySlot struct {
	Slot int32
	Item Slot
}

// ReadPlayerInventorySlot は Set Player Inventory Slot を読む。
func ReadPlayerInventorySlot(d *protocol.Decoder) (PlayerInventorySlot, error) {
	var p PlayerInventorySlot
	var err error
	if p.Slot, err = d.ReadVarInt(); err != nil {
		return p, err
	}
	p.Item, err = ReadSlot(d)
	return p, err
}

// WritePlayerInventorySlot は Set Player Inventory Slot を書く（テストの偽のサーバー用）。
func WritePlayerInventorySlot(e *protocol.Encoder, p PlayerInventorySlot) {
	e.WriteVarInt(p.Slot)
	WriteSlot(e, p.Item)
}

// クリックの種類（Click Container の Mode）。
const (
	ClickPickup     int32 = 0 // 左右クリック（button 0 左, 1 右）
	ClickQuickMove  int32 = 1 // シフトクリック
	ClickSwap       int32 = 2 // 数字キー（button 0〜8）・左手との入れ替え（button 40）
	ClickClone      int32 = 3 // 中クリック（クリエイティブ）
	ClickThrow      int32 = 4 // 捨てる（button 0 で 1 個, 1 で全部）
	ClickQuickCraft int32 = 5 // ドラッグ
	ClickPickupAll  int32 = 6 // ダブルクリック
)

// ChangedSlot は Click Container の「変わるスロット」の予測 1 つ。
type ChangedSlot struct {
	Slot int16
	Item Slot
}

// ContainerClick は Click Container（serverbound）。
type ContainerClick struct {
	WindowID int32
	StateID  int32
	Slot     int16
	Button   int8
	Mode     int32
	Changed  []ChangedSlot // 128 個まで
	Carried  Slot
}

// MaxChangedSlots は Click Container の予測の数の上限（サーバーが受け付ける数）。
const MaxChangedSlots = 128

// WriteContainerClick は Click Container を書く。
func WriteContainerClick(e *protocol.Encoder, p ContainerClick) {
	e.WriteVarInt(p.WindowID)
	e.WriteVarInt(p.StateID)
	e.WriteShort(p.Slot)
	e.WriteInt8(p.Button)
	e.WriteVarInt(p.Mode)
	e.WriteVarInt(int32(len(p.Changed)))
	for _, c := range p.Changed {
		e.WriteShort(c.Slot)
		WriteSlot(e, c.Item)
	}
	WriteSlot(e, p.Carried)
}

// ReadContainerClick は Click Container を読む（テストの偽のサーバー用）。
func ReadContainerClick(d *protocol.Decoder) (ContainerClick, error) {
	var p ContainerClick
	var err error
	if p.WindowID, err = d.ReadVarInt(); err != nil {
		return p, err
	}
	if p.StateID, err = d.ReadVarInt(); err != nil {
		return p, err
	}
	if p.Slot, err = d.ReadShort(); err != nil {
		return p, err
	}
	if p.Button, err = d.ReadInt8(); err != nil {
		return p, err
	}
	if p.Mode, err = d.ReadVarInt(); err != nil {
		return p, err
	}
	p.Changed, err = protocol.ReadPrefixedArray(d, MaxChangedSlots, func(d *protocol.Decoder) (ChangedSlot, error) {
		var c ChangedSlot
		var err error
		if c.Slot, err = d.ReadShort(); err != nil {
			return c, err
		}
		c.Item, err = ReadSlot(d)
		return c, err
	})
	if err != nil {
		return p, err
	}
	p.Carried, err = ReadSlot(d)
	return p, err
}
