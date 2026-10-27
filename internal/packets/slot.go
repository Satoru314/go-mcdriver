package packets

// アイテムのスロット（ItemStack）と、その部品情報（data components）の読み飛ばし。
//
// 形は protocol 769（1.21.4）。部品情報は使う側に出さない（設計書）ので、読み進めるだけにする。
// 部品の種類ごとの形は、公式のサーバーの DataComponents の StreamCodec に合わせたもの。
// 通信用の形を持たない部品（map_decorations, debug_stick_state, recipes, lock, container_loot,
// intangible_projectile）は、サーバーが NBT で送るので NBT として読み飛ばす。

import (
	"errors"
	"fmt"

	"github.com/Satoru314/go-mcdriver/data"
	"github.com/Satoru314/go-mcdriver/internal/nbt"
	"github.com/Satoru314/go-mcdriver/internal/protocol"
)

// Slot はアイテムの入れ物 1 つ分。Count が 0 なら空（Item は 0）。部品情報は読み飛ばしてある。
type Slot struct {
	Count int32
	Item  data.ItemID
}

// Empty は空なら true。
func (s Slot) Empty() bool { return s.Count <= 0 }

// ErrUnsupportedData は、読み飛ばし方を知らない形のデータ（知らない部品の種類、
// registry に無い中身をその場で送る形など）に出会ったときのエラー。
// これが返ったら、そのパケットの残りは読めない（パケットの枠は正しいので、接続は続けられる）。
var ErrUnsupportedData = errors.New("packets: 読み飛ばし方を知らない形のデータ")

// maxSlotDepth は入れ物の中の入れ物（シュルカーボックスの中身など）をたどる深さの上限。
const maxSlotDepth = 16

// ReadSlot はスロット（空でもよい形）を 1 つ読む。部品情報は読み飛ばす。
func ReadSlot(d *protocol.Decoder) (Slot, error) {
	s := skipper{d: d}
	slot := s.slot()
	return slot, s.err
}

// SkipComponents は部品情報の差分（DataComponentPatch: 足す数、消す数、足す部品の中身、消す部品の種類）を読み飛ばす。
func SkipComponents(d *protocol.Decoder) error {
	s := skipper{d: d}
	s.components()
	return s.err
}

// WriteSlot はスロットを部品情報なしで書く（テストの偽のサーバー用）。
func WriteSlot(e *protocol.Encoder, s Slot) {
	if s.Empty() {
		e.WriteVarInt(0)
		return
	}
	e.WriteVarInt(s.Count)
	e.WriteVarInt(int32(s.Item))
	e.WriteVarInt(0) // 足す部品の数
	e.WriteVarInt(0) // 消す部品の数
}

// ---- 読み飛ばしの道具 ----

// skipper は読み飛ばしを続けて書くための道具。一度エラーになったら、あとは何もしない。
type skipper struct {
	d     *protocol.Decoder
	err   error
	depth int
}

func (s *skipper) fail(err error) {
	if s.err == nil {
		s.err = err
	}
}

func (s *skipper) varint() int32 {
	if s.err != nil {
		return 0
	}
	v, err := s.d.ReadVarInt()
	s.fail(err)
	return v
}

func (s *skipper) varlong() {
	if s.err != nil {
		return
	}
	_, err := s.d.ReadVarLong()
	s.fail(err)
}

func (s *skipper) bytes(n int) {
	if s.err != nil {
		return
	}
	s.fail(s.d.Skip(n))
}

func (s *skipper) boolean() bool {
	if s.err != nil {
		return false
	}
	v, err := s.d.ReadBool()
	s.fail(err)
	return v
}

func (s *skipper) str(max int) {
	if s.err != nil {
		return
	}
	_, err := s.d.ReadString(max)
	s.fail(err)
}

func (s *skipper) ident() { s.str(32767) }

func (s *skipper) nbt() {
	if s.err != nil {
		return
	}
	s.fail(nbt.SkipNetwork(s.d))
}

// count は並びの長さを読む。残りのバイト数より多ければ壊れたデータとする（1 つは 1 バイト以上なので）。
func (s *skipper) count() int {
	n := s.varint()
	if s.err != nil {
		return 0
	}
	if n < 0 || int(n) > s.d.Remaining() {
		s.fail(fmt.Errorf("packets: 並びの長さ %d が正しくない（残り %d バイト）", n, s.d.Remaining()))
		return 0
	}
	return int(n)
}

// list は長さ（VarInt）と、その数だけ f を読む。
func (s *skipper) list(f func()) {
	n := s.count()
	for i := 0; i < n && s.err == nil; i++ {
		f()
	}
}

// opt は有無（Bool）と、有れば f を読む。
func (s *skipper) opt(f func()) {
	if s.boolean() && s.err == nil {
		f()
	}
}

// either は左右（Bool、true が左）と、その中身を読む。
func (s *skipper) either(left, right func()) {
	if s.err != nil {
		return
	}
	if s.boolean() {
		left()
	} else {
		right()
	}
}

// holderSet は registry の項目の集まり（0 ならタグの名前、n なら n-1 個の番号）を読む。
func (s *skipper) holderSet() {
	n := s.varint()
	if s.err != nil {
		return
	}
	if n == 0 {
		s.ident()
		return
	}
	if n < 0 || int(n-1) > s.d.Remaining() {
		s.fail(fmt.Errorf("packets: HolderSet の長さ %d が正しくない", n))
		return
	}
	for i := int32(0); i < n-1 && s.err == nil; i++ {
		s.varint()
	}
}

// holder は registry の項目（番号+1）か、0 ならその場で送る中身（direct）を読む。
// direct が nil なら、その場で送る形は読めないとしてエラーにする。
func (s *skipper) holder(name string, direct func()) {
	id := s.varint()
	if s.err != nil || id != 0 {
		return
	}
	if direct == nil {
		s.fail(fmt.Errorf("%w: %s をその場で送る形", ErrUnsupportedData, name))
		return
	}
	direct()
}

// soundEvent は Holder<SoundEvent>（その場で送る形は 名前 + 届く距離（Optional<Float>））。
func (s *skipper) soundEvent() {
	s.holder("sound_event", func() {
		s.ident()
		s.opt(func() { s.bytes(4) })
	})
}

// mobEffectDetails は MobEffectInstance.Details（強さ、時間、環境から、粒子、アイコン、隠れた効果）。
func (s *skipper) mobEffectDetails() {
	if s.depth > maxSlotDepth {
		s.fail(errors.New("packets: 効果の入れ子が深すぎる"))
		return
	}
	s.varint()
	s.varint()
	s.boolean()
	s.boolean()
	s.boolean()
	s.depth++
	s.opt(s.mobEffectDetails)
	s.depth--
}

// mobEffect は MobEffectInstance（効果の番号 + Details）。
func (s *skipper) mobEffect() {
	s.varint()
	s.mobEffectDetails()
}

// consumeEffect は ConsumeEffect（種類の番号 + 種類ごとの中身）。
func (s *skipper) consumeEffect() {
	t := s.varint()
	if s.err != nil {
		return
	}
	switch registryName(consumeEffectTypes, t) {
	case "apply_effects":
		s.list(s.mobEffect)
		s.bytes(4) // probability
	case "remove_effects":
		s.holderSet()
	case "clear_all_effects":
	case "teleport_randomly":
		s.bytes(4) // diameter
	case "play_sound":
		s.soundEvent()
	default:
		s.fail(fmt.Errorf("%w: consume_effect_type %d", ErrUnsupportedData, t))
	}
}

// blockPredicate は BlockPredicate（ブロックの集まり、状態の条件、NBT の条件。どれも有無つき）。
func (s *skipper) blockPredicate() {
	s.opt(s.holderSet)
	s.opt(func() {
		s.list(func() {
			s.str(32767) // 状態の名前
			s.either(
				func() { s.str(32767) }, // ちょうどの値
				func() { s.opt(func() { s.str(32767) }); s.opt(func() { s.str(32767) }) }, // 範囲
			)
		})
	})
	s.opt(s.nbt)
}

// enchantments は ItemEnchantments（番号 → 段階の対応 + 説明に出すか）。
func (s *skipper) enchantments() {
	s.list(func() { s.varint(); s.varint() })
	s.boolean()
}

// fireworkExplosion は FireworkExplosion（形、色の並び、消える色の並び、尾、きらめき）。
func (s *skipper) fireworkExplosion() {
	s.varint()
	s.list(func() { s.bytes(4) })
	s.list(func() { s.bytes(4) })
	s.boolean()
	s.boolean()
}

// filterableString は Filterable<String>（元の文字列 + 有無つきの言い換え）。
func (s *skipper) filterableString(max int) {
	s.str(max)
	s.opt(func() { s.str(max) })
}

// slot は ItemStack（空でもよい形）を読み、数と種類を返す。
func (s *skipper) slot() Slot {
	n := s.varint()
	if s.err != nil || n <= 0 {
		return Slot{}
	}
	id := s.varint()
	if s.depth > maxSlotDepth {
		s.fail(errors.New("packets: アイテムの入れ子が深すぎる"))
		return Slot{}
	}
	s.depth++
	s.components()
	s.depth--
	return Slot{Count: n, Item: data.ItemID(id)}
}

// components は DataComponentPatch を読み飛ばす。
func (s *skipper) components() {
	add := s.count()
	remove := s.count()
	for i := 0; i < add && s.err == nil; i++ {
		s.component(s.varint())
	}
	for i := 0; i < remove && s.err == nil; i++ {
		s.varint()
	}
}

// component は部品 1 つの中身を読み飛ばす。
func (s *skipper) component(t int32) {
	if s.err != nil {
		return
	}
	name := registryName(componentTypes, t)
	switch name {
	// 中身なし
	case "hide_additional_tooltip", "hide_tooltip", "creative_slot_lock", "glider":

	// NBT（文字の部品も NBT）
	case "custom_data", "custom_name", "item_name", "entity_data", "bucket_entity_data", "block_entity_data",
		// 通信用の形を持たないもの（サーバーは NBT で送る）
		"intangible_projectile", "map_decorations", "debug_stick_state", "recipes", "lock", "container_loot":
		s.nbt()

	// VarInt 1 つ
	case "max_stack_size", "max_damage", "damage", "rarity", "repair_cost", "enchantable", "map_id",
		"map_post_processing", "ominous_bottle_amplifier", "base_color":
		s.varint()

	// Bool 1 つ
	case "unbreakable", "enchantment_glint_override":
		s.boolean()

	// Int 1 つ
	case "map_color":
		s.bytes(4)

	// 名前（Identifier）1 つ
	case "item_model", "damage_resistant", "tooltip_style", "note_block_sound":
		s.ident()

	case "lore":
		s.list(s.nbt)
	case "enchantments", "stored_enchantments":
		s.enchantments()
	case "can_place_on", "can_break":
		s.list(s.blockPredicate)
		s.boolean()
	case "attribute_modifiers":
		s.list(func() {
			s.varint() // 属性の番号
			s.ident()  // 修正の名前
			s.bytes(8) // 量（double）
			s.varint() // 足し方
			s.varint() // 効く場所
		})
		s.boolean()
	case "custom_model_data": // 1.21.4: float の並び、bool の並び、文字列の並び、色（int）の並び
		s.list(func() { s.bytes(4) })
		s.list(func() { s.bytes(1) })
		s.list(func() { s.str(32767) })
		s.list(func() { s.bytes(4) })
	case "food":
		s.varint()  // 満腹度
		s.bytes(4)  // 隠し満腹度
		s.boolean() // いつでも食べられるか
	case "consumable":
		s.bytes(4) // 食べる秒数
		s.varint() // 動き
		s.soundEvent()
		s.boolean() // 粒子を出すか
		s.list(s.consumeEffect)
	case "use_remainder":
		s.slot()
	case "use_cooldown":
		s.bytes(4)
		s.opt(s.ident)
	case "tool":
		s.list(func() {
			s.holderSet()
			s.opt(func() { s.bytes(4) })
			s.opt(func() { s.bytes(1) })
		})
		s.bytes(4) // 既定の掘る速さ
		s.varint() // 1 ブロックで減る耐久値
	case "equippable":
		s.varint() // 場所
		s.soundEvent()
		s.opt(s.ident)     // 見た目
		s.opt(s.ident)     // 画面にかぶせる絵
		s.opt(s.holderSet) // 着けられるエンティティ
		s.boolean()
		s.boolean()
		s.boolean()
	case "repairable":
		s.holderSet()
	case "death_protection":
		s.list(s.consumeEffect)
	case "dyed_color":
		s.bytes(4)
		s.boolean()
	case "charged_projectiles", "bundle_contents", "container":
		s.list(func() { s.slot() })
	case "potion_contents":
		s.opt(func() { s.varint() })   // ポーションの番号
		s.opt(func() { s.bytes(4) })   // 色
		s.list(s.mobEffect)            // 追加の効果
		s.opt(func() { s.str(32767) }) // 名前
	case "suspicious_stew_effects":
		s.list(func() { s.varint(); s.varint() })
	case "writable_book_content":
		s.list(func() { s.filterableString(1024) })
	case "written_book_content":
		s.filterableString(32) // 題名
		s.str(32767)           // 著者
		s.varint()             // 写しの世代
		s.list(func() { s.nbt(); s.opt(s.nbt) })
		s.boolean() // 解決済みか
	case "trim":
		s.holder("trim_material", nil)
		s.holder("trim_pattern", nil)
		s.boolean()
	case "instrument":
		s.holder("instrument", func() {
			s.soundEvent()
			s.bytes(4) // 使う時間
			s.bytes(4) // 届く距離
			s.nbt()    // 説明
		})
	case "jukebox_playable":
		s.either(
			func() {
				s.holder("jukebox_song", func() {
					s.soundEvent()
					s.nbt()    // 説明
					s.bytes(4) // 長さ（秒）
					s.varint() // コンパレーターの出力
				})
			},
			s.ident,
		)
		s.boolean()
	case "lodestone_tracker":
		s.opt(func() { s.ident(); s.bytes(8) })
		s.boolean()
	case "firework_explosion":
		s.fireworkExplosion()
	case "fireworks":
		s.varint()
		s.list(s.fireworkExplosion)
	case "profile":
		s.opt(func() { s.str(16) })
		s.opt(func() { s.bytes(16) })
		s.list(func() {
			s.str(64)
			s.str(32767)
			s.opt(func() { s.str(1024) })
		})
	case "banner_patterns":
		s.list(func() {
			s.holder("banner_pattern", func() { s.ident(); s.str(32767) })
			s.varint() // 色
		})
	case "pot_decorations":
		s.list(func() { s.varint() })
	case "block_state":
		s.list(func() { s.str(32767); s.str(32767) })
	case "bees":
		s.list(func() { s.nbt(); s.varint(); s.varint() })
	default:
		s.fail(fmt.Errorf("%w: 部品の種類 %d（%q）", ErrUnsupportedData, t, name))
	}
}

// ---- registry の番号と名前 ----

var (
	componentTypes     = registryEntries("data_component_type")
	consumeEffectTypes = registryEntries("consume_effect_type")
	particleTypes      = registryEntries("particle_type")
)

func registryEntries(name string) []string {
	r, ok := data.RegistryByName(name)
	if !ok {
		return nil
	}
	return r.Entries
}

// registryName は番号の名前を返す。範囲の外なら空。
func registryName(entries []string, id int32) string {
	if id < 0 || int(id) >= len(entries) {
		return ""
	}
	return entries[id]
}
