package packets

import (
	"bytes"
	"reflect"
	"testing"

	"github.com/Satoru314/go-mcdriver/internal/protocol"
)

// Player Action のバイト列（仕様: Status VarInt、Position、Face Byte、Sequence VarInt）。
func TestPlayerActionBytes(t *testing.T) {
	e := protocol.NewEncoder()
	if err := WritePlayerAction(e, PlayerAction{Status: ActionStopDestroy, Pos: protocol.Position{X: 1, Y: -61, Z: -1}, Face: 1, Sequence: 300}); err != nil {
		t.Fatal(err)
	}
	pos, _ := protocol.Position{X: 1, Y: -61, Z: -1}.Pack()
	want := []byte{0x02}
	for i := 7; i >= 0; i-- {
		want = append(want, byte(pos>>(8*i)))
	}
	want = append(want, 0x01, 0xac, 0x02)
	if !bytes.Equal(e.Bytes(), want) {
		t.Errorf("Player Action = % x、期待 % x", e.Bytes(), want)
	}
}

// Use Item On のバイト列（Hand、Position、Face VarInt、Cursor の float 3 つ、Inside、World Border Hit、Sequence）。
func TestUseItemOnBytes(t *testing.T) {
	e := protocol.NewEncoder()
	if err := WriteUseItemOn(e, UseItemOn{Hand: HandMain, Pos: protocol.Position{}, Face: 5, CursorX: 1, CursorY: 0.5, CursorZ: 0, Sequence: 7}); err != nil {
		t.Fatal(err)
	}
	want := []byte{0x00, 0, 0, 0, 0, 0, 0, 0, 0, 0x05, 0x3f, 0x80, 0, 0, 0x3f, 0, 0, 0, 0, 0, 0, 0, 0x00, 0x00, 0x07}
	if !bytes.Equal(e.Bytes(), want) {
		t.Errorf("Use Item On = % x、期待 % x", e.Bytes(), want)
	}
}

func TestActionRoundTrip(t *testing.T) {
	roundTrip := func(name string, write func(e *protocol.Encoder) error, read func(d *protocol.Decoder) (any, error), want any) {
		t.Helper()
		e := protocol.NewEncoder()
		if err := write(e); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		d := protocol.NewDecoder(e.Bytes())
		got, err := read(d)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if d.Remaining() != 0 {
			t.Errorf("%s: %d バイト余った", name, d.Remaining())
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("%s: %+v、期待 %+v", name, got, want)
		}
	}
	ua := UpdateAttributes{EntityID: 5, Attributes: []Attribute{
		{ID: 4, Base: 4, Modifiers: []AttributeModifier{{ID: "minecraft:base_attack_speed", Amount: -2.4, Operation: ModifierAddValue}}},
		{ID: 6, Base: 4.5, Modifiers: []AttributeModifier{}},
	}}
	roundTrip("Update Attributes", func(e *protocol.Encoder) error { return WriteUpdateAttributes(e, ua) },
		func(d *protocol.Decoder) (any, error) { return ReadUpdateAttributes(d) }, ua)
	me := MobEffect{EntityID: 9, EffectID: 2, Amplifier: 1, Duration: -1, Flags: 6}
	roundTrip("Entity Effect", func(e *protocol.Encoder) error { WriteMobEffect(e, me); return nil },
		func(d *protocol.Decoder) (any, error) { return ReadMobEffect(d) }, me)
	ui := UseItem{Hand: HandOff, Sequence: 3, Yaw: -90, Pitch: 12.5}
	roundTrip("Use Item", func(e *protocol.Encoder) error { WriteUseItem(e, ui); return nil },
		func(d *protocol.Decoder) (any, error) { return ReadUseItem(d) }, ui)
	for _, in := range []Interact{
		{EntityID: 7, Type: InteractAttack, Sneaking: true},
		{EntityID: 7, Type: InteractInteract, Hand: HandOff},
		{EntityID: 7, Type: InteractInteractAt, TargetX: 0.1, TargetY: 1.5, TargetZ: -0.2, Hand: HandMain},
	} {
		roundTrip("Interact", func(e *protocol.Encoder) error { return WriteInteract(e, in) },
			func(d *protocol.Decoder) (any, error) { return ReadInteract(d) }, in)
	}
	// 攻撃には手が付かない: ID、Type、Sneaking だけ
	e := protocol.NewEncoder()
	WriteInteract(e, Interact{EntityID: 1, Type: InteractAttack})
	if !bytes.Equal(e.Bytes(), []byte{1, 1, 0}) {
		t.Errorf("Interact（attack）= % x", e.Bytes())
	}
	if err := WriteInteract(protocol.NewEncoder(), Interact{Type: 3}); err == nil {
		t.Error("正しくない Type でエラーにならない")
	}
}
