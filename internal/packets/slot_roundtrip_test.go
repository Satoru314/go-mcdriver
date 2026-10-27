package packets

import (
	"bytes"
	"testing"

	"github.com/Satoru314/go-mcdriver/data"
	"github.com/Satoru314/go-mcdriver/internal/nbt"
	"github.com/Satoru314/go-mcdriver/internal/protocol"
)

// 単位 8: 部品つきのスロットを読んで Components に残し、書き戻すと同じバイト列になることを確かめる。
// （読み進め方そのもののテストは slot_test.go）

// ---- 部品つきのスロットを、読み方の実装とは別に手で組み立てる道具 ----

func compID(t *testing.T, name string) int32 {
	t.Helper()
	return regID(t, "data_component_type", name)
}

func itemNum(t *testing.T, name string) data.ItemID {
	t.Helper()
	it, ok := data.ItemByName(name)
	if !ok {
		t.Fatalf("アイテム %q が無い", name)
	}
	return it.ID
}

// comp は部品 1 つ（型の名前と、中身を書く関数）。
type comp struct {
	name  string
	write func(e *protocol.Encoder)
}

// encodeSlot はスロットを組み立てる。足す部品 add、消す部品 remove。
// 戻り値の 2 つ目は部品の部分（足す数から最後まで）のバイト列。
func encodeSlot(t *testing.T, count int32, item string, add []comp, remove []string) (all, comps []byte) {
	t.Helper()
	c := protocol.NewEncoder()
	c.WriteVarInt(int32(len(add)))
	c.WriteVarInt(int32(len(remove)))
	for _, a := range add {
		c.WriteVarInt(compID(t, a.name))
		a.write(c)
	}
	for _, r := range remove {
		c.WriteVarInt(compID(t, r))
	}
	e := protocol.NewEncoder()
	e.WriteVarInt(count)
	e.WriteVarInt(int32(itemNum(t, item)))
	e.WriteBytes(c.Bytes())
	return e.Bytes(), c.Bytes()
}

func wNBT(t *testing.T, v any) func(e *protocol.Encoder) {
	return func(e *protocol.Encoder) {
		if err := nbt.WriteNetwork(e, v); err != nil {
			t.Fatal(err)
		}
	}
}

func wStr(e *protocol.Encoder, s string) {
	e.WriteVarInt(int32(len(s)))
	e.WriteBytes([]byte(s))
}

// 効果 1 つ（MobEffectInstance）。hidden があれば隠れた効果を 1 段つける。
func wEffect(e *protocol.Encoder, id, amp, dur int32, hidden bool) {
	e.WriteVarInt(id)
	e.WriteVarInt(amp)
	e.WriteVarInt(dur)
	e.WriteBool(false)
	e.WriteBool(true)
	e.WriteBool(true)
	e.WriteBool(hidden)
	if hidden {
		e.WriteVarInt(0)
		e.WriteVarInt(100)
		e.WriteBool(false)
		e.WriteBool(false)
		e.WriteBool(false)
		e.WriteBool(false)
	}
}

func wEnchantments(pairs ...int32) func(e *protocol.Encoder) {
	return func(e *protocol.Encoder) {
		e.WriteVarInt(int32(len(pairs) / 2))
		for i := 0; i+1 < len(pairs); i += 2 {
			e.WriteVarInt(pairs[i])
			e.WriteVarInt(pairs[i+1])
		}
		e.WriteBool(true)
	}
}

// survivalComponents はサバイバルで出てくる部品と、その中身（1.21.4 の形）。
func survivalComponents(t *testing.T) []comp {
	text := map[string]any{"text": "すごい剣", "italic": int8(0), "color": "gold"}
	return []comp{
		{"damage", func(e *protocol.Encoder) { e.WriteVarInt(123) }},
		{"max_damage", func(e *protocol.Encoder) { e.WriteVarInt(1561) }},
		{"max_stack_size", func(e *protocol.Encoder) { e.WriteVarInt(16) }},
		{"enchantments", wEnchantments(9, 5, 37, 3)},
		{"stored_enchantments", wEnchantments(20, 1)},
		{"repair_cost", func(e *protocol.Encoder) { e.WriteVarInt(3) }},
		{"custom_name", wNBT(t, text)},
		{"item_name", wNBT(t, "名前だけの文字列")}, // ルートが TAG_String の文字の部品
		{"lore", func(e *protocol.Encoder) {
			e.WriteVarInt(2)
			wNBT(t, "1 行目")(e)
			wNBT(t, map[string]any{"text": "2 行目", "extra": []any{map[string]any{"text": "!"}}})(e)
		}},
		{"rarity", func(e *protocol.Encoder) { e.WriteVarInt(2) }},
		{"unbreakable", func(e *protocol.Encoder) { e.WriteBool(true) }},
		{"attribute_modifiers", func(e *protocol.Encoder) {
			e.WriteVarInt(2)
			for i := int32(0); i < 2; i++ {
				e.WriteVarInt(i)                        // 属性
				wStr(e, "minecraft:base_attack_damage") // 修飾子の名前
				e.WriteDouble(7.5)
				e.WriteVarInt(0) // add_value
				e.WriteVarInt(1) // mainhand
			}
			e.WriteBool(true)
		}},
		{"food", func(e *protocol.Encoder) {
			e.WriteVarInt(4)
			e.WriteFloat(2.4)
			e.WriteBool(false)
		}},
		{"consumable", func(e *protocol.Encoder) {
			e.WriteFloat(1.6)
			e.WriteVarInt(1) // eat
			e.WriteVarInt(0) // 音を直接
			wStr(e, "minecraft:entity.generic.eat")
			e.WriteBool(true) // 届く距離あり
			e.WriteFloat(16)
			e.WriteBool(true) // 粒子
			e.WriteVarInt(3)  // 効果 3 つ
			e.WriteVarInt(0)  // apply_effects
			e.WriteVarInt(2)
			wEffect(e, 9, 1, 600, true)
			wEffect(e, 10, 0, 100, false)
			e.WriteFloat(0.8)
			e.WriteVarInt(4)   // play_sound
			e.WriteVarInt(101) // 番号で（100 番）
			e.WriteVarInt(1)   // remove_effects
			e.WriteVarInt(0)   // タグで
			wStr(e, "minecraft:harmful")
		}},
		{"use_cooldown", func(e *protocol.Encoder) {
			e.WriteFloat(1)
			e.WriteBool(true)
			wStr(e, "minecraft:ender_pearl")
		}},
		{"tool", func(e *protocol.Encoder) {
			e.WriteVarInt(3)
			// タグで指す規則、速さと正しい道具か あり
			e.WriteVarInt(0)
			wStr(e, "minecraft:mineable/pickaxe")
			e.WriteBool(true)
			e.WriteFloat(8)
			e.WriteBool(true)
			e.WriteBool(true)
			// 番号 2 つで指す規則、速さだけ
			e.WriteVarInt(3)
			e.WriteVarInt(1)
			e.WriteVarInt(2)
			e.WriteBool(true)
			e.WriteFloat(1.5)
			e.WriteBool(false)
			// 空の ID Set（番号 0 個）、どちらも無し
			e.WriteVarInt(1)
			e.WriteBool(false)
			e.WriteBool(false)
			e.WriteFloat(1)
			e.WriteVarInt(1)
		}},
		{"enchantable", func(e *protocol.Encoder) { e.WriteVarInt(10) }},
		{"equippable", func(e *protocol.Encoder) {
			e.WriteVarInt(4)  // head
			e.WriteVarInt(51) // 音（番号）
			e.WriteBool(true) // 見た目あり
			wStr(e, "minecraft:diamond")
			e.WriteBool(false) // 画面の重ねなし
			e.WriteBool(true)  // 着けられる生き物あり
			e.WriteVarInt(2)
			e.WriteVarInt(147)
			e.WriteBool(true)
			e.WriteBool(true)
			e.WriteBool(true)
		}},
		{"repairable", func(e *protocol.Encoder) {
			e.WriteVarInt(0)
			wStr(e, "minecraft:diamond_tool_materials")
		}},
		{"potion_contents", func(e *protocol.Encoder) {
			e.WriteBool(true)
			e.WriteVarInt(17) // ポーションの番号
			e.WriteBool(true)
			e.WriteInt(0x385DC6) // 色
			e.WriteVarInt(1)
			wEffect(e, 1, 0, 3600, false)
			e.WriteBool(true)
			wStr(e, "my_potion")
		}},
		{"dyed_color", func(e *protocol.Encoder) {
			e.WriteInt(0xFF0000)
			e.WriteBool(true)
		}},
		{"custom_model_data", func(e *protocol.Encoder) {
			e.WriteVarInt(1)
			e.WriteFloat(1.5)
			e.WriteVarInt(2)
			e.WriteBool(true)
			e.WriteBool(false)
			e.WriteVarInt(1)
			wStr(e, "abc")
			e.WriteVarInt(1)
			e.WriteInt(-1)
		}},
		{"enchantment_glint_override", func(e *protocol.Encoder) { e.WriteBool(true) }},
		{"hide_additional_tooltip", func(e *protocol.Encoder) {}},
		{"glider", func(e *protocol.Encoder) {}},
		{"custom_data", wNBT(t, map[string]any{"a": int32(1), "b": []any{int8(1), int8(2)}})},
		{"written_book_content", func(e *protocol.Encoder) {
			wStr(e, "題名")
			e.WriteBool(false)
			wStr(e, "書いた人")
			e.WriteVarInt(0)
			e.WriteVarInt(2)
			wNBT(t, "ページ 1")(e)
			e.WriteBool(false)
			wNBT(t, "ページ 2")(e)
			e.WriteBool(true)
			wNBT(t, "ページ 2（書き換え）")(e)
			e.WriteBool(true)
		}},
		{"writable_book_content", func(e *protocol.Encoder) {
			e.WriteVarInt(1)
			wStr(e, "書きかけ")
			e.WriteBool(false)
		}},
		{"can_break", func(e *protocol.Encoder) {
			e.WriteVarInt(1)
			e.WriteBool(true) // ブロックあり
			e.WriteVarInt(2)
			e.WriteVarInt(1)
			e.WriteBool(true) // 状態の条件あり
			e.WriteVarInt(2)
			wStr(e, "facing")
			e.WriteBool(true)
			wStr(e, "north")
			wStr(e, "age")
			e.WriteBool(false)
			e.WriteBool(true)
			wStr(e, "1")
			e.WriteBool(false)
			e.WriteBool(true) // NBT あり
			wNBT(t, map[string]any{"x": int32(1)})(e)
			e.WriteBool(true)
		}},
		{"firework_explosion", func(e *protocol.Encoder) {
			e.WriteVarInt(1)
			e.WriteVarInt(2)
			e.WriteInt(1)
			e.WriteInt(2)
			e.WriteVarInt(0)
			e.WriteBool(true)
			e.WriteBool(false)
		}},
		{"fireworks", func(e *protocol.Encoder) {
			e.WriteVarInt(2)
			e.WriteVarInt(1)
			e.WriteVarInt(4)
			e.WriteVarInt(1)
			e.WriteInt(0xFFFFFF)
			e.WriteVarInt(1)
			e.WriteInt(0)
			e.WriteBool(false)
			e.WriteBool(true)
		}},
		{"profile", func(e *protocol.Encoder) {
			e.WriteBool(true)
			wStr(e, "Steve")
			e.WriteBool(true)
			e.WriteUUID(protocol.UUID{1, 2, 3})
			e.WriteVarInt(1)
			wStr(e, "textures")
			wStr(e, "ZXlK...")
			e.WriteBool(true)
			wStr(e, "sig")
		}},
		{"trim", func(e *protocol.Encoder) {
			e.WriteVarInt(3) // 素材は番号（2 番）
			e.WriteVarInt(5) // 模様も番号（4 番）。直接の中身の形は読まない（ErrUnsupportedData）
			e.WriteBool(true)
		}},
		{"instrument", func(e *protocol.Encoder) {
			e.WriteVarInt(0)  // 直接
			e.WriteVarInt(10) // 音は番号
			e.WriteFloat(7)
			e.WriteFloat(256)
			wNBT(t, "ponder")(e)
		}},
		{"jukebox_playable", func(e *protocol.Encoder) {
			e.WriteBool(false) // 名前で
			wStr(e, "minecraft:cat")
			e.WriteBool(true)
		}},
		{"lodestone_tracker", func(e *protocol.Encoder) {
			e.WriteBool(true)
			wStr(e, "minecraft:overworld")
			e.WriteLong(12345)
			e.WriteBool(true)
		}},
		{"banner_patterns", func(e *protocol.Encoder) {
			e.WriteVarInt(2)
			e.WriteVarInt(5) // 番号
			e.WriteVarInt(14)
			e.WriteVarInt(0) // 直接
			wStr(e, "minecraft:creeper")
			wStr(e, "block.minecraft.banner.creeper")
			e.WriteVarInt(1)
		}},
		{"base_color", func(e *protocol.Encoder) { e.WriteVarInt(3) }},
		{"pot_decorations", func(e *protocol.Encoder) {
			e.WriteVarInt(4)
			for i := int32(0); i < 4; i++ {
				e.WriteVarInt(i)
			}
		}},
		{"block_state", func(e *protocol.Encoder) {
			e.WriteVarInt(1)
			wStr(e, "honey_level")
			wStr(e, "5")
		}},
		{"bees", func(e *protocol.Encoder) {
			e.WriteVarInt(1)
			wNBT(t, map[string]any{"id": "minecraft:bee"})(e)
			e.WriteVarInt(10)
			e.WriteVarInt(600)
		}},
		{"suspicious_stew_effects", func(e *protocol.Encoder) {
			e.WriteVarInt(1)
			e.WriteVarInt(15)
			e.WriteVarInt(160)
		}},
		{"map_id", func(e *protocol.Encoder) { e.WriteVarInt(7) }},
		{"map_color", func(e *protocol.Encoder) { e.WriteInt(0x46402E) }},
		{"map_decorations", wNBT(t, map[string]any{})},
		{"ominous_bottle_amplifier", func(e *protocol.Encoder) { e.WriteVarInt(2) }},
		{"death_protection", func(e *protocol.Encoder) {
			e.WriteVarInt(2)
			e.WriteVarInt(2) // clear_all_effects
			e.WriteVarInt(3) // teleport_randomly
			e.WriteFloat(16)
		}},
		{"damage_resistant", func(e *protocol.Encoder) { wStr(e, "minecraft:is_fire") }},
		{"intangible_projectile", wNBT(t, map[string]any{})},
		{"block_entity_data", wNBT(t, map[string]any{"id": "minecraft:chest"})},
		{"note_block_sound", func(e *protocol.Encoder) { wStr(e, "minecraft:entity.zombie.ambient") }},
		{"item_model", func(e *protocol.Encoder) { wStr(e, "minecraft:diamond_sword") }},
	}
}

// readAndCheck は raw（スロットのあとに目印の 3 バイトがつく）を ReadSlot と SkipSlot で読み、
// 両方がスロットの終わりちょうどで止まり、ReadSlot の結果が書き戻すと同じバイト列になることを確かめる。
func readAndCheck(t *testing.T, raw, comps []byte, wantCount int32, wantItem data.ItemID) Slot {
	t.Helper()
	marker := []byte{0xAB, 0xCD, 0xEF}
	buf := append(append([]byte(nil), raw...), marker...)

	d := protocol.NewDecoder(buf)
	s, err := ReadSlot(d)
	if err != nil {
		t.Fatalf("ReadSlot: %v", err)
	}
	if rest := d.ReadRest(); !bytes.Equal(rest, marker) {
		t.Fatalf("ReadSlot のあとの残り %x、期待 %x（読み過ぎか読み足りない）", rest, marker)
	}
	if s.Count != wantCount || s.Item != wantItem {
		t.Errorf("Count %d Item %d、期待 %d %d", s.Count, s.Item, wantCount, wantItem)
	}
	if comps != nil && !bytes.Equal(s.Components, comps) {
		t.Errorf("Components が元と違う:\n got %x\nwant %x", s.Components, comps)
	}

	d = protocol.NewDecoder(buf)
	if err := SkipSlot(d); err != nil {
		t.Fatalf("SkipSlot: %v", err)
	}
	if rest := d.ReadRest(); !bytes.Equal(rest, marker) {
		t.Fatalf("SkipSlot のあとの残り %x", rest)
	}

	e := protocol.NewEncoder()
	WriteSlot(e, s)
	if !bytes.Equal(e.Bytes(), raw) {
		t.Errorf("書き戻したバイト列が元と違う:\n got %x\nwant %x", e.Bytes(), raw)
	}
	return s
}

// ---- テスト ----

// 部品の無いスロットと空のスロット。
func TestSlotPlain(t *testing.T) {
	raw, _ := encodeSlot(t, 64, "stone", nil, nil)
	s := readAndCheck(t, raw, nil, 64, itemNum(t, "stone"))
	if s.Components != nil || s.HasComponents() {
		t.Errorf("部品が無いのに Components = %x", s.Components)
	}
	readAndCheck(t, []byte{0}, nil, 0, 0)
}

// サバイバルで出てくる部品を 1 つずつつけたスロットを、正しく読み飛ばして書き戻せる。
func TestSlotEachComponent(t *testing.T) {
	for _, c := range survivalComponents(t) {
		t.Run(c.name, func(t *testing.T) {
			raw, comps := encodeSlot(t, 1, "diamond_sword", []comp{c}, nil)
			readAndCheck(t, raw, comps, 1, itemNum(t, "diamond_sword"))
		})
	}
}

// 全部の部品を 1 つのスロットにつけ、消す部品もつけたもの。
func TestSlotAllComponentsTogether(t *testing.T) {
	raw, comps := encodeSlot(t, 3, "netherite_pickaxe", survivalComponents(t), []string{"tool", "max_damage", "food"})
	readAndCheck(t, raw, comps, 3, itemNum(t, "netherite_pickaxe"))
}

// 消す部品だけのスロット（例: 食べられなくしたリンゴ）。
func TestSlotRemovedOnly(t *testing.T) {
	raw, comps := encodeSlot(t, 5, "apple", nil, []string{"food", "consumable"})
	s := readAndCheck(t, raw, comps, 5, itemNum(t, "apple"))
	if !s.HasComponents() {
		t.Error("消す部品があるのに HasComponents が false")
	}
}

// 中にスロットを持つ部品（container, bundle_contents, charged_projectiles, use_remainder）。
// 中のスロットにも部品がつく。
func TestSlotNested(t *testing.T) {
	ench, _ := encodeSlot(t, 1, "diamond_sword", []comp{{"enchantments", wEnchantments(9, 5)}, {"damage", func(e *protocol.Encoder) { e.WriteVarInt(10) }}}, nil)
	named, _ := encodeSlot(t, 3, "apple", []comp{{"custom_name", wNBT(t, "りんご")}}, nil)
	slots := func(e *protocol.Encoder) {
		e.WriteVarInt(4)
		e.WriteBytes(ench)
		e.WriteVarInt(0) // 空のスロット（container は空のスロットを持てる）
		e.WriteBytes(named)
		e.WriteBytes(ench)
	}
	// シュルカーボックスの中に、中身つきのバンドル（入れ子 2 段）
	bundle, _ := encodeSlot(t, 1, "bundle", []comp{{"bundle_contents", func(e *protocol.Encoder) {
		e.WriteVarInt(1)
		e.WriteBytes(named)
	}}}, nil)
	raw, comps := encodeSlot(t, 1, "shulker_box", []comp{
		{"container", func(e *protocol.Encoder) {
			slots(e)
		}},
		{"custom_name", wNBT(t, "箱")},
	}, nil)
	readAndCheck(t, raw, comps, 1, itemNum(t, "shulker_box"))

	raw, comps = encodeSlot(t, 1, "shulker_box", []comp{{"container", func(e *protocol.Encoder) {
		e.WriteVarInt(1)
		e.WriteBytes(bundle)
	}}}, nil)
	readAndCheck(t, raw, comps, 1, itemNum(t, "shulker_box"))

	raw, comps = encodeSlot(t, 1, "crossbow", []comp{{"charged_projectiles", func(e *protocol.Encoder) {
		e.WriteVarInt(1)
		e.WriteBytes(named)
	}}}, nil)
	readAndCheck(t, raw, comps, 1, itemNum(t, "crossbow"))

	raw, comps = encodeSlot(t, 1, "honey_bottle", []comp{{"use_remainder", func(e *protocol.Encoder) { e.WriteBytes(named) }}}, nil)
	readAndCheck(t, raw, comps, 1, itemNum(t, "honey_bottle"))
}

// 入れ子が深すぎるスロットは止まる（スタックを使い切らない）。
func TestSlotTooDeep(t *testing.T) {
	cid := compID(t, "container")
	box := itemNum(t, "shulker_box")
	var inner []byte = []byte{0}
	for range 40 {
		e := protocol.NewEncoder()
		e.WriteVarInt(1)
		e.WriteVarInt(int32(box))
		e.WriteVarInt(1)
		e.WriteVarInt(0)
		e.WriteVarInt(cid)
		e.WriteVarInt(1)
		e.WriteBytes(inner)
		inner = e.Bytes()
	}
	if _, err := ReadSlot(protocol.NewDecoder(inner)); err == nil {
		t.Fatal("入れ子が深すぎるのにエラーにならない")
	}
}

// 途中で切れたスロットは、どこで切れてもパニックせずにエラーになる。
func TestSlotTruncated(t *testing.T) {
	raw, _ := encodeSlot(t, 3, "netherite_pickaxe", survivalComponents(t), []string{"tool"})
	for n := 1; n < len(raw); n++ {
		if _, err := ReadSlot(protocol.NewDecoder(raw[:n])); err == nil {
			t.Fatalf("%d バイトで切れたのにエラーにならない", n)
		}
		if err := SkipSlot(protocol.NewDecoder(raw[:n])); err == nil {
			t.Fatalf("SkipSlot: %d バイトで切れたのにエラーにならない", n)
		}
	}
}

// SameItem / Equal: 部品が違えば重ならない。
func TestSlotSameItem(t *testing.T) {
	_, named := encodeSlot(t, 1, "apple", []comp{{"custom_name", wNBT(t, "a")}}, nil)
	a := Slot{Count: 3, Item: 5}
	b := Slot{Count: 7, Item: 5}
	c := Slot{Count: 3, Item: 5, Components: named}
	if !a.SameItem(b) || a.Equal(b) {
		t.Error("a と b")
	}
	if a.SameItem(c) || a.Equal(c) {
		t.Error("a と c（部品が違う）")
	}
	if !(Slot{}).Equal(Slot{Count: 0, Item: 9}) {
		t.Error("空どうし")
	}
}

// Set Container Content と Click Container の書き読み。
func TestContainerPackets(t *testing.T) {
	_, named := encodeSlot(t, 1, "apple", []comp{{"custom_name", wNBT(t, "a")}}, nil)
	cc := ContainerContent{WindowID: 3, StateID: 77, Slots: []Slot{{}, {Count: 2, Item: 5, Components: named}, {Count: 64, Item: 1}}, Carried: Slot{Count: 1, Item: 9}}
	e := protocol.NewEncoder()
	WriteContainerContent(e, cc)
	got, err := ReadContainerContent(protocol.NewDecoder(e.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	if got.WindowID != 3 || got.StateID != 77 || len(got.Slots) != 3 || !got.Slots[1].Equal(cc.Slots[1]) || !got.Carried.Equal(cc.Carried) {
		t.Errorf("Set Container Content: %+v", got)
	}

	ck := ContainerClick{WindowID: 0, StateID: 5, Slot: 36, Button: 1, Mode: ClickPickup,
		Changed: []ChangedSlot{{Slot: 36, Item: Slot{Count: 32, Item: 1}}}, Carried: Slot{Count: 32, Item: 1}}
	e = protocol.NewEncoder()
	WriteContainerClick(e, ck)
	gk, err := ReadContainerClick(protocol.NewDecoder(e.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	if gk.Slot != 36 || gk.Button != 1 || gk.StateID != 5 || len(gk.Changed) != 1 || gk.Changed[0].Item.Count != 32 || gk.Carried.Count != 32 {
		t.Errorf("Click Container: %+v", gk)
	}
}
