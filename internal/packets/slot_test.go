package packets

import (
	"errors"
	"testing"

	"github.com/Satoru314/go-mcdriver/data"
	"github.com/Satoru314/go-mcdriver/internal/nbt"
	"github.com/Satoru314/go-mcdriver/internal/protocol"
)

// regID は registry の名前から番号を引く（無ければテストを失敗にする）。
func regID(t *testing.T, reg, name string) int32 {
	t.Helper()
	r, ok := data.RegistryByName(reg)
	if !ok {
		t.Fatalf("registry %s が無い", reg)
	}
	id, ok := r.ID(name)
	if !ok {
		t.Fatalf("%s に %s が無い", reg, name)
	}
	return id
}

func itemID(t *testing.T, name string) data.ItemID {
	t.Helper()
	it, ok := data.ItemByName(name)
	if !ok {
		t.Fatalf("アイテム %s が無い", name)
	}
	return it.ID
}

// wstr は文字列を書く。
func wstr(t *testing.T, e *protocol.Encoder, s string) {
	t.Helper()
	if err := e.WriteString(s, 32767); err != nil {
		t.Fatal(err)
	}
}

// wtext は文字の部品（NBT の文字列）を書く。
func wtext(t *testing.T, e *protocol.Encoder, s string) {
	t.Helper()
	if err := nbt.WriteNetwork(e, s); err != nil {
		t.Fatal(err)
	}
}

// component は部品 1 つ（種類の名前と、中身を書く関数）。
type component struct {
	name  string
	write func(e *protocol.Encoder)
}

// writeStack は部品つきのスロットを書く。
func writeStack(t *testing.T, e *protocol.Encoder, count int32, item data.ItemID, add []component, remove []string) {
	t.Helper()
	e.WriteVarInt(count)
	e.WriteVarInt(int32(item))
	e.WriteVarInt(int32(len(add)))
	e.WriteVarInt(int32(len(remove)))
	for _, c := range add {
		e.WriteVarInt(regID(t, "data_component_type", c.name))
		c.write(e)
	}
	for _, r := range remove {
		e.WriteVarInt(regID(t, "data_component_type", r))
	}
}

// 部品情報のついたスロットを読み、数と種類が合い、ちょうど最後まで読み進めること。
func TestReadSlotComponents(t *testing.T) {
	sword := itemID(t, "diamond_sword")
	stone := itemID(t, "stone")
	apple := itemID(t, "apple")
	sound := func(e *protocol.Encoder) { e.WriteVarInt(5) } // registry の番号 4
	directSound := func(e *protocol.Encoder) {
		e.WriteVarInt(0)
		wstr(t, e, "minecraft:custom.sound")
		e.WriteBool(true)
		e.WriteFloat(16)
	}
	effect := func(e *protocol.Encoder) {
		e.WriteVarInt(1)  // 効果の番号
		e.WriteVarInt(2)  // 強さ
		e.WriteVarInt(60) // 時間
		e.WriteBool(false)
		e.WriteBool(true)
		e.WriteBool(true)
		e.WriteBool(true) // 隠れた効果あり
		e.WriteVarInt(0)
		e.WriteVarInt(20)
		e.WriteBool(false)
		e.WriteBool(false)
		e.WriteBool(false)
		e.WriteBool(false)
	}
	consumeEffects := func(e *protocol.Encoder) {
		e.WriteVarInt(5)
		e.WriteVarInt(regID(t, "consume_effect_type", "apply_effects"))
		e.WriteVarInt(1)
		effect(e)
		e.WriteFloat(1)
		e.WriteVarInt(regID(t, "consume_effect_type", "remove_effects"))
		e.WriteVarInt(0)
		wstr(t, e, "minecraft:harmful")
		e.WriteVarInt(regID(t, "consume_effect_type", "clear_all_effects"))
		e.WriteVarInt(regID(t, "consume_effect_type", "teleport_randomly"))
		e.WriteFloat(16)
		e.WriteVarInt(regID(t, "consume_effect_type", "play_sound"))
		directSound(e)
	}
	explosion := func(e *protocol.Encoder) {
		e.WriteVarInt(1)
		e.WriteVarInt(2)
		e.WriteInt(0xff0000)
		e.WriteInt(0x00ff00)
		e.WriteVarInt(0)
		e.WriteBool(true)
		e.WriteBool(false)
	}

	inner := func(e *protocol.Encoder) {
		// 中に部品つきのアイテムを持つアイテム（入れ物の入れ子）
		e.WriteVarInt(2)
		writeStack(t, e, 3, apple, []component{{"damage", func(e *protocol.Encoder) { e.WriteVarInt(4) }}}, nil)
		e.WriteVarInt(0) // 空のスロット
	}

	comps := []component{
		{"custom_data", func(e *protocol.Encoder) { slotMust(t, nbt.WriteNetwork(e, map[string]any{"a": int32(1)})) }},
		{"max_stack_size", func(e *protocol.Encoder) { e.WriteVarInt(16) }},
		{"damage", func(e *protocol.Encoder) { e.WriteVarInt(100) }},
		{"unbreakable", func(e *protocol.Encoder) { e.WriteBool(true) }},
		{"custom_name", func(e *protocol.Encoder) { wtext(t, e, "けん") }},
		{"item_model", func(e *protocol.Encoder) { wstr(t, e, "minecraft:stick") }},
		{"lore", func(e *protocol.Encoder) { e.WriteVarInt(2); wtext(t, e, "1"); wtext(t, e, "2") }},
		{"rarity", func(e *protocol.Encoder) { e.WriteVarInt(2) }},
		{"enchantments", func(e *protocol.Encoder) {
			e.WriteVarInt(2)
			e.WriteVarInt(10)
			e.WriteVarInt(3)
			e.WriteVarInt(11)
			e.WriteVarInt(1)
			e.WriteBool(true)
		}},
		{"can_break", func(e *protocol.Encoder) {
			e.WriteVarInt(1)
			e.WriteBool(true) // ブロックの集まり
			e.WriteVarInt(3)  // 2 個
			e.WriteVarInt(1)
			e.WriteVarInt(2)
			e.WriteBool(true) // 状態の条件
			e.WriteVarInt(2)
			wstr(t, e, "facing")
			e.WriteBool(true)
			wstr(t, e, "north")
			wstr(t, e, "age")
			e.WriteBool(false)
			e.WriteBool(true)
			wstr(t, e, "1")
			e.WriteBool(false)
			e.WriteBool(true) // NBT の条件
			slotMust(t, nbt.WriteNetwork(e, map[string]any{}))
			e.WriteBool(false)
		}},
		{"attribute_modifiers", func(e *protocol.Encoder) {
			e.WriteVarInt(1)
			e.WriteVarInt(3)
			wstr(t, e, "minecraft:base_attack_damage")
			e.WriteDouble(6)
			e.WriteVarInt(0)
			e.WriteVarInt(1)
			e.WriteBool(true)
		}},
		{"custom_model_data", func(e *protocol.Encoder) {
			e.WriteVarInt(1)
			e.WriteFloat(1.5)
			e.WriteVarInt(2)
			e.WriteBool(true)
			e.WriteBool(false)
			e.WriteVarInt(1)
			wstr(t, e, "x")
			e.WriteVarInt(1)
			e.WriteInt(7)
		}},
		{"hide_tooltip", func(e *protocol.Encoder) {}},
		{"intangible_projectile", func(e *protocol.Encoder) { slotMust(t, nbt.WriteNetwork(e, map[string]any{})) }},
		{"food", func(e *protocol.Encoder) { e.WriteVarInt(4); e.WriteFloat(2.4); e.WriteBool(false) }},
		{"consumable", func(e *protocol.Encoder) {
			e.WriteFloat(1.6)
			e.WriteVarInt(1)
			sound(e)
			e.WriteBool(true)
			consumeEffects(e)
		}},
		{"use_remainder", func(e *protocol.Encoder) {
			writeStack(t, e, 1, stone, []component{{"rarity", func(e *protocol.Encoder) { e.WriteVarInt(1) }}}, nil)
		}},
		{"use_cooldown", func(e *protocol.Encoder) { e.WriteFloat(1); e.WriteBool(true); wstr(t, e, "minecraft:g") }},
		{"damage_resistant", func(e *protocol.Encoder) { wstr(t, e, "minecraft:is_fire") }},
		{"tool", func(e *protocol.Encoder) {
			e.WriteVarInt(2)
			e.WriteVarInt(0)
			wstr(t, e, "minecraft:mineable/pickaxe")
			e.WriteBool(true)
			e.WriteFloat(8)
			e.WriteBool(true)
			e.WriteBool(true)
			e.WriteVarInt(2)
			e.WriteVarInt(5)
			e.WriteBool(false)
			e.WriteBool(false)
			e.WriteFloat(1)
			e.WriteVarInt(1)
		}},
		{"equippable", func(e *protocol.Encoder) {
			e.WriteVarInt(4)
			directSound(e)
			e.WriteBool(true)
			wstr(t, e, "minecraft:diamond")
			e.WriteBool(false)
			e.WriteBool(true)
			e.WriteVarInt(2)
			e.WriteVarInt(7)
			e.WriteBool(true)
			e.WriteBool(true)
			e.WriteBool(false)
		}},
		{"repairable", func(e *protocol.Encoder) { e.WriteVarInt(2); e.WriteVarInt(9) }},
		{"glider", func(e *protocol.Encoder) {}},
		{"death_protection", consumeEffects},
		{"dyed_color", func(e *protocol.Encoder) { e.WriteInt(0x123456); e.WriteBool(true) }},
		{"map_decorations", func(e *protocol.Encoder) { slotMust(t, nbt.WriteNetwork(e, map[string]any{"x": "y"})) }},
		{"bundle_contents", inner},
		{"potion_contents", func(e *protocol.Encoder) {
			e.WriteBool(true)
			e.WriteVarInt(3)
			e.WriteBool(true)
			e.WriteInt(0xff)
			e.WriteVarInt(1)
			effect(e)
			e.WriteBool(true)
			wstr(t, e, "なまえ")
		}},
		{"suspicious_stew_effects", func(e *protocol.Encoder) { e.WriteVarInt(1); e.WriteVarInt(2); e.WriteVarInt(160) }},
		{"writable_book_content", func(e *protocol.Encoder) {
			e.WriteVarInt(2)
			wstr(t, e, "p1")
			e.WriteBool(false)
			wstr(t, e, "p2")
			e.WriteBool(true)
			wstr(t, e, "p2f")
		}},
		{"written_book_content", func(e *protocol.Encoder) {
			wstr(t, e, "title")
			e.WriteBool(false)
			wstr(t, e, "author")
			e.WriteVarInt(0)
			e.WriteVarInt(1)
			wtext(t, e, "page")
			e.WriteBool(true)
			wtext(t, e, "pagef")
			e.WriteBool(true)
		}},
		{"trim", func(e *protocol.Encoder) { e.WriteVarInt(2); e.WriteVarInt(3); e.WriteBool(true) }},
		{"instrument", func(e *protocol.Encoder) {
			e.WriteVarInt(0)
			sound(e)
			e.WriteFloat(7)
			e.WriteFloat(256)
			wtext(t, e, "horn")
		}},
		{"jukebox_playable", func(e *protocol.Encoder) {
			e.WriteBool(true)
			e.WriteVarInt(0)
			directSound(e)
			wtext(t, e, "song")
			e.WriteFloat(120)
			e.WriteVarInt(3)
			e.WriteBool(true)
		}},
		{"lodestone_tracker", func(e *protocol.Encoder) {
			e.WriteBool(true)
			wstr(t, e, "minecraft:overworld")
			slotMust(t, e.WritePosition(protocol.Position{X: 1, Y: 2, Z: 3}))
			e.WriteBool(true)
		}},
		{"firework_explosion", explosion},
		{"fireworks", func(e *protocol.Encoder) { e.WriteVarInt(2); e.WriteVarInt(1); explosion(e) }},
		{"profile", func(e *protocol.Encoder) {
			e.WriteBool(true)
			wstr(t, e, "Steve")
			e.WriteBool(true)
			e.WriteUUID(protocol.UUID{1, 2, 3})
			e.WriteVarInt(1)
			wstr(t, e, "textures")
			wstr(t, e, "abc")
			e.WriteBool(true)
			wstr(t, e, "sig")
		}},
		{"note_block_sound", func(e *protocol.Encoder) { wstr(t, e, "minecraft:x") }},
		{"banner_patterns", func(e *protocol.Encoder) {
			e.WriteVarInt(2)
			e.WriteVarInt(4)
			e.WriteVarInt(1)
			e.WriteVarInt(0)
			wstr(t, e, "minecraft:custom")
			wstr(t, e, "block.minecraft.banner.custom")
			e.WriteVarInt(14)
		}},
		{"base_color", func(e *protocol.Encoder) { e.WriteVarInt(3) }},
		{"pot_decorations", func(e *protocol.Encoder) {
			e.WriteVarInt(4)
			for range 4 {
				e.WriteVarInt(int32(stone))
			}
		}},
		{"container", inner},
		{"block_state", func(e *protocol.Encoder) { e.WriteVarInt(1); wstr(t, e, "half"); wstr(t, e, "top") }},
		{"bees", func(e *protocol.Encoder) {
			e.WriteVarInt(1)
			slotMust(t, nbt.WriteNetwork(e, map[string]any{"id": "minecraft:bee"}))
			e.WriteVarInt(10)
			e.WriteVarInt(600)
		}},
		{"lock", func(e *protocol.Encoder) { slotMust(t, nbt.WriteNetwork(e, map[string]any{})) }},
	}

	// 部品 1 つずつと、全部まとめたもの
	for _, c := range comps {
		t.Run(c.name, func(t *testing.T) {
			e := protocol.NewEncoder()
			writeStack(t, e, 1, sword, []component{c}, []string{"food"})
			e.WriteUByte(0xAB) // 印
			d := protocol.NewDecoder(e.Bytes())
			s, err := ReadSlot(d)
			if err != nil {
				t.Fatal(err)
			}
			if s.Count != 1 || s.Item != sword {
				t.Errorf("スロット %+v", s)
			}
			if b, err := d.ReadUByte(); err != nil || b != 0xAB || d.Remaining() != 0 {
				t.Errorf("読み進めた位置がずれた: 次 %x, err %v, 残り %d", b, err, d.Remaining())
			}
		})
	}
	e := protocol.NewEncoder()
	writeStack(t, e, 64, stone, comps, nil)
	d := protocol.NewDecoder(e.Bytes())
	s, err := ReadSlot(d)
	if err != nil || s.Count != 64 || s.Item != stone || d.Remaining() != 0 {
		t.Errorf("全部まとめ: %+v %v 残り %d", s, err, d.Remaining())
	}
}

func TestReadSlotEmptyAndErrors(t *testing.T) {
	// 空のスロット
	e := protocol.NewEncoder()
	WriteSlot(e, Slot{})
	WriteSlot(e, Slot{Count: 5, Item: 7})
	d := protocol.NewDecoder(e.Bytes())
	if s, err := ReadSlot(d); err != nil || !s.Empty() {
		t.Errorf("空: %+v %v", s, err)
	}
	if s, err := ReadSlot(d); err != nil || s != (Slot{Count: 5, Item: 7}) {
		t.Errorf("部品なし: %+v %v", s, err)
	}

	// 知らない部品の種類
	e = protocol.NewEncoder()
	e.WriteVarInt(1)
	e.WriteVarInt(1)
	e.WriteVarInt(1)
	e.WriteVarInt(0)
	e.WriteVarInt(9999)
	if _, err := ReadSlot(protocol.NewDecoder(e.Bytes())); !errors.Is(err, ErrUnsupportedData) {
		t.Errorf("知らない部品: %v", err)
	}

	// その場で送る形の trim は読めない
	e = protocol.NewEncoder()
	writeStack(t, e, 1, 1, []component{{"trim", func(e *protocol.Encoder) { e.WriteVarInt(0) }}}, nil)
	if _, err := ReadSlot(protocol.NewDecoder(e.Bytes())); !errors.Is(err, ErrUnsupportedData) {
		t.Errorf("その場の trim: %v", err)
	}

	// 途中で切れたデータ
	e = protocol.NewEncoder()
	writeStack(t, e, 1, 1, []component{{"lore", func(e *protocol.Encoder) { e.WriteVarInt(3) }}}, nil)
	if _, err := ReadSlot(protocol.NewDecoder(e.Bytes())); err == nil {
		t.Error("切れたデータでエラーにならない")
	}

	// 入れ子が深すぎる
	e = protocol.NewEncoder()
	var nest func(n int)
	nest = func(n int) {
		if n == 0 {
			e.WriteVarInt(0)
			return
		}
		writeStack(t, e, 1, 1, []component{{"container", func(e *protocol.Encoder) { e.WriteVarInt(1); nest(n - 1) }}}, nil)
	}
	nest(40)
	if _, err := ReadSlot(protocol.NewDecoder(e.Bytes())); err == nil {
		t.Error("深すぎる入れ子でエラーにならない")
	}
}

func slotMust(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
