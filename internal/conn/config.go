package conn

import (
	"fmt"
	"sync"

	"github.com/Satoru314/go-mcdriver/data"
	"github.com/Satoru314/go-mcdriver/internal/nbt"
	"github.com/Satoru314/go-mcdriver/internal/protocol"
)

// ClientInfo は Client Information で送る、クライアントの設定。
type ClientInfo struct {
	Locale       string // 例 "en_us"（16 文字まで）
	ViewDistance int8   // 描画距離（チャンク）
	MainHand     int32  // 0: 左, 1: 右
}

// DefaultClientInfo は特に指定がないときの設定。
var DefaultClientInfo = ClientInfo{Locale: "en_us", ViewDistance: 8, MainHand: 1}

// Brand は minecraft:brand で名乗るクライアントの名前。
const Brand = "go-mcdriver"

// RegistryEntry は Registry Data の 1 項目。
type RegistryEntry struct {
	ID   string // 例 "minecraft:overworld"
	Data any    // NBT を読んだ値。既知のデータパックにあるので省かれたときは nil
}

// Registries は設定の段階で受け取ったもの（Registry Data、Update Tags、有効な機能）を保管する。
// 複数の goroutine から読んでよい。
type Registries struct {
	mu       sync.RWMutex
	entries  map[string][]RegistryEntry    // registry の名前 → 項目（添字が番号）
	tags     map[string]map[string][]int32 // registry の名前 → タグの名前 → 項目の番号
	features []string                      // 有効な機能（Feature Flags）
}

// NewRegistries は空の保管場所を作る。
func NewRegistries() *Registries {
	return &Registries{
		entries: map[string][]RegistryEntry{},
		tags:    map[string]map[string][]int32{},
	}
}

// Entries は registry（例 "minecraft:dimension_type"）の項目の写しを返す。
func (r *Registries) Entries(registry string) []RegistryEntry {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return append([]RegistryEntry(nil), r.entries[registry]...)
}

// RegistryNames は受け取った registry の名前を返す（順不同）。
func (r *Registries) RegistryNames() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	names := make([]string, 0, len(r.entries))
	for k := range r.entries {
		names = append(names, k)
	}
	return names
}

// Tag は registry のタグ（例 "minecraft:block", "minecraft:mineable/pickaxe"）の項目の番号の写しを返す。
func (r *Registries) Tag(registry, tag string) ([]int32, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	ids, ok := r.tags[registry][tag]
	return append([]int32(nil), ids...), ok
}

// TagRegistryNames はタグを受け取った registry の名前を返す（順不同）。
func (r *Registries) TagRegistryNames() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	names := make([]string, 0, len(r.tags))
	for k := range r.tags {
		names = append(names, k)
	}
	return names
}

// Features は有効な機能の写しを返す。
func (r *Registries) Features() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return append([]string(nil), r.features...)
}

// Configure は設定の段階（Configuration）を、Finish Configuration に Acknowledge を返すまで進める。
// sendInfo が true なら、最初に Client Information と minecraft:brand を送る。
// 戻ったとき、接続は Play の段階にある。
func Configure(c *Conn, info ClientInfo, reg *Registries, sendInfo bool) error {
	if sendInfo {
		if err := SendClientInfo(c, data.ConfigurationServerboundClientInformation, info); err != nil {
			return err
		}
		if err := c.Send(data.ConfigurationServerboundCustomPayload, func(e *protocol.Encoder) error {
			if err := e.WriteIdentifier("minecraft:brand"); err != nil {
				return err
			}
			return e.WriteString(Brand, 32767)
		}); err != nil {
			return fmt.Errorf("brand を送る: %w", err)
		}
	}
	for {
		p, err := c.ReadPacket()
		if err != nil {
			return fmt.Errorf("設定の段階で読む: %w", err)
		}
		d := protocol.NewDecoder(p.Data)
		switch p.ID {
		case data.ConfigurationClientboundDisconnect:
			reason, err := ReadTextNBT(d)
			if err != nil {
				return fmt.Errorf("Disconnect (configuration): %w", err)
			}
			return &DisconnectError{State: data.StateConfiguration, Reason: reason}

		case data.ConfigurationClientboundFinishConfiguration:
			if err := c.Send(data.ConfigurationServerboundFinishConfiguration, nil); err != nil {
				return fmt.Errorf("Acknowledge Finish Configuration を送る: %w", err)
			}
			return nil

		case data.ConfigurationClientboundKeepAlive:
			if err := ReplyKeepAlive(c, d, data.ConfigurationServerboundKeepAlive); err != nil {
				return err
			}

		case data.ConfigurationClientboundPing:
			if err := ReplyPing(c, d, data.ConfigurationServerboundPong); err != nil {
				return err
			}

		case data.ConfigurationClientboundSelectKnownPacks:
			// 公式のデータパック（minecraft:core）を「知らない」と答える。
			// こうするとサーバーは Registry Data に全部の NBT を入れて送ってくるので、
			// 手元に公式の registry の中身（dimension_type など）を持たなくてよい。
			if _, err := readKnownPacks(d); err != nil {
				return fmt.Errorf("Clientbound Known Packs: %w", err)
			}
			if err := c.Send(data.ConfigurationServerboundSelectKnownPacks, func(e *protocol.Encoder) error {
				e.WriteVarInt(0)
				return nil
			}); err != nil {
				return fmt.Errorf("Serverbound Known Packs を送る: %w", err)
			}

		case data.ConfigurationClientboundRegistryData:
			if err := reg.readRegistryData(d); err != nil {
				return fmt.Errorf("Registry Data: %w", err)
			}

		case data.ConfigurationClientboundUpdateTags:
			if err := reg.ReadUpdateTags(d); err != nil {
				return fmt.Errorf("Update Tags (configuration): %w", err)
			}

		case data.ConfigurationClientboundUpdateEnabledFeatures:
			fs, err := protocol.ReadPrefixedArray(d, 1024, (*protocol.Decoder).ReadIdentifier)
			if err != nil {
				return fmt.Errorf("Feature Flags: %w", err)
			}
			reg.mu.Lock()
			reg.features = fs
			reg.mu.Unlock()

		case data.ConfigurationClientboundCookieRequest:
			if err := ReplyCookie(c, d, data.ConfigurationServerboundCookieResponse); err != nil {
				return err
			}

		case data.ConfigurationClientboundResourcePackPush:
			if err := ReplyResourcePack(c, d, data.ConfigurationServerboundResourcePack); err != nil {
				return err
			}

		case data.ConfigurationClientboundTransfer:
			return ErrTransfer

		default:
			// 知らないパケット（Plugin Message、Reset Chat、Server Links など）は読み飛ばす
		}
	}
}

// SendClientInfo は Client Information を送る。id は段階に合った ID。
func SendClientInfo(c *Conn, id int32, info ClientInfo) error {
	if info.Locale == "" {
		info.Locale = DefaultClientInfo.Locale
	}
	if info.ViewDistance <= 0 {
		info.ViewDistance = DefaultClientInfo.ViewDistance
	}
	if err := c.Send(id, func(e *protocol.Encoder) error {
		if err := e.WriteString(info.Locale, 16); err != nil {
			return err
		}
		e.WriteInt8(info.ViewDistance)
		e.WriteVarInt(0)   // チャット: 全部表示
		e.WriteBool(true)  // チャットの色
		e.WriteUByte(0x7f) // スキンの部位: 全部
		e.WriteVarInt(info.MainHand)
		e.WriteBool(false) // 文字のフィルタ
		e.WriteBool(true)  // サーバーの一覧に出てよい
		e.WriteVarInt(0)   // パーティクル: 全部（1.21.2 から）
		return nil
	}); err != nil {
		return fmt.Errorf("Client Information を送る: %w", err)
	}
	return nil
}

// ReplyKeepAlive は Keep Alive の ID をそのまま返す。
func ReplyKeepAlive(c *Conn, d *protocol.Decoder, id int32) error {
	v, err := d.ReadLong()
	if err != nil {
		return fmt.Errorf("Keep Alive: %w", err)
	}
	if err := c.Send(id, func(e *protocol.Encoder) error { e.WriteLong(v); return nil }); err != nil {
		return fmt.Errorf("Keep Alive を返す: %w", err)
	}
	return nil
}

// ReplyPing は Ping の ID を Pong で返す。
func ReplyPing(c *Conn, d *protocol.Decoder, id int32) error {
	v, err := d.ReadInt()
	if err != nil {
		return fmt.Errorf("Ping: %w", err)
	}
	if err := c.Send(id, func(e *protocol.Encoder) error { e.WriteInt(v); return nil }); err != nil {
		return fmt.Errorf("Pong を送る: %w", err)
	}
	return nil
}

// リソースパックの応答の結果。
const (
	resourcePackLoaded     = 0
	resourcePackAccepted   = 3
	resourcePackDownloaded = 4
)

// ReplyResourcePack は Add Resource Pack に「受け入れて、読み込んだ」と答える。
// bot は見た目を使わないので実際には読み込まない。断ると、必須のパックのサーバーでは切られるため。
func ReplyResourcePack(c *Conn, d *protocol.Decoder, id int32) error {
	uuid, err := d.ReadUUID()
	if err != nil {
		return fmt.Errorf("Add Resource Pack: %w", err)
	}
	for _, r := range []int32{resourcePackAccepted, resourcePackDownloaded, resourcePackLoaded} {
		if err := c.Send(id, func(e *protocol.Encoder) error {
			e.WriteUUID(uuid)
			e.WriteVarInt(r)
			return nil
		}); err != nil {
			return fmt.Errorf("Resource Pack Response を送る: %w", err)
		}
	}
	return nil
}

// knownPack は Known Packs の 1 つ。
type knownPack struct{ Namespace, ID, Version string }

func readKnownPacks(d *protocol.Decoder) ([]knownPack, error) {
	return protocol.ReadPrefixedArray(d, 64, func(d *protocol.Decoder) (knownPack, error) {
		var k knownPack
		var err error
		if k.Namespace, err = d.ReadString(32767); err != nil {
			return k, err
		}
		if k.ID, err = d.ReadString(32767); err != nil {
			return k, err
		}
		k.Version, err = d.ReadString(32767)
		return k, err
	})
}

// readRegistryData は Registry Data を読んで保管する（同じ registry が来たら置き換える）。
func (r *Registries) readRegistryData(d *protocol.Decoder) error {
	name, err := d.ReadIdentifier()
	if err != nil {
		return err
	}
	entries, err := protocol.ReadPrefixedArray(d, 1<<16, func(d *protocol.Decoder) (RegistryEntry, error) {
		var en RegistryEntry
		var err error
		if en.ID, err = d.ReadIdentifier(); err != nil {
			return en, err
		}
		v, ok, err := protocol.ReadPrefixedOptional(d, nbt.ReadNetwork)
		if err != nil {
			return en, err
		}
		if ok {
			en.Data = v
		}
		return en, nil
	})
	if err != nil {
		return fmt.Errorf("%s: %w", name, err)
	}
	r.mu.Lock()
	r.entries[name] = entries
	r.mu.Unlock()
	return nil
}

// ReadUpdateTags は Update Tags（設定の段階と Play の段階で同じ形）を読んで保管する。
// 届いた registry のタグは置き換える。
func (r *Registries) ReadUpdateTags(d *protocol.Decoder) error {
	type tagSet struct {
		registry string
		tags     map[string][]int32
	}
	sets, err := protocol.ReadPrefixedArray(d, 1024, func(d *protocol.Decoder) (tagSet, error) {
		var s tagSet
		var err error
		if s.registry, err = d.ReadIdentifier(); err != nil {
			return s, err
		}
		n, err := d.ReadLength(min(1<<16, d.Remaining()))
		if err != nil {
			return s, err
		}
		s.tags = make(map[string][]int32, n)
		for i := 0; i < n; i++ {
			name, err := d.ReadIdentifier()
			if err != nil {
				return s, err
			}
			ids, err := protocol.ReadPrefixedArray(d, 1<<20, (*protocol.Decoder).ReadVarInt)
			if err != nil {
				return s, fmt.Errorf("%s: %w", name, err)
			}
			s.tags[name] = ids
		}
		return s, nil
	})
	if err != nil {
		return err
	}
	r.mu.Lock()
	for _, s := range sets {
		r.tags[s.registry] = s.tags
	}
	r.mu.Unlock()
	return nil
}
