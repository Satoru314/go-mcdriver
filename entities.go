package mcdriver

// 周りの生き物と物（エンティティ）の受け取りと読み出し。
//
// 受け取るパケット: Spawn Entity、Remove Entities、Update Entity Position / Position and Rotation / Rotation、
// Entity Position Sync、Teleport Entity、Set Entity Velocity、Set Head Rotation、Set Entity Metadata、
// Player Info Update / Remove（プレイヤーの名前）、Login (play) と Respawn（世界が変わったら全部消す）。
//
// 自分自身（bot）はサーバーから Spawn Entity が来ないので、ここには入らない（自分の体は Self で読む）。

import (
	"cmp"
	"fmt"
	"slices"
	"sync"

	"github.com/Satoru314/go-mcdriver/data"
	"github.com/Satoru314/go-mcdriver/internal/packets"
	"github.com/Satoru314/go-mcdriver/internal/protocol"
	"gonum.org/v1/gonum/spatial/r3"
)

// UUID はエンティティやプレイヤーの UUID。
type UUID [16]byte

// String は UUID を「xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx」の形にする。
func (u UUID) String() string { return protocol.UUID(u).String() }

// EntityItem は落ちているアイテムの中身。部品情報（耐久値、エンチャントなど）は持たない。
type EntityItem struct {
	ID    data.ItemID
	Name  string // "minecraft:" を外した名前（例 "diamond"）。知らない番号なら空
	Count int    // 個数。中身をまだ受け取っていなければ 0
}

// Entity はエンティティ 1 つの、呼んだ時点の写し。
type Entity struct {
	// ID はエンティティ ID（このサーバーの接続の中でだけ通じる番号）
	ID   int32
	UUID UUID
	// Type は種類。大きさ（Width, Height）もここにある。知らない番号なら Name は空
	Type data.EntityType
	// Pos は足元の中心の位置
	Pos r3.Vec
	// Vel は速度（ブロック/ティック）。サーバーが送ってきた値で、間の動きは計算しない
	Vel r3.Vec
	// Yaw, Pitch は体の向き、HeadYaw は頭の横の向き（どれも度）。1 バイトの角度で届いたものは -180 以上 180 未満にする。
	// Entity Position Sync と Teleport Entity で届いたものは、サーバーの値（float）そのまま
	Yaw, Pitch, HeadYaw float32
	OnGround            bool
	// Box は当たり判定の箱（Pos と Type の大きさから計算。子供や姿勢による違いは入っていない）
	Box r3.Box
	// Item は落ちているアイテム（Type が item）の中身。ほかの種類では空
	Item EntityItem
	// Name はプレイヤーの名前（Player Info Update から）。プレイヤーでなければ空
	Name string
	// Health は体力。HasHealth が false なら（生き物でない、またはまだ受け取っていない）意味を持たない
	Health    float32
	HasHealth bool
	// OnFire は燃えているか
	OnFire bool
}

// entityState はエンティティの状態。鍵 mu で守る。
type entityState struct {
	mu      sync.RWMutex
	ents    map[int32]*Entity
	players map[UUID]string // プレイヤーの一覧（UUID → 名前）
	dim     string          // 今いるディメンションの名前（Respawn で変わったかを見る）
	// metaErrors は最後まで読めなかった Set Entity Metadata の数（読めたところまでは使う）
	metaErrors int
	// lastMetaErr はその最後のエラー
	lastMetaErr error
}

var entityKey = newFeature(func(*Client) *entityState {
	return &entityState{ents: map[int32]*Entity{}, players: map[UUID]string{}}
})

func init() {
	onPlay(data.PlayClientboundLogin, (*Client).entOnLogin)
	onPlay(data.PlayClientboundRespawn, (*Client).entOnRespawn)
	onPlay(data.PlayClientboundAddEntity, (*Client).entOnSpawn)
	onPlay(data.PlayClientboundRemoveEntities, (*Client).entOnRemove)
	onPlay(data.PlayClientboundMoveEntityPos, func(cl *Client, d *protocol.Decoder) error { return cl.entOnMove(d, true, false) })
	onPlay(data.PlayClientboundMoveEntityPosRot, func(cl *Client, d *protocol.Decoder) error { return cl.entOnMove(d, true, true) })
	onPlay(data.PlayClientboundMoveEntityRot, func(cl *Client, d *protocol.Decoder) error { return cl.entOnMove(d, false, true) })
	onPlay(data.PlayClientboundEntityPositionSync, func(cl *Client, d *protocol.Decoder) error { return cl.entOnSync(d, false) })
	onPlay(data.PlayClientboundTeleportEntity, func(cl *Client, d *protocol.Decoder) error { return cl.entOnSync(d, true) })
	onPlay(data.PlayClientboundSetEntityMotion, (*Client).entOnMotion)
	onPlay(data.PlayClientboundRotateHead, (*Client).entOnHead)
	onPlay(data.PlayClientboundSetEntityData, (*Client).entOnMetadata)
	onPlay(data.PlayClientboundPlayerInfoUpdate, (*Client).entOnPlayerInfo)
	onPlay(data.PlayClientboundPlayerInfoRemove, (*Client).entOnPlayerRemove)
}

// ---- 公開 API ----

// Entities は見えているエンティティ全部の写しを、ID の順に返す。
func (cl *Client) Entities() []Entity {
	es := entityKey.of(cl)
	es.mu.RLock()
	defer es.mu.RUnlock()
	out := make([]Entity, 0, len(es.ents))
	for _, e := range es.ents {
		out = append(out, es.snapshotLocked(e))
	}
	slices.SortFunc(out, func(a, b Entity) int { return cmp.Compare(a.ID, b.ID) })
	return out
}

// Entity は ID が id のエンティティの写しを返す。見えていなければ ok は false。
func (cl *Client) Entity(id int32) (e Entity, ok bool) {
	es := entityKey.of(cl)
	es.mu.RLock()
	defer es.mu.RUnlock()
	p, ok := es.ents[id]
	if !ok {
		return Entity{}, false
	}
	return es.snapshotLocked(p), true
}

// PlayerNames はプレイヤーの一覧（タブの一覧。遠くて見えないプレイヤーも入る）の UUID → 名前の写しを返す。
func (cl *Client) PlayerNames() map[UUID]string {
	es := entityKey.of(cl)
	es.mu.RLock()
	defer es.mu.RUnlock()
	m := make(map[UUID]string, len(es.players))
	for k, v := range es.players {
		m[k] = v
	}
	return m
}

// snapshotLocked は e の写しに、名前と当たり判定の箱を入れて返す。
func (es *entityState) snapshotLocked(e *Entity) Entity {
	s := *e
	if s.Type.Name == "player" {
		s.Name = es.players[s.UUID]
	}
	hw, h := float64(s.Type.Width)/2, float64(s.Type.Height)
	s.Box = r3.Box{
		Min: r3.Vec{X: s.Pos.X - hw, Y: s.Pos.Y, Z: s.Pos.Z - hw},
		Max: r3.Vec{X: s.Pos.X + hw, Y: s.Pos.Y + h, Z: s.Pos.Z + hw},
	}
	return s
}

// ---- 受け取ったパケットの処理 ----

// velocityScale は Spawn Entity と Set Entity Velocity の速度の単位（1/8000 ブロック/ティック）。
const velocityScale = 8000

// moveScale は Update Entity Position の位置の差分の単位（1/4096 ブロック）。
const moveScale = 4096

// metaIndex... はメタデータの番号（1.21.4）。
const (
	metaIndexFlags  = 0 // Entity: Byte（0x01 が燃えている）
	metaIndexItem   = 8 // ItemEntity: Slot
	metaIndexHealth = 9 // LivingEntity: Float
)

func (cl *Client) entOnLogin(d *protocol.Decoder) error {
	lp, err := packets.ReadLoginPlay(d)
	if err != nil {
		return fmt.Errorf("mcdriver: Login (play): %w", err)
	}
	es := entityKey.of(cl)
	es.mu.Lock()
	defer es.mu.Unlock()
	// Login (play) は最初と、設定の段階から戻ったとき。どちらも世界が新しくなるので、全部消す
	clear(es.ents)
	clear(es.players)
	es.dim = lp.DimensionName
	return nil
}

func (cl *Client) entOnRespawn(d *protocol.Decoder) error {
	rp, err := packets.ReadRespawn(d)
	if err != nil {
		return fmt.Errorf("mcdriver: Respawn: %w", err)
	}
	es := entityKey.of(cl)
	es.mu.Lock()
	defer es.mu.Unlock()
	if rp.DimensionName != es.dim {
		// 別のディメンションに移ったら、前の世界のエンティティは全部消す（プレイヤーの一覧は残る）
		clear(es.ents)
		es.dim = rp.DimensionName
	}
	return nil
}

func (cl *Client) entOnSpawn(d *protocol.Decoder) error {
	p, err := packets.ReadSpawnEntity(d)
	if err != nil {
		return fmt.Errorf("mcdriver: Spawn Entity: %w", err)
	}
	e := &Entity{
		ID:      p.EntityID,
		UUID:    UUID(p.UUID),
		Type:    data.EntityType{ID: data.EntityTypeID(p.Type)},
		Pos:     r3.Vec{X: p.X, Y: p.Y, Z: p.Z},
		Vel:     r3.Vec{X: float64(p.VelX) / velocityScale, Y: float64(p.VelY) / velocityScale, Z: float64(p.VelZ) / velocityScale},
		Yaw:     angleDegrees(p.Yaw),
		Pitch:   angleDegrees(p.Pitch),
		HeadYaw: angleDegrees(p.HeadYaw),
	}
	if t, ok := data.EntityTypeByID(data.EntityTypeID(p.Type)); ok {
		e.Type = *t
	}
	es := entityKey.of(cl)
	es.mu.Lock()
	es.ents[p.EntityID] = e
	es.mu.Unlock()
	return nil
}

func (cl *Client) entOnRemove(d *protocol.Decoder) error {
	ids, err := packets.ReadRemoveEntities(d)
	if err != nil {
		return fmt.Errorf("mcdriver: Remove Entities: %w", err)
	}
	es := entityKey.of(cl)
	es.mu.Lock()
	for _, id := range ids {
		delete(es.ents, id)
	}
	es.mu.Unlock()
	return nil
}

func (cl *Client) entOnMove(d *protocol.Decoder, pos, rot bool) error {
	p, err := packets.ReadMoveEntity(d, pos, rot)
	if err != nil {
		return fmt.Errorf("mcdriver: Update Entity Position/Rotation: %w", err)
	}
	es := entityKey.of(cl)
	es.mu.Lock()
	defer es.mu.Unlock()
	e, ok := es.ents[p.EntityID]
	if !ok {
		return nil // 知らないエンティティ（自分自身や、消えた直後のもの）は無視する
	}
	if pos {
		// 差分は前に受け取った位置からのもの（固定小数点、1/4096 ブロック）
		e.Pos.X += float64(p.DX) / moveScale
		e.Pos.Y += float64(p.DY) / moveScale
		e.Pos.Z += float64(p.DZ) / moveScale
	}
	if rot {
		e.Yaw = angleDegrees(p.Yaw)
		e.Pitch = angleDegrees(p.Pitch)
	}
	e.OnGround = p.OnGround
	return nil
}

func (cl *Client) entOnSync(d *protocol.Decoder, teleport bool) error {
	p, err := packets.ReadEntityPositionSync(d, teleport)
	if err != nil {
		if teleport {
			return fmt.Errorf("mcdriver: Teleport Entity: %w", err)
		}
		return fmt.Errorf("mcdriver: Entity Position Sync: %w", err)
	}
	es := entityKey.of(cl)
	es.mu.Lock()
	defer es.mu.Unlock()
	e, ok := es.ents[p.EntityID]
	if !ok {
		return nil
	}
	// Entity Position Sync は Flags が 0（全部が絶対の値）。Teleport Entity は Flags のビットが立った成分だけ今の値に足す
	// （速度の向きを回す RotateVelocity は扱わない）
	rel := func(bit int32, cur, v float64) float64 {
		if p.Flags&bit != 0 {
			return cur + v
		}
		return v
	}
	e.Pos = r3.Vec{X: rel(packets.RelX, e.Pos.X, p.X), Y: rel(packets.RelY, e.Pos.Y, p.Y), Z: rel(packets.RelZ, e.Pos.Z, p.Z)}
	e.Vel = r3.Vec{X: rel(packets.RelVelX, e.Vel.X, p.VelX), Y: rel(packets.RelVelY, e.Vel.Y, p.VelY), Z: rel(packets.RelVelZ, e.Vel.Z, p.VelZ)}
	e.Yaw = float32(rel(packets.RelYaw, float64(e.Yaw), float64(p.Yaw)))
	e.Pitch = float32(rel(packets.RelPitch, float64(e.Pitch), float64(p.Pitch)))
	e.OnGround = p.OnGround
	return nil
}

func (cl *Client) entOnMotion(d *protocol.Decoder) error {
	p, err := packets.ReadSetEntityMotion(d)
	if err != nil {
		return fmt.Errorf("mcdriver: Set Entity Velocity: %w", err)
	}
	es := entityKey.of(cl)
	es.mu.Lock()
	defer es.mu.Unlock()
	if e, ok := es.ents[p.EntityID]; ok {
		e.Vel = r3.Vec{X: float64(p.VelX) / velocityScale, Y: float64(p.VelY) / velocityScale, Z: float64(p.VelZ) / velocityScale}
	}
	return nil
}

func (cl *Client) entOnHead(d *protocol.Decoder) error {
	p, err := packets.ReadRotateHead(d)
	if err != nil {
		return fmt.Errorf("mcdriver: Set Head Rotation: %w", err)
	}
	es := entityKey.of(cl)
	es.mu.Lock()
	defer es.mu.Unlock()
	if e, ok := es.ents[p.EntityID]; ok {
		e.HeadYaw = angleDegrees(p.HeadYaw)
	}
	return nil
}

func (cl *Client) entOnMetadata(d *protocol.Decoder) error {
	p, err := packets.ReadSetEntityMetadata(d)
	es := entityKey.of(cl)
	es.mu.Lock()
	defer es.mu.Unlock()
	if err != nil {
		// 読めない型があっても、そのパケットの残りを捨てるだけで接続は続ける（読めたところまでは使う）
		es.metaErrors++
		es.lastMetaErr = fmt.Errorf("mcdriver: Set Entity Metadata（エンティティ %d）: %w", p.EntityID, err)
	}
	e, ok := es.ents[p.EntityID]
	if !ok {
		return nil
	}
	for _, m := range p.Entries {
		switch {
		case m.Index == metaIndexFlags && m.Type == packets.MetaByte:
			e.OnFire = m.Value.(uint8)&0x01 != 0
		case m.Index == metaIndexItem && m.Type == packets.MetaSlot && e.Type.Name == "item":
			s := m.Value.(packets.Slot)
			e.Item = EntityItem{ID: s.Item, Count: int(s.Count)}
			if it, ok := data.ItemByID(s.Item); ok {
				e.Item.Name = it.Name
			}
		case m.Index == metaIndexHealth && m.Type == packets.MetaFloat && e.Type.Name != "interaction":
			// 番号 9 が Float なのは生き物（LivingEntity の体力）と interaction（高さ）だけ
			e.Health = m.Value.(float32)
			e.HasHealth = true
		}
	}
	return nil
}

func (cl *Client) entOnPlayerInfo(d *protocol.Decoder) error {
	p, err := packets.ReadPlayerInfoUpdate(d)
	if err != nil {
		return fmt.Errorf("mcdriver: Player Info Update: %w", err)
	}
	if p.Actions&packets.PlayerInfoAddPlayer == 0 {
		return nil
	}
	es := entityKey.of(cl)
	es.mu.Lock()
	defer es.mu.Unlock()
	for _, en := range p.Entries {
		es.players[UUID(en.UUID)] = en.Name
	}
	return nil
}

func (cl *Client) entOnPlayerRemove(d *protocol.Decoder) error {
	ids, err := packets.ReadPlayerInfoRemove(d)
	if err != nil {
		return fmt.Errorf("mcdriver: Player Info Remove: %w", err)
	}
	es := entityKey.of(cl)
	es.mu.Lock()
	defer es.mu.Unlock()
	for _, u := range ids {
		delete(es.players, UUID(u))
	}
	return nil
}

// angleDegrees は Angle（1 回転の 256 分の 1）を -180 以上 180 未満の度にする
// （Angle.Degrees は 0〜360 なので、上を向いた縦の向きが 180 より大きくなってしまう）。
func angleDegrees(a protocol.Angle) float32 {
	return float32(int8(a)) * 360 / 256
}
