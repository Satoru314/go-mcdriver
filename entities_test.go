package mcdriver

import (
	"math"
	"testing"
	"time"

	"github.com/Satoru314/go-mcdriver/data"
	"github.com/Satoru314/go-mcdriver/internal/packets"
	"github.com/Satoru314/go-mcdriver/internal/protocol"
	"gonum.org/v1/gonum/spatial/r3"
)

func entType(t *testing.T, name string) *data.EntityType {
	t.Helper()
	et, ok := data.EntityTypeByName(name)
	if !ok {
		t.Fatalf("エンティティの種類 %s が無い", name)
	}
	return et
}

func mustEntity(t *testing.T, c *Client, id int32) Entity {
	t.Helper()
	e, ok := c.Entity(id)
	if !ok {
		t.Fatalf("エンティティ %d が見えない", id)
	}
	return e
}

func near(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

func nearVec(a, b r3.Vec) bool { return near(a.X, b.X) && near(a.Y, b.Y) && near(a.Z, b.Z) }

// 偽のサーバーからエンティティのパケットを送り、Entities / Entity で読めること。
func TestEntitiesFromFakeServer(t *testing.T) {
	s, c := playClient(t)
	zombie := entType(t, "zombie")
	itemT := entType(t, "item")
	playerT := entType(t, "player")
	diamond, _ := data.ItemByName("diamond")

	spawn := func(p packets.SpawnEntity) {
		s.send(data.PlayClientboundAddEntity, func(e *protocol.Encoder) { packets.WriteSpawnEntity(e, p) })
	}
	meta := func(id int32, w func(e *protocol.Encoder)) {
		s.send(data.PlayClientboundSetEntityData, func(e *protocol.Encoder) {
			e.WriteVarInt(id)
			w(e)
			packets.WriteMetadataEnd(e)
		})
	}

	// ---- 出現 ----
	spawn(packets.SpawnEntity{EntityID: 10, UUID: protocol.UUID{0xa}, Type: int32(zombie.ID), X: 1.5, Y: -60, Z: 2.5,
		Pitch: protocol.AngleFromDegrees(-45), Yaw: protocol.AngleFromDegrees(90), HeadYaw: protocol.AngleFromDegrees(45),
		VelX: 8000, VelY: -800})
	spawn(packets.SpawnEntity{EntityID: 11, UUID: protocol.UUID{0xb}, Type: int32(itemT.ID), X: 0, Y: 64, Z: 0})
	spawn(packets.SpawnEntity{EntityID: 12, UUID: protocol.UUID{0xc}, Type: int32(playerT.ID), X: -3, Y: 70, Z: 4})
	s.sync()

	es := c.Entities()
	if len(es) != 3 || es[0].ID != 10 || es[1].ID != 11 || es[2].ID != 12 {
		t.Fatalf("Entities %+v", es)
	}
	z := mustEntity(t, c, 10)
	if z.Type.Name != "zombie" || z.UUID != (UUID{0xa}) || z.Pos != (r3.Vec{X: 1.5, Y: -60, Z: 2.5}) {
		t.Errorf("ゾンビ %+v", z)
	}
	if !nearVec(z.Vel, r3.Vec{X: 1, Y: -0.1}) {
		t.Errorf("速度 %v", z.Vel)
	}
	if z.Yaw != 90 || z.Pitch != -45 || z.HeadYaw != 45 {
		t.Errorf("向き yaw %v pitch %v head %v", z.Yaw, z.Pitch, z.HeadYaw)
	}
	hw := float64(zombie.Width) / 2
	wantBox := r3.Box{Min: r3.Vec{X: 1.5 - hw, Y: -60, Z: 2.5 - hw}, Max: r3.Vec{X: 1.5 + hw, Y: -60 + float64(zombie.Height), Z: 2.5 + hw}}
	if z.Box != wantBox {
		t.Errorf("当たり判定 %v、期待 %v", z.Box, wantBox)
	}
	if z.HasHealth || z.OnFire || z.Name != "" {
		t.Errorf("メタデータの前 %+v", z)
	}
	if _, ok := c.Entity(99); ok {
		t.Error("無いエンティティが見える")
	}

	// ---- メタデータ: 体力、燃えている、落ちているアイテム ----
	meta(10, func(e *protocol.Encoder) {
		e.WriteUByte(0)
		e.WriteVarInt(packets.MetaByte)
		e.WriteUByte(0x01)
		e.WriteUByte(9)
		e.WriteVarInt(packets.MetaFloat)
		e.WriteFloat(17.5)
	})
	meta(11, func(e *protocol.Encoder) {
		e.WriteUByte(8)
		e.WriteVarInt(packets.MetaSlot)
		// 部品つき（名前）のスロット
		e.WriteVarInt(5)
		e.WriteVarInt(int32(diamond.ID))
		e.WriteVarInt(1)
		e.WriteVarInt(0)
		r, _ := data.RegistryByName("data_component_type")
		id, _ := r.ID("custom_name")
		e.WriteVarInt(id)
		e.WriteUByte(8) // TAG_String
		e.WriteUShort(3)
		e.WriteBytes([]byte("abc"))
	})
	s.sync()
	z = mustEntity(t, c, 10)
	if !z.HasHealth || z.Health != 17.5 || !z.OnFire {
		t.Errorf("ゾンビのメタデータ %+v", z)
	}
	it := mustEntity(t, c, 11)
	if it.Item != (EntityItem{ID: diamond.ID, Name: "diamond", Count: 5}) {
		t.Errorf("落ちているアイテム %+v", it.Item)
	}
	// 火が消える
	meta(10, func(e *protocol.Encoder) { e.WriteUByte(0); e.WriteVarInt(packets.MetaByte); e.WriteUByte(0) })
	// 読めない型があっても切れず、その前の項目は使う
	meta(10, func(e *protocol.Encoder) {
		e.WriteUByte(9)
		e.WriteVarInt(packets.MetaFloat)
		e.WriteFloat(3)
		e.WriteUByte(30)
		e.WriteVarInt(77)
	})
	s.sync()
	z = mustEntity(t, c, 10)
	if z.OnFire || z.Health != 3 {
		t.Errorf("メタデータの更新 %+v", z)
	}
	if es := entityKey.of(c); es.metaErrors != 1 {
		t.Errorf("読めなかったメタデータの数 %d", es.metaErrors)
	}

	// ---- プレイヤーの名前 ----
	if p := mustEntity(t, c, 12); p.Name != "" {
		t.Errorf("一覧の前に名前 %q", p.Name)
	}
	s.send(data.PlayClientboundPlayerInfoUpdate, func(e *protocol.Encoder) {
		must(t, packets.WritePlayerInfoUpdate(e, packets.PlayerInfoUpdate{
			Actions: packets.PlayerInfoAddPlayer | packets.PlayerInfoUpdateListed | packets.PlayerInfoUpdateLatency,
			Entries: []packets.PlayerInfoEntry{{UUID: protocol.UUID{0xc}, Name: "alice", Listed: true}, {UUID: protocol.UUID{0xd}, Name: "bob"}},
		}))
	})
	s.sync()
	if p := mustEntity(t, c, 12); p.Name != "alice" {
		t.Errorf("プレイヤーの名前 %q", p.Name)
	}
	if n := c.PlayerNames(); len(n) != 2 || n[UUID{0xd}] != "bob" {
		t.Errorf("PlayerNames %v", n)
	}
	s.send(data.PlayClientboundPlayerInfoRemove, func(e *protocol.Encoder) { packets.WritePlayerInfoRemove(e, []protocol.UUID{{0xd}}) })
	s.sync()
	if n := c.PlayerNames(); len(n) != 1 {
		t.Errorf("Player Info Remove のあと %v", n)
	}

	// ---- 動き ----
	move := func(m packets.MoveEntity) {
		id := data.PlayClientboundMoveEntityPos
		switch {
		case m.HasPos && m.HasRot:
			id = data.PlayClientboundMoveEntityPosRot
		case m.HasRot:
			id = data.PlayClientboundMoveEntityRot
		}
		s.send(id, func(e *protocol.Encoder) { packets.WriteMoveEntity(e, m) })
	}
	move(packets.MoveEntity{EntityID: 10, HasPos: true, DX: 4096, DY: -2048, DZ: 1, OnGround: true})
	move(packets.MoveEntity{EntityID: 10, HasPos: true, HasRot: true, DX: -1024, Yaw: protocol.AngleFromDegrees(-90), Pitch: protocol.AngleFromDegrees(11.25)})
	move(packets.MoveEntity{EntityID: 99, HasPos: true, DX: 1}) // 知らないエンティティは無視
	s.sync()
	z = mustEntity(t, c, 10)
	if want := (r3.Vec{X: 1.5 + 1 - 0.25, Y: -60.5, Z: 2.5 + 1.0/4096}); !nearVec(z.Pos, want) {
		t.Errorf("差分のあとの位置 %v、期待 %v", z.Pos, want)
	}
	if z.Yaw != -90 || z.Pitch != 11.25 || z.OnGround {
		t.Errorf("差分のあとの向き %v %v 接地 %v", z.Yaw, z.Pitch, z.OnGround)
	}
	if !near(z.Box.Min.Y, -60.5) {
		t.Errorf("箱が位置に追従しない %v", z.Box)
	}
	move(packets.MoveEntity{EntityID: 10, HasRot: true, Yaw: protocol.AngleFromDegrees(180), OnGround: true})
	s.send(data.PlayClientboundRotateHead, func(e *protocol.Encoder) {
		packets.WriteRotateHead(e, packets.RotateHead{EntityID: 10, HeadYaw: protocol.AngleFromDegrees(-45)})
	})
	s.send(data.PlayClientboundSetEntityMotion, func(e *protocol.Encoder) {
		packets.WriteSetEntityMotion(e, packets.SetEntityMotion{EntityID: 10, VelX: -4000, VelY: 0, VelZ: 800})
	})
	s.sync()
	z = mustEntity(t, c, 10)
	if z.Yaw != -180 || z.HeadYaw != -45 || !z.OnGround || !nearVec(z.Vel, r3.Vec{X: -0.5, Z: 0.1}) {
		t.Errorf("向き・頭・速度 %+v", z)
	}

	// Entity Position Sync は絶対位置
	s.send(data.PlayClientboundEntityPositionSync, func(e *protocol.Encoder) {
		packets.WriteEntityPositionSync(e, packets.EntityPositionSync{EntityID: 10, X: 100.25, Y: -61, Z: -7.5,
			VelY: -0.0784, Yaw: 30, Pitch: -5, OnGround: true}, false)
	})
	s.sync()
	z = mustEntity(t, c, 10)
	if z.Pos != (r3.Vec{X: 100.25, Y: -61, Z: -7.5}) || z.Vel != (r3.Vec{Y: -0.0784}) || z.Yaw != 30 || z.Pitch != -5 || !z.OnGround {
		t.Errorf("Position Sync のあと %+v", z)
	}
	// Teleport Entity: x と縦の向きは今の値に足す
	s.send(data.PlayClientboundTeleportEntity, func(e *protocol.Encoder) {
		packets.WriteEntityPositionSync(e, packets.EntityPositionSync{EntityID: 10, X: 1, Y: 5, Z: 6, Yaw: 10, Pitch: 10,
			Flags: packets.RelX | packets.RelPitch}, true)
	})
	s.sync()
	z = mustEntity(t, c, 10)
	if z.Pos != (r3.Vec{X: 101.25, Y: 5, Z: 6}) || z.Yaw != 10 || z.Pitch != 5 || z.OnGround {
		t.Errorf("Teleport Entity のあと %+v", z)
	}

	// ---- 消える ----
	s.send(data.PlayClientboundRemoveEntities, func(e *protocol.Encoder) { packets.WriteRemoveEntities(e, []int32{11, 99}) })
	s.sync()
	if _, ok := c.Entity(11); ok || len(c.Entities()) != 2 {
		t.Errorf("Remove Entities のあと %+v", c.Entities())
	}

	// ---- 同じディメンションの Respawn では残り、別のディメンションでは全部消える ----
	rp := packets.Respawn{DimensionType: 0, DimensionName: "minecraft:overworld", PreviousGameMode: -1, SeaLevel: 63}
	s.send(data.PlayClientboundRespawn, func(e *protocol.Encoder) { must(t, packets.WriteRespawn(e, rp)) })
	s.sync()
	if len(c.Entities()) != 2 {
		t.Errorf("同じディメンションの Respawn で消えた")
	}
	rp.DimensionType, rp.DimensionName = 1, "minecraft:the_nether"
	s.send(data.PlayClientboundRespawn, func(e *protocol.Encoder) { must(t, packets.WriteRespawn(e, rp)) })
	s.sync()
	if es := c.Entities(); len(es) != 0 {
		t.Errorf("ディメンションを変えたのに残る %+v", es)
	}
	if len(c.PlayerNames()) != 1 {
		t.Errorf("プレイヤーの一覧はディメンションを変えても残る: %v", c.PlayerNames())
	}

	// 知らない種類の番号でも切れない
	spawn(packets.SpawnEntity{EntityID: 20, Type: 9999, X: 1})
	s.sync()
	if e := mustEntity(t, c, 20); e.Type.ID != 9999 || e.Type.Name != "" || e.Box.Min != e.Box.Max {
		t.Errorf("知らない種類 %+v", e)
	}
	if err := c.Err(); err != nil {
		t.Errorf("Err %v", err)
	}
}

// 壊れた Spawn Entity は接続を切る（エラーが Err に入る）。
func TestEntitiesBadPacket(t *testing.T) {
	s, c := playClient(t)
	s.send(data.PlayClientboundAddEntity, func(e *protocol.Encoder) { e.WriteBytes([]byte{1, 2, 3}) })
	select {
	case <-c.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("切れない")
	}
	if c.Err() == nil {
		t.Error("Err が nil")
	}
}
