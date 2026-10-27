package packets

import (
	"errors"
	"reflect"
	"testing"

	"github.com/Satoru314/go-mcdriver/internal/nbt"
	"github.com/Satoru314/go-mcdriver/internal/protocol"
)

// メタデータの全部の型を読み、使う型の値が合い、ちょうど最後まで読み進めること。
func TestReadSetEntityMetadata(t *testing.T) {
	e := protocol.NewEncoder()
	e.WriteVarInt(42)
	entry := func(idx uint8, typ int32, w func()) {
		e.WriteUByte(idx)
		e.WriteVarInt(typ)
		w()
	}
	particle := func(name string, w func()) {
		e.WriteVarInt(regID(t, "particle_type", name))
		w()
	}
	entry(0, MetaByte, func() { e.WriteUByte(0x01) })
	entry(1, MetaVarInt, func() { e.WriteVarInt(300) })
	entry(2, MetaVarLong, func() { e.WriteVarLong(1 << 40) })
	entry(3, MetaFloat, func() { e.WriteFloat(20) })
	entry(4, MetaString, func() { wstr(t, e, "もじ") })
	entry(5, MetaTextComponent, func() { wtext(t, e, "名前") })
	entry(6, MetaOptTextComponent, func() { e.WriteBool(true); wtext(t, e, "x") })
	entry(7, MetaSlot, func() { WriteSlot(e, Slot{Count: 3, Item: 5}) })
	entry(8, MetaBool, func() { e.WriteBool(true) })
	entry(9, MetaRotations, func() { e.WriteFloat(1); e.WriteFloat(2); e.WriteFloat(3) })
	entry(10, MetaPosition, func() { slotMust(t, e.WritePosition(protocol.Position{X: 1})) })
	entry(11, MetaOptPosition, func() { e.WriteBool(false) })
	entry(12, MetaDirection, func() { e.WriteVarInt(3) })
	entry(13, MetaOptUUID, func() { e.WriteBool(true); e.WriteUUID(protocol.UUID{9}) })
	entry(14, MetaBlockState, func() { e.WriteVarInt(1) })
	entry(15, MetaOptBlockState, func() { e.WriteVarInt(0) })
	entry(16, MetaNBT, func() { slotMust(t, nbt.WriteNetwork(e, map[string]any{"a": int8(1)})) })
	entry(17, MetaParticle, func() { particle("dust", func() { e.WriteInt(0xff0000); e.WriteFloat(1) }) })
	entry(18, MetaParticles, func() {
		e.WriteVarInt(11)
		particle("entity_effect", func() { e.WriteInt(-1) })
		particle("block", func() { e.WriteVarInt(1) })
		particle("dust_color_transition", func() { e.WriteInt(1); e.WriteInt(2); e.WriteFloat(1) })
		particle("item", func() { WriteSlot(e, Slot{Count: 1, Item: 1}) })
		particle("vibration", func() {
			e.WriteVarInt(regID(t, "position_source_type", "entity"))
			e.WriteVarInt(5)
			e.WriteFloat(1)
			e.WriteVarInt(20)
		})
		particle("vibration", func() {
			e.WriteVarInt(regID(t, "position_source_type", "block"))
			slotMust(t, e.WritePosition(protocol.Position{}))
			e.WriteVarInt(20)
		})
		particle("trail", func() { e.WriteDouble(1); e.WriteDouble(2); e.WriteDouble(3); e.WriteInt(5); e.WriteVarInt(9) })
		particle("sculk_charge", func() { e.WriteFloat(1) })
		particle("shriek", func() { e.WriteVarInt(3) })
		particle("flame", func() {})
		particle("block_crumble", func() { e.WriteVarInt(2) })
	})
	entry(19, MetaVillagerData, func() { e.WriteVarInt(1); e.WriteVarInt(2); e.WriteVarInt(3) })
	entry(20, MetaOptVarInt, func() { e.WriteVarInt(0) })
	entry(21, MetaPose, func() { e.WriteVarInt(5) })
	entry(22, MetaCatVariant, func() { e.WriteVarInt(1) })
	entry(23, MetaWolfVariant, func() {
		e.WriteVarInt(0)
		wstr(t, e, "a")
		wstr(t, e, "b")
		wstr(t, e, "c")
		e.WriteVarInt(0)
		wstr(t, e, "minecraft:is_forest")
	})
	entry(24, MetaFrogVariant, func() { e.WriteVarInt(2) })
	entry(25, MetaOptGlobalPosition, func() {
		e.WriteBool(true)
		wstr(t, e, "minecraft:overworld")
		slotMust(t, e.WritePosition(protocol.Position{}))
	})
	entry(26, MetaPaintingVariant, func() {
		e.WriteVarInt(0)
		e.WriteVarInt(2)
		e.WriteVarInt(1)
		wstr(t, e, "minecraft:kebab")
		e.WriteBool(true)
		wtext(t, e, "title")
		e.WriteBool(false)
	})
	entry(27, MetaSnifferState, func() { e.WriteVarInt(1) })
	entry(28, MetaArmadilloState, func() { e.WriteVarInt(1) })
	entry(29, MetaVector3, func() { e.WriteFloat(1); e.WriteFloat(2); e.WriteFloat(3) })
	entry(30, MetaQuaternion, func() { e.WriteFloat(0); e.WriteFloat(0); e.WriteFloat(0); e.WriteFloat(1) })
	WriteMetadataEnd(e)

	d := protocol.NewDecoder(e.Bytes())
	p, err := ReadSetEntityMetadata(d)
	if err != nil {
		t.Fatal(err)
	}
	if d.Remaining() != 0 {
		t.Errorf("%d バイト残った", d.Remaining())
	}
	if p.EntityID != 42 || len(p.Entries) != 31 {
		t.Fatalf("EntityID %d、項目 %d 個", p.EntityID, len(p.Entries))
	}
	want := map[uint8]any{
		0: uint8(1), 1: int32(300), 2: int64(1 << 40), 3: float32(20), 4: "もじ",
		7: Slot{Count: 3, Item: 5}, 8: true, 12: int32(3), 21: int32(5), 5: nil, 18: nil,
	}
	for i, en := range p.Entries {
		if en.Index != uint8(i) || en.Type != int32(i) {
			t.Errorf("項目 %d: %+v", i, en)
		}
		if w, ok := want[en.Index]; ok && !reflect.DeepEqual(en.Value, w) {
			t.Errorf("項目 %d の値 %#v、期待 %#v", i, en.Value, w)
		}
	}
}

// 知らない型に出会ったら、そこまでの項目とエラーを返す。
func TestReadSetEntityMetadataUnknown(t *testing.T) {
	e := protocol.NewEncoder()
	e.WriteVarInt(7)
	e.WriteUByte(9)
	e.WriteVarInt(MetaFloat)
	e.WriteFloat(5)
	e.WriteUByte(10)
	e.WriteVarInt(99)
	e.WriteBytes([]byte{1, 2, 3})
	p, err := ReadSetEntityMetadata(protocol.NewDecoder(e.Bytes()))
	if !errors.Is(err, ErrUnsupportedData) {
		t.Errorf("err %v", err)
	}
	if p.EntityID != 7 || len(p.Entries) != 1 || p.Entries[0].Value != float32(5) {
		t.Errorf("読めた分 %+v", p)
	}
}

// エンティティのパケットを書いて読むと元に戻る。
func TestEntityPacketsRoundTrip(t *testing.T) {
	sp := SpawnEntity{EntityID: 5, UUID: protocol.UUID{1, 2}, Type: 3, X: 1.5, Y: -60, Z: -2.25,
		Pitch: 200, Yaw: 64, HeadYaw: 10, Data: 9, VelX: -8000, VelY: 400, VelZ: 1}
	e := protocol.NewEncoder()
	WriteSpawnEntity(e, sp)
	if got, err := ReadSpawnEntity(protocol.NewDecoder(e.Bytes())); err != nil || got != sp {
		t.Errorf("Spawn Entity %+v %v", got, err)
	}

	for _, m := range []MoveEntity{
		{EntityID: 1, HasPos: true, DX: -4096, DY: 1, DZ: 32767, OnGround: true},
		{EntityID: 2, HasPos: true, HasRot: true, DX: 5, Yaw: 3, Pitch: 250},
		{EntityID: 3, HasRot: true, Yaw: 128, Pitch: 1, OnGround: true},
	} {
		e := protocol.NewEncoder()
		WriteMoveEntity(e, m)
		d := protocol.NewDecoder(e.Bytes())
		got, err := ReadMoveEntity(d, m.HasPos, m.HasRot)
		if err != nil || got != m || d.Remaining() != 0 {
			t.Errorf("Move %+v → %+v %v", m, got, err)
		}
	}

	for _, tp := range []bool{false, true} {
		ps := EntityPositionSync{EntityID: 4, X: 1, Y: 2, Z: 3, VelX: 0.1, VelY: -0.08, VelZ: 0, Yaw: -90, Pitch: 45, OnGround: true}
		if tp {
			ps.Flags = RelX | RelYaw
		}
		e := protocol.NewEncoder()
		WriteEntityPositionSync(e, ps, tp)
		d := protocol.NewDecoder(e.Bytes())
		got, err := ReadEntityPositionSync(d, tp)
		if err != nil || got != ps || d.Remaining() != 0 {
			t.Errorf("Position Sync (teleport=%v) %+v %v", tp, got, err)
		}
	}

	e = protocol.NewEncoder()
	WriteSetEntityMotion(e, SetEntityMotion{EntityID: 8, VelX: 1, VelY: -2, VelZ: 3})
	if got, err := ReadSetEntityMotion(protocol.NewDecoder(e.Bytes())); err != nil || got != (SetEntityMotion{8, 1, -2, 3}) {
		t.Errorf("Motion %+v %v", got, err)
	}
	e = protocol.NewEncoder()
	WriteRotateHead(e, RotateHead{EntityID: 8, HeadYaw: 77})
	if got, err := ReadRotateHead(protocol.NewDecoder(e.Bytes())); err != nil || got != (RotateHead{8, 77}) {
		t.Errorf("Head %+v %v", got, err)
	}
	e = protocol.NewEncoder()
	WriteRemoveEntities(e, []int32{1, 300, 70000})
	if got, err := ReadRemoveEntities(protocol.NewDecoder(e.Bytes())); err != nil || !reflect.DeepEqual(got, []int32{1, 300, 70000}) {
		t.Errorf("Remove %v %v", got, err)
	}
}

// Player Info Update: 全部の Actions と、属性・会話の情報つき（本物のサーバーが送る形）を読める。
func TestPlayerInfoUpdate(t *testing.T) {
	e := protocol.NewEncoder()
	e.WriteUByte(0xff)
	e.WriteVarInt(2)
	for i, name := range []string{"alice", "bob"} {
		e.WriteUUID(protocol.UUID{byte(i + 1)})
		wstr(t, e, name)
		e.WriteVarInt(1) // 属性
		wstr(t, e, "textures")
		wstr(t, e, "base64...")
		e.WriteBool(true)
		wstr(t, e, "sig")
		e.WriteBool(true) // 会話
		e.WriteUUID(protocol.UUID{7})
		e.WriteLong(123)
		slotMust(t, e.WritePrefixedBytes(make([]byte, 294)))
		slotMust(t, e.WritePrefixedBytes(make([]byte, 512)))
		e.WriteVarInt(int32(i)) // ゲームモード
		e.WriteBool(true)       // 一覧に出す
		e.WriteVarInt(25)       // 遅れ
		e.WriteBool(true)       // 一覧に出す名前
		wtext(t, e, "★"+name)
		e.WriteVarInt(3) // 並び順
		e.WriteBool(true)
	}
	d := protocol.NewDecoder(e.Bytes())
	p, err := ReadPlayerInfoUpdate(d)
	if err != nil {
		t.Fatal(err)
	}
	want := []PlayerInfoEntry{
		{UUID: protocol.UUID{1}, Name: "alice", GameMode: 0, Listed: true, Latency: 25},
		{UUID: protocol.UUID{2}, Name: "bob", GameMode: 1, Listed: true, Latency: 25},
	}
	if !reflect.DeepEqual(p.Entries, want) || d.Remaining() != 0 {
		t.Errorf("%+v（残り %d）", p, d.Remaining())
	}

	// 書いて読む（遅れだけの更新）
	e = protocol.NewEncoder()
	pu := PlayerInfoUpdate{Actions: PlayerInfoUpdateLatency, Entries: []PlayerInfoEntry{{UUID: protocol.UUID{3}, Latency: 9}}}
	slotMust(t, WritePlayerInfoUpdate(e, pu))
	if got, err := ReadPlayerInfoUpdate(protocol.NewDecoder(e.Bytes())); err != nil || !reflect.DeepEqual(got, pu) {
		t.Errorf("%+v %v", got, err)
	}

	e = protocol.NewEncoder()
	WritePlayerInfoRemove(e, []protocol.UUID{{1}, {2}})
	if got, err := ReadPlayerInfoRemove(protocol.NewDecoder(e.Bytes())); err != nil || !reflect.DeepEqual(got, []protocol.UUID{{1}, {2}}) {
		t.Errorf("Remove %v %v", got, err)
	}
}
