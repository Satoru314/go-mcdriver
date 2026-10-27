package data

// ItemID はアイテムの registry の番号（protocol_id）。持ち物のパケットで使う。
type ItemID int32

// Item は1種類のアイテム。値は公式データ（reports/items.json）の既定の部品情報（components）から取ったもの。
type Item struct {
	ID           ItemID
	Name         string      // "minecraft:" を外した名前（例 "diamond_pickaxe"）
	MaxStackSize int         // 1スロットに重ねられる数
	MaxDamage    int         // 耐久値（無いものは 0）
	Food         *Food       // 食べ物（minecraft:food）。食べ物でなければ nil
	Consumable   *Consumable // 使うと消費される（食べる・飲む）もの（minecraft:consumable）。でなければ nil
	Tool         *Tool       // 道具（minecraft:tool）。道具でなければ nil
}

// Food は食べ物の回復量。
type Food struct {
	Nutrition    int     // 満腹度の回復量
	Saturation   float32 // 隠し満腹度の回復量
	CanAlwaysEat bool    // 満腹でも食べられる
}

// Consumable は使うと消費されるアイテムの情報。公式データで省略された値は 0 や空になる
// （そのときはゲーム側の既定値が使われる。既定値は公式データに含まれないのでここには無い）。
type Consumable struct {
	ConsumeSeconds float32 // 使い終わるまでの秒数。0 は省略
	Animation      string  // "eat", "drink" など。空は省略
}

// Tool は道具の情報。
type Tool struct {
	Rules              []ToolRule
	DefaultMiningSpeed float32 // どの規則にも当たらないときの掘る速さ。0 は省略（ゲーム側の既定値が使われる）
	DamagePerBlock     int     // 1ブロック掘るごとに減る耐久値
	HasDamagePerBlock  bool    // DamagePerBlock が公式データにあったか（無ければゲーム側の既定値）
}

// ToolRule は道具の規則の1つ。Blocks のどれか、または Tag のタグのブロックに当てはまる。
type ToolRule struct {
	Tag                string    // ブロックのタグ（"#" と "minecraft:" を外した名前）。直接ブロックを指すときは空
	Blocks             []BlockID // 直接指すブロック
	Speed              float32
	HasSpeed           bool
	CorrectForDrops    bool
	HasCorrectForDrops bool
}

// Matches はブロックがこの規則に当てはまるかを返す。
func (r *ToolRule) Matches(b BlockID) bool {
	if r.Tag != "" {
		return BlockHasTag(b, r.Tag)
	}
	for _, x := range r.Blocks {
		if x == b {
			return true
		}
	}
	return false
}

// MiningSpeed は、規則を順に見て、速さを持つ最初の当てはまる規則の速さを返す。
// 当てはまる規則が無ければ ok は false（そのときは DefaultMiningSpeed かゲーム側の既定値）。
func (t *Tool) MiningSpeed(b BlockID) (speed float32, ok bool) {
	for i := range t.Rules {
		r := &t.Rules[i]
		if r.HasSpeed && r.Matches(b) {
			return r.Speed, true
		}
	}
	return 0, false
}

// CorrectForDrops は、規則を順に見て、correct_for_drops を持つ最初の当てはまる規則の値を返す。
// 当てはまる規則が無ければ false。
// 注意: ブロックが「適した道具でないと何も落とさない」かどうかは公式データに無いので、ここだけでは落ちるかは決まらない。
func (t *Tool) CorrectForDrops(b BlockID) bool {
	for i := range t.Rules {
		r := &t.Rules[i]
		if r.HasCorrectForDrops && r.Matches(b) {
			return r.CorrectForDrops
		}
	}
	return false
}

var itemByName = func() map[string]*Item {
	m := make(map[string]*Item, len(items))
	for i := range items {
		m[items[i].Name] = &items[i]
	}
	return m
}()

// Items は全アイテムを ID の順に返す（中身を書き換えないこと）。
func Items() []Item { return items }

// ItemByName は名前でアイテムを引く。"minecraft:" は付いていてもいなくてもよい。
func ItemByName(name string) (*Item, bool) {
	it, ok := itemByName[trimNS(name)]
	return it, ok
}

// ItemByID は ID でアイテムを引く。
func ItemByID(id ItemID) (*Item, bool) {
	if id < 0 || int(id) >= len(items) {
		return nil, false
	}
	return &items[id], true
}
