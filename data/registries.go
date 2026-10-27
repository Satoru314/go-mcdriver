package data

// Registry は1つの registry（reports/registries.json）。Entries の添字が protocol_id。
type Registry struct {
	Name    string   // "minecraft:" を外した名前（例 "entity_type"）
	Default string   // 既定の項目（無ければ空）
	Entries []string // "minecraft:" を外した項目の名前（ほかの名前空間はそのまま。例 "brigadier:string"）
}

// ID は項目の protocol_id を返す。
func (r *Registry) ID(entry string) (int32, bool) {
	entry = trimNS(entry)
	for i, e := range r.Entries {
		if e == entry {
			return int32(i), true
		}
	}
	return 0, false
}

// RegistryByName は名前で registry を引く（例 "entity_type"）。中身を書き換えないこと。
func RegistryByName(name string) (*Registry, bool) {
	r, ok := registries[trimNS(name)]
	return r, ok
}

// RegistryNames は全 registry の名前を返す（昇順）。
func RegistryNames() []string { return sortedKeys(registries) }
