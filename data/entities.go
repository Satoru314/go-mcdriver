package data

// EntityTypeID はエンティティの種類の registry（entity_type）の番号。エンティティの出現のパケットで使う。
type EntityTypeID int32

// EntityType はエンティティの種類。ID と名前は公式データの entity_type の registry、
// 大きさは PrismarineJS の minecraft-data（entities.json）から取ったもの（生成時に名前と ID を突き合わせ済み）。
type EntityType struct {
	ID     EntityTypeID
	Name   string  // "minecraft:" を外した名前（例 "zombie"）
	Width  float32 // 当たり判定の幅（x と z の長さ。足元の中心から ±Width/2）。ゲームの中でも float
	Height float32 // 当たり判定の高さ（足元から）。子供や姿勢（しゃがみ、泳ぎ）による違いは入っていない
}

var entityTypeByName = func() map[string]*EntityType {
	m := make(map[string]*EntityType, len(entityTypes))
	for i := range entityTypes {
		m[entityTypes[i].Name] = &entityTypes[i]
	}
	return m
}()

// EntityTypes は全種類を ID の順に返す（中身を書き換えないこと）。
func EntityTypes() []EntityType { return entityTypes }

// EntityTypeByName は名前で種類を引く。"minecraft:" は付いていてもいなくてもよい。
func EntityTypeByName(name string) (*EntityType, bool) {
	e, ok := entityTypeByName[trimNS(name)]
	return e, ok
}

// EntityTypeByID は ID で種類を引く。
func EntityTypeByID(id EntityTypeID) (*EntityType, bool) {
	if id < 0 || int(id) >= len(entityTypes) {
		return nil, false
	}
	return &entityTypes[id], true
}
