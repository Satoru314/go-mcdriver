package mcdriver

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Satoru314/go-mcdriver/data"
	"github.com/Satoru314/go-mcdriver/internal/nbt"
	"github.com/Satoru314/go-mcdriver/internal/packets"
	"github.com/Satoru314/go-mcdriver/internal/protocol"
)

// ---- 偽のサーバーが送るスロットを組み立てる道具 ----

func invItem(t *testing.T, name string) *data.Item {
	t.Helper()
	it, ok := data.ItemByName(name)
	if !ok {
		t.Fatalf("アイテム %q が無い", name)
	}
	return it
}

func compType(t *testing.T, name string) int32 {
	t.Helper()
	r, _ := data.RegistryByName("data_component_type")
	id, ok := r.ID(name)
	if !ok {
		t.Fatalf("部品 %q が無い", name)
	}
	return id
}

// plainSlot は部品なしのスロット。
func plainSlot(t *testing.T, name string, n int32) packets.Slot {
	return packets.Slot{Count: n, Item: invItem(t, name).ID}
}

// enchantedSword はエンチャント（2 つ）と耐久の減り（damage）と名前のついたダイヤの剣。
func enchantedSword(t *testing.T) packets.Slot {
	e := protocol.NewEncoder()
	e.WriteVarInt(3) // 足す
	e.WriteVarInt(1) // 消す
	e.WriteVarInt(compType(t, "enchantments"))
	e.WriteVarInt(2)
	e.WriteVarInt(9)
	e.WriteVarInt(5)
	e.WriteVarInt(37)
	e.WriteVarInt(3)
	e.WriteBool(true)
	e.WriteVarInt(compType(t, "damage"))
	e.WriteVarInt(100)
	e.WriteVarInt(compType(t, "custom_name"))
	must(t, nbt.WriteNetwork(e, map[string]any{"text": "えくすかりばー", "italic": int8(0)}))
	e.WriteVarInt(compType(t, "repair_cost"))
	return packets.Slot{Count: 1, Item: invItem(t, "diamond_sword").ID, Components: e.Bytes()}
}

func checkItem(t *testing.T, where string, got Item, name string, n int) {
	t.Helper()
	if name == "" {
		if !got.Empty() {
			t.Errorf("%s = %v、期待は空", where, got)
		}
		return
	}
	if got.Name() != name || got.Count != n {
		t.Errorf("%s = %v、期待 %s×%d", where, got, name, n)
	}
}

func (s *fakeServer) sendContent(win, stateID int32, slots []packets.Slot, carried packets.Slot) {
	s.send(data.PlayClientboundContainerSetContent, func(e *protocol.Encoder) {
		packets.WriteContainerContent(e, packets.ContainerContent{WindowID: win, StateID: stateID, Slots: slots, Carried: carried})
	})
}

func (s *fakeServer) sendSlot(win, stateID int32, slot int16, it packets.Slot) {
	s.send(data.PlayClientboundContainerSetSlot, func(e *protocol.Encoder) {
		packets.WriteContainerSlot(e, packets.ContainerSlot{WindowID: win, StateID: stateID, Slot: slot, Item: it})
	})
}

// expectClick は Click Container を待って読む。
func (s *fakeServer) expectClick() packets.ContainerClick {
	s.t.Helper()
	d := s.expect(data.PlayServerboundContainerClick, tickPackets)
	c, err := packets.ReadContainerClick(d)
	must(s.t, err)
	return c
}

// startInventory は持ち物の窓（46 スロット）を送り、受け取り終わるのを待つ。
func startInventory(t *testing.T, s *fakeServer, stateID int32, set map[int]packets.Slot) []packets.Slot {
	slots := make([]packets.Slot, InventorySlots)
	for i, v := range set {
		slots[i] = v
	}
	s.sendContent(0, stateID, slots, packets.Slot{})
	s.sync()
	return slots
}

// ---- テスト ----

// 持ち物の窓の中身、ホットバーの選択、スロット 1 つの変化、Set Player Inventory Slot、カーソル。
func TestInventoryFromFakeServer(t *testing.T) {
	s, c := playClient(t)
	if inv := c.Inventory(); inv.Held().Count != 0 || inv.Selected != 0 {
		t.Errorf("はじめの持ち物: %+v", inv)
	}
	named := packets.Slot{Count: 3, Item: invItem(t, "apple").ID}
	{
		e := protocol.NewEncoder()
		e.WriteVarInt(1)
		e.WriteVarInt(0)
		e.WriteVarInt(compType(t, "custom_name"))
		must(t, nbt.WriteNetwork(e, "りんご"))
		named.Components = e.Bytes()
	}
	startInventory(t, s, 1, map[int]packets.Slot{
		SlotHotbar:     plainSlot(t, "stone", 64),
		SlotHotbar + 1: enchantedSword(t),
		SlotMain:       named,
		SlotHead:       plainSlot(t, "iron_helmet", 1),
	})
	s.send(data.PlayClientboundSetHeldSlot, func(e *protocol.Encoder) { e.WriteVarInt(1) })
	s.sync()

	inv := c.Inventory()
	checkItem(t, "ホットバー 0", inv.Hotbar(0), "stone", 64)
	checkItem(t, "ホットバー 1", inv.Hotbar(1), "diamond_sword", 1)
	checkItem(t, "中 9", inv.Slots[SlotMain], "apple", 3)
	checkItem(t, "頭", inv.Slots[SlotHead], "iron_helmet", 1)
	checkItem(t, "手", inv.Held(), "diamond_sword", 1)
	if inv.Selected != 1 || inv.Stale {
		t.Errorf("Selected %d Stale %v", inv.Selected, inv.Stale)
	}
	if inv.Count("stone") != 64 || inv.Find("minecraft:apple") != SlotMain || inv.Find("dirt") != -1 {
		t.Errorf("Count / Find: %d %d", inv.Count("stone"), inv.Find("apple"))
	}

	// Set Container Slot、Set Player Inventory Slot（持ち物の番号: 0 はホットバー 0、36 は足、40 は左手）、カーソル
	s.sendSlot(0, 2, SlotHotbar+2, plainSlot(t, "torch", 10))
	s.send(data.PlayClientboundSetPlayerInventory, func(e *protocol.Encoder) {
		packets.WritePlayerInventorySlot(e, packets.PlayerInventorySlot{Slot: 40, Item: plainSlot(t, "shield", 1)})
	})
	s.send(data.PlayClientboundSetPlayerInventory, func(e *protocol.Encoder) {
		packets.WritePlayerInventorySlot(e, packets.PlayerInventorySlot{Slot: 36, Item: plainSlot(t, "iron_boots", 1)})
	})
	s.send(data.PlayClientboundSetPlayerInventory, func(e *protocol.Encoder) {
		packets.WritePlayerInventorySlot(e, packets.PlayerInventorySlot{Slot: 0, Item: plainSlot(t, "stone", 5)})
	})
	s.send(data.PlayClientboundSetCursorItem, func(e *protocol.Encoder) { packets.WriteSlot(e, plainSlot(t, "dirt", 2)) })
	s.sync()
	inv = c.Inventory()
	checkItem(t, "ホットバー 2", inv.Hotbar(2), "torch", 10)
	checkItem(t, "左手", inv.Slots[SlotOffhand], "shield", 1)
	checkItem(t, "足", inv.Slots[SlotFeet], "iron_boots", 1)
	checkItem(t, "ホットバー 0", inv.Hotbar(0), "stone", 5)
	checkItem(t, "カーソル", inv.Cursor, "dirt", 2)
	if _, ok := c.Container(); ok {
		t.Error("容器を開いていないのに Container が ok")
	}
}

// 知らない部品のついたスロットが来ても切断せず、そのパケットだけを捨てて記録する。
// 次の Set Container Content で新しくなる。
func TestInventoryUnknownComponent(t *testing.T) {
	s, c := playClient(t)
	startInventory(t, s, 1, map[int]packets.Slot{SlotHotbar: plainSlot(t, "stone", 64)})

	bad := packets.Slot{Count: 1, Item: invItem(t, "stone").ID, Components: []byte{1, 0, 0x7f, 1, 2, 3}} // 型 127 は無い
	s.sendSlot(0, 2, SlotHotbar, bad)
	s.sync()
	select {
	case <-c.Done():
		t.Fatalf("切断された: %v", c.Err())
	default:
	}
	inv := c.Inventory()
	checkItem(t, "ホットバー 0（古いまま）", inv.Hotbar(0), "stone", 64)
	n, last := c.InventoryReadErrors()
	if !inv.Stale || n != 1 || !errors.Is(last, packets.ErrUnsupportedData) {
		t.Errorf("Stale %v、読めなかった数 %d、最後のエラー %v", inv.Stale, n, last)
	}

	// 読めない Set Container Content も同じ
	s.sendContent(0, 3, []packets.Slot{bad}, packets.Slot{})
	s.sync()
	if n, _ := c.InventoryReadErrors(); n != 2 {
		t.Errorf("読めなかった数 %d、期待 2", n)
	}

	startInventory(t, s, 4, map[int]packets.Slot{SlotHotbar: plainSlot(t, "dirt", 1)})
	inv = c.Inventory()
	checkItem(t, "ホットバー 0（新しい）", inv.Hotbar(0), "dirt", 1)
	if inv.Stale {
		t.Error("Set Container Content のあとも Stale")
	}
}

// 容器を開き、中身・題名・かまどの値を読み、持ち物の部分が持ち物と一致し、閉じられる。
func TestContainerFromFakeServer(t *testing.T) {
	s, c := playClient(t)
	startInventory(t, s, 1, map[int]packets.Slot{SlotHotbar: plainSlot(t, "stone", 64), SlotMain: plainSlot(t, "dirt", 3)})

	menu, _ := data.RegistryByName("menu")
	chest, _ := menu.ID("generic_9x3")
	s.send(data.PlayClientboundOpenScreen, func(e *protocol.Encoder) {
		must(t, packets.WriteOpenScreen(e, packets.OpenScreen{WindowID: 7, Type: chest, Title: map[string]any{"translate": "container.chest"}}))
	})
	slots := make([]packets.Slot, 27+36)
	slots[0] = plainSlot(t, "oak_log", 16)
	slots[26] = enchantedSword(t)
	slots[27] = plainSlot(t, "dirt", 3)   // 持ち物の中 9
	slots[54] = plainSlot(t, "stone", 64) // ホットバー 0
	s.sendContent(7, 11, slots, packets.Slot{})
	s.sync()

	ct, ok := c.Container()
	if !ok {
		t.Fatal("Container が開いていない")
	}
	if ct.ID != 7 || ct.Type != "generic_9x3" || ct.Title != "container.chest" || ct.Size != 27 || len(ct.Slots) != 63 {
		t.Errorf("Container: ID %d Type %q Title %q Size %d len %d", ct.ID, ct.Type, ct.Title, ct.Size, len(ct.Slots))
	}
	checkItem(t, "チェスト 0", ct.Slots[0], "oak_log", 16)
	checkItem(t, "チェスト 26", ct.Slots[26], "diamond_sword", 1)
	checkItem(t, "チェストの窓のホットバー 0", ct.Slots[54], "stone", 64)

	// 容器の窓の持ち物の部分が変わると、持ち物も変わる
	s.sendSlot(7, 12, 55, plainSlot(t, "torch", 4)) // ホットバー 1
	s.sendSlot(7, 13, 3, plainSlot(t, "coal", 8))
	s.send(data.PlayClientboundContainerSetData, func(e *protocol.Encoder) {
		packets.WriteContainerProperty(e, packets.ContainerProperty{WindowID: 7, Property: 2, Value: 150})
	})
	s.send(data.PlayClientboundContainerSetData, func(e *protocol.Encoder) { // 別の窓のものは無視
		packets.WriteContainerProperty(e, packets.ContainerProperty{WindowID: 99, Property: 1, Value: 5})
	})
	// 持ち物の窓（0）の変化は、容器の窓の持ち物の部分にも写る
	s.sendSlot(0, 2, SlotMain+1, plainSlot(t, "sand", 2))
	s.sync()
	inv := c.Inventory()
	checkItem(t, "ホットバー 1", inv.Hotbar(1), "torch", 4)
	ct, _ = c.Container()
	checkItem(t, "チェスト 3", ct.Slots[3], "coal", 8)
	checkItem(t, "チェストの窓の持ち物の中 10", ct.Slots[28], "sand", 2)
	if len(ct.Properties) != 1 || ct.Properties[2] != 150 {
		t.Errorf("Properties %v", ct.Properties)
	}

	// 自分で閉じる → Close Container（serverbound）に窓の番号
	must(t, c.CloseContainer())
	d := s.expect(data.PlayServerboundContainerClose, tickPackets)
	if id, _ := d.ReadVarInt(); id != 7 {
		t.Errorf("閉じる窓の番号 %d", id)
	}
	if _, ok := c.Container(); ok {
		t.Error("閉じたのに Container が ok")
	}

	// サーバーが開いて、サーバーが閉じる
	furnace, _ := menu.ID("furnace")
	s.send(data.PlayClientboundOpenScreen, func(e *protocol.Encoder) {
		must(t, packets.WriteOpenScreen(e, packets.OpenScreen{WindowID: 8, Type: furnace, Title: "かまど"}))
	})
	s.sync()
	if ct, ok := c.Container(); !ok || ct.Type != "furnace" || ct.Title != "かまど" || ct.Size != 3 {
		t.Errorf("かまど: %+v %v", ct, ok)
	}
	s.send(data.PlayClientboundContainerClose, func(e *protocol.Encoder) { e.WriteVarInt(8) })
	s.sync()
	if _, ok := c.Container(); ok {
		t.Error("サーバーが閉じたのに Container が ok")
	}
}

// SelectHotbar は Set Held Item（serverbound）を送る。
func TestSelectHotbar(t *testing.T) {
	s, c := playClient(t)
	must(t, c.SelectHotbar(5))
	d := s.expect(data.PlayServerboundSetCarriedItem, tickPackets)
	if v, err := d.ReadShort(); err != nil || v != 5 {
		t.Errorf("Set Held Item: %d %v", v, err)
	}
	if c.Inventory().Selected != 5 {
		t.Error("Selected が変わらない")
	}
	if err := c.SelectHotbar(9); err == nil {
		t.Error("9 でエラーにならない")
	}
}

// clickAsync は別の goroutine で ClickSlot を呼ぶ。
func clickAsync(c *Client, slot, button int, mode ClickMode) <-chan error {
	ch := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		ch <- c.ClickSlot(ctx, slot, button, mode)
	}()
	return ch
}

func waitErr(t *testing.T, ch <-chan error) error {
	t.Helper()
	select {
	case err := <-ch:
		return err
	case <-time.After(5 * time.Second):
		t.Fatal("ClickSlot が戻らない")
		return nil
	}
}

// expectProbe は確かめるためのクリック（PICKUP、button 5、スロット -1、state ID -1）を待つ。
func (s *fakeServer) expectProbe(win int32) packets.ContainerClick {
	s.t.Helper()
	p := s.expectClick()
	if p.WindowID != win || p.StateID != -1 || p.Slot != -1 || p.Button != probeButton || p.Mode != packets.ClickPickup || len(p.Changed) != 0 {
		s.t.Fatalf("確かめのクリック: %+v", p)
	}
	return p
}

// ClickSlot: state ID と予測を正しく送り、サーバーの内容が予測どおりなら nil、違えば ErrClickRejected。
func TestClickSlot(t *testing.T) {
	s, c := playClient(t)
	sword := enchantedSword(t)
	slots := startInventory(t, s, 5, map[int]packets.Slot{
		SlotHotbar:     plainSlot(t, "stone", 64),
		SlotHotbar + 1: sword,
		SlotMain:       plainSlot(t, "stone", 10),
	})

	// 1. 左クリックでホットバー 0 の石 64 を拾う
	ch := clickAsync(c, SlotHotbar, 0, ClickPickup)
	p := s.expectClick()
	if p.WindowID != 0 || p.StateID != 5 || p.Slot != SlotHotbar || p.Button != 0 || p.Mode != packets.ClickPickup {
		t.Fatalf("Click Container: %+v", p)
	}
	if len(p.Changed) != 1 || p.Changed[0].Slot != SlotHotbar || !p.Changed[0].Item.Empty() || !p.Carried.Equal(plainSlot(t, "stone", 64)) {
		t.Fatalf("予測: %+v carried %+v", p.Changed, p.Carried)
	}
	s.expectProbe(0)
	slots[SlotHotbar] = packets.Slot{}
	s.sendContent(0, 6, slots, plainSlot(t, "stone", 64))
	if err := waitErr(t, ch); err != nil {
		t.Fatalf("受け入れられたクリック: %v", err)
	}
	checkItem(t, "カーソル", c.Inventory().Cursor, "stone", 64)

	// 2. 右クリックで石 10 の上に 1 個置く（重なる）
	ch = clickAsync(c, SlotMain, 1, ClickPickup)
	p = s.expectClick()
	if p.StateID != 6 || len(p.Changed) != 1 || !p.Changed[0].Item.Equal(plainSlot(t, "stone", 11)) || !p.Carried.Equal(plainSlot(t, "stone", 63)) {
		t.Fatalf("右クリックの予測: %+v", p)
	}
	s.expectProbe(0)
	slots[SlotMain] = plainSlot(t, "stone", 11)
	s.sendContent(0, 7, slots, plainSlot(t, "stone", 63))
	if err := waitErr(t, ch); err != nil {
		t.Fatalf("右クリック: %v", err)
	}

	// 3. 剣（部品つき）と入れ替える。予測には剣の部品情報がそのままつく
	ch = clickAsync(c, SlotHotbar+1, 0, ClickPickup)
	p = s.expectClick()
	if len(p.Changed) != 1 || !p.Changed[0].Item.Equal(plainSlot(t, "stone", 63)) || !p.Carried.Equal(sword) || !bytes.Equal(p.Carried.Components, sword.Components) {
		t.Fatalf("入れ替えの予測: %+v carried %+v", p.Changed, p.Carried)
	}
	s.expectProbe(0)
	// サーバーは受け入れなかった（何も変わらない）
	s.sendContent(0, 8, slots, plainSlot(t, "stone", 63))
	if err := waitErr(t, ch); !errors.Is(err, ErrClickRejected) {
		t.Fatalf("受け入れられなかったクリック: %v", err)
	}
	inv := c.Inventory()
	checkItem(t, "カーソル（サーバーの内容に戻る）", inv.Cursor, "stone", 63)
	checkItem(t, "ホットバー 1（サーバーの内容に戻る）", inv.Hotbar(1), "diamond_sword", 1)

	// 4. 数字キー（ホットバー 2 と中 9 の入れ替え）
	ch = clickAsync(c, SlotMain, 2, ClickSwap)
	p = s.expectClick()
	if p.Mode != packets.ClickSwap || p.Button != 2 || len(p.Changed) != 2 {
		t.Fatalf("入れ替え: %+v", p)
	}
	for _, ch := range p.Changed {
		switch ch.Slot {
		case SlotMain:
			if !ch.Item.Empty() {
				t.Errorf("中 9 の予測 %+v", ch.Item)
			}
		case SlotHotbar + 2:
			if !ch.Item.Equal(plainSlot(t, "stone", 11)) {
				t.Errorf("ホットバー 2 の予測 %+v", ch.Item)
			}
		default:
			t.Errorf("予測のスロット %d", ch.Slot)
		}
	}
	s.expectProbe(0)
	slots[SlotMain], slots[SlotHotbar+2] = packets.Slot{}, plainSlot(t, "stone", 11)
	s.sendContent(0, 9, slots, plainSlot(t, "stone", 63))
	if err := waitErr(t, ch); err != nil {
		t.Fatalf("数字キー: %v", err)
	}

	// 5. シフトクリックは予測しない（予測なしで送り、内容が届けば nil）
	ch = clickAsync(c, SlotHotbar+2, 0, ClickQuickMove)
	p = s.expectClick()
	if p.Mode != packets.ClickQuickMove || len(p.Changed) != 0 || !p.Carried.Equal(plainSlot(t, "stone", 63)) {
		t.Fatalf("シフトクリック: %+v", p)
	}
	s.expectProbe(0)
	s.sendContent(0, 10, slots, plainSlot(t, "stone", 63))
	if err := waitErr(t, ch); err != nil {
		t.Fatalf("シフトクリック: %v", err)
	}

	// 6. 窓の外に左クリックでカーソルのものを捨てる
	ch = clickAsync(c, SlotOutside, 0, ClickPickup)
	p = s.expectClick()
	if p.Slot != SlotOutside || len(p.Changed) != 0 || !p.Carried.Empty() {
		t.Fatalf("窓の外: %+v", p)
	}
	s.expectProbe(0)
	s.sendContent(0, 11, slots, packets.Slot{})
	if err := waitErr(t, ch); err != nil {
		t.Fatalf("窓の外: %v", err)
	}

	// 窓の外の番号はエラー
	if err := c.ClickSlot(context.Background(), 46, 0, ClickPickup); err == nil {
		t.Error("46 番でエラーにならない")
	}
}

// Drop は Click Container の捨てる（mode 4）。1 個と全部。
func TestDrop(t *testing.T) {
	s, c := playClient(t)
	slots := startInventory(t, s, 1, map[int]packets.Slot{SlotHotbar: plainSlot(t, "stone", 64)})

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		c.Drop(ctx, SlotHotbar, false)
	}()
	p := s.expectClick()
	if p.Mode != packets.ClickThrow || p.Button != 0 || len(p.Changed) != 1 || !p.Changed[0].Item.Equal(plainSlot(t, "stone", 63)) {
		t.Fatalf("1 個捨てる: %+v", p)
	}
	s.expectProbe(0)
	slots[SlotHotbar] = plainSlot(t, "stone", 63)
	s.sendContent(0, 2, slots, packets.Slot{})

	ch := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		ch <- c.Drop(ctx, SlotHotbar, true)
	}()
	p = s.expectClick()
	if p.Mode != packets.ClickThrow || p.Button != 1 || p.StateID != 2 || len(p.Changed) != 1 || !p.Changed[0].Item.Empty() {
		t.Fatalf("全部捨てる: %+v", p)
	}
	s.expectProbe(0)
	slots[SlotHotbar] = packets.Slot{}
	s.sendContent(0, 3, slots, packets.Slot{})
	if err := waitErr(t, ch); err != nil {
		t.Fatal(err)
	}
	checkItem(t, "ホットバー 0", c.Inventory().Hotbar(0), "", 0)
}

// サーバーが答えなければ、ctx の時間切れで戻る。
func TestClickSlotNoAnswer(t *testing.T) {
	s, c := playClient(t)
	startInventory(t, s, 1, map[int]packets.Slot{SlotHotbar: plainSlot(t, "stone", 64)})
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	start := time.Now()
	err := c.ClickSlot(ctx, SlotHotbar, 0, ClickPickup)
	if !errors.Is(err, context.DeadlineExceeded) || time.Since(start) > 2*time.Second {
		t.Errorf("err = %v（%v）", err, time.Since(start))
	}
}

// 容器の窓のクリックは、その窓の番号と state ID で送り、ホットバーの番号は容器の窓の番号になる。
func TestClickSlotInContainer(t *testing.T) {
	s, c := playClient(t)
	startInventory(t, s, 1, nil)
	menu, _ := data.RegistryByName("menu")
	chest, _ := menu.ID("generic_9x1")
	s.send(data.PlayClientboundOpenScreen, func(e *protocol.Encoder) {
		must(t, packets.WriteOpenScreen(e, packets.OpenScreen{WindowID: 3, Type: chest, Title: "箱"}))
	})
	slots := make([]packets.Slot, 9+36)
	slots[0] = plainSlot(t, "oak_log", 5)
	s.sendContent(3, 40, slots, packets.Slot{})
	s.sync()

	// 数字キー 0 で、箱の 0 番とホットバー 0（箱の窓では 9+27 = 36 番）を入れ替える
	ch := clickAsync(c, 0, 0, ClickSwap)
	p := s.expectClick()
	if p.WindowID != 3 || p.StateID != 40 || len(p.Changed) != 2 {
		t.Fatalf("容器の中のクリック: %+v", p)
	}
	s.expectProbe(3)
	slots[0], slots[36] = packets.Slot{}, plainSlot(t, "oak_log", 5)
	s.sendContent(3, 41, slots, packets.Slot{})
	if err := waitErr(t, ch); err != nil {
		t.Fatal(err)
	}
	checkItem(t, "持ち物のホットバー 0", c.Inventory().Hotbar(0), "oak_log", 5)
}
