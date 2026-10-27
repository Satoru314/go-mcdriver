package data

// Ingredient はレシピの材料の1枠。アイテムのタグか、アイテムの候補の一覧のどちらか。
type Ingredient struct {
	Tag   string   // アイテムのタグ（"#" と "minecraft:" を外した名前）
	Items []ItemID // 候補のアイテム（Tag が空のとき）
}

// IsEmpty は材料が指定されていないかを返す。
func (g Ingredient) IsEmpty() bool { return g.Tag == "" && len(g.Items) == 0 }

// Matches はアイテムがこの材料に当てはまるかを返す。
func (g Ingredient) Matches(it ItemID) bool {
	if g.Tag != "" {
		return ItemHasTag(it, g.Tag)
	}
	for _, x := range g.Items {
		if x == it {
			return true
		}
	}
	return false
}

// ItemStack はレシピの出来上がり。
type ItemStack struct {
	Item          ItemID
	Count         int  // 公式データで省略されたときは 1（アイテムの形式の既定値）
	HasComponents bool // 部品情報（components）付きの出来上がり（例 怪しげなシチュー）。部品情報そのものは持たない
}

// Recipe は1つのレシピ（data/minecraft/recipe/*.json）。Type によって使う欄が違う。
//
//   - crafting_shaped: Pattern, Key
//   - crafting_shapeless: Ingredients
//   - smelting, blasting, smoking, campfire_cooking: Input, Experience, CookingTime
//   - stonecutting: Input
//   - crafting_transmute: Input, Material
//   - smithing_transform: Template, Base, Addition
//   - smithing_trim: Template, Base, Addition（出来上がりは無い）
//   - crafting_special_*, crafting_decorated_pot: ゲームの中の処理で決まるので、材料の情報は無い
type Recipe struct {
	Name        string // ファイル名（例 "oak_planks"）
	Type        string // "minecraft:" を外した種類（例 "crafting_shaped"）
	Category    string
	Group       string
	Pattern     []string
	Key         map[rune]Ingredient
	Ingredients []Ingredient
	Input       Ingredient
	Material    Ingredient
	Template    Ingredient
	Base        Ingredient
	Addition    Ingredient
	Result      ItemStack
	HasResult   bool
	Experience  float32
	CookingTime int // ティック
}

// Recipes は全レシピを名前の順に返す（中身を書き換えないこと）。
func Recipes() []Recipe { return recipes }

var recipeByName = func() map[string]*Recipe {
	m := make(map[string]*Recipe, len(recipes))
	for i := range recipes {
		m[recipes[i].Name] = &recipes[i]
	}
	return m
}()

// RecipeByName は名前でレシピを引く。
func RecipeByName(name string) (*Recipe, bool) {
	r, ok := recipeByName[trimNS(name)]
	return r, ok
}

// RecipesFor は、出来上がりが it のレシピを返す。
func RecipesFor(it ItemID) []*Recipe {
	var out []*Recipe
	for i := range recipes {
		if recipes[i].HasResult && recipes[i].Result.Item == it {
			out = append(out, &recipes[i])
		}
	}
	return out
}
