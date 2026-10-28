package mcdriver

// 地形（周りのブロックと明るさ）の受け取りと読み出し。
//
// 受け取るパケット: Login (play) と Respawn（ディメンションの高さを決める）、Chunk Data and Update Light、
// Unload Chunk、Block Update、Update Section Blocks、Update Light、Chunk Batch Start / Finished。

import (
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/Satoru314/go-mcdriver/data"
	"github.com/Satoru314/go-mcdriver/internal/chunk"
	"github.com/Satoru314/go-mcdriver/internal/packets"
	"github.com/Satoru314/go-mcdriver/internal/protocol"
)

// Dimension は今いるディメンション（世界）の情報。
type Dimension struct {
	// Name は名前（例 "minecraft:overworld"）。Login (play) の前は空
	Name string
	// MinY は一番下の y、Height は高さ。ブロックがあるのは MinY から MinY+Height-1 まで
	MinY, Height int
	// HasSkylight は空の明るさがあるか（ネザーとジ・エンドは false）
	HasSkylight bool
}

// worldState は地形の状態。鍵 mu で守る。
type worldState struct {
	mu  sync.RWMutex
	w   *chunk.World // Login (play) の前は nil
	dim Dimension
	// staleLight は明るさが古いかもしれないセクション（セクションの座標）。
	// サーバーはブロックの変化のあと明るさを送ってこないことがある（見えている範囲の端のチャンクにしか送らない）ので、
	// ブロックが変わったら印を付け、そのセクションの明るさが届いたら消す
	staleLight map[chunk.SectionPos]struct{}

	// チャンクを受け取る速さの見積もり（受信の goroutine だけが触るので鍵は要らない）
	batch chunkBatchRate
}

var worldKey = newFeature(func(*Client) *worldState {
	return &worldState{batch: newChunkBatchRate()}
})

func init() {
	onPlay(data.PlayClientboundLogin, (*Client).worldOnLogin)
	onPlay(data.PlayClientboundRespawn, (*Client).worldOnRespawn)
	onPlay(data.PlayClientboundLevelChunkWithLight, (*Client).worldOnChunk)
	onPlay(data.PlayClientboundForgetLevelChunk, (*Client).worldOnForget)
	onPlay(data.PlayClientboundBlockUpdate, (*Client).worldOnBlockUpdate)
	onPlay(data.PlayClientboundSectionBlocksUpdate, (*Client).worldOnSectionUpdate)
	onPlay(data.PlayClientboundLightUpdate, (*Client).worldOnLightUpdate)
	onPlay(data.PlayClientboundChunkBatchStart, (*Client).worldOnBatchStart)
	onPlay(data.PlayClientboundChunkBatchFinished, (*Client).worldOnBatchFinished)
}

// ---- 公開 API ----

// Block は位置 p のブロックの状態を返す。そのチャンクをまだ受け取っていない（または y が世界の外）なら ok は false。
func (cl *Client) Block(p BlockPos) (s data.BlockState, ok bool) {
	ws := worldKey.of(cl)
	ws.mu.RLock()
	id, err := ws.blockLocked(p)
	ws.mu.RUnlock()
	if err != nil {
		return data.BlockState{}, false
	}
	return data.StateByID(data.StateID(id))
}

// Light は位置 p の明るさ（ブロックの明るさと空の明るさ、どちらも 0〜15）を返す。
// y は世界の一番下の 16 下から一番上の 16 上まで読める（その外は ok=false）。
//
// 読み方は公式のクライアントと同じ:
//   - サーバーは、何も無いセクション（ブロックの無い高いところなど）の明るさを送ってこない。
//     そのようなセクションのブロックの明るさは 0。空の明るさは、上のセクションをたどって最初にデータのある
//     セクションの一番下の層の、同じ x, z の値。上に何も無ければ 15（空の明るさの無いディメンションでは 0）
//   - チャンクを受け取っていない場所は ok=false
//
// サーバーは、ブロックが変わったあとの明るさの変化を送ってこないことがある
// （公式のサーバーは見えている範囲の端のチャンクにしか送らず、クライアントが自分で計算する決まり。
// このドライバは明るさを計算しない）。そのため、ブロックが変わると、そのセクションの周り（3×3×3 のセクション）と、
// その下のセクション全部（空の明るさは影として下へいくらでも届く）に「古い」の印を付け、
// そこの明るさがまた届くまで ok=false を返す（古い値は返さない）。
func (cl *Client) Light(p BlockPos) (block, sky int, ok bool) {
	ws := worldKey.of(cl)
	ws.mu.RLock()
	defer ws.mu.RUnlock()
	if ws.w == nil {
		return 0, 0, false
	}
	if _, stale := ws.staleLight[chunk.SectionPos{X: p.X >> 4, Y: p.Y >> 4, Z: p.Z >> 4}]; stale {
		return 0, 0, false
	}
	c := ws.w.Column(int32(p.X>>4), int32(p.Z>>4))
	if c == nil {
		return 0, 0, false
	}
	// 明るさのセクションの番号（0 がワールドの一番下のセクションの 1 つ下）
	i := (p.Y-c.Format.MinY)>>4 + 1
	n := len(c.Light.Sky)
	if i < 0 || i >= n {
		return 0, 0, false
	}
	x, y, z := p.X&15, p.Y&15, p.Z&15
	if arr := c.Light.Block[i]; arr != nil {
		block = chunk.Nibble(arr, x, y, z)
	}
	if !ws.dim.HasSkylight {
		return block, 0, true
	}
	if arr := c.Light.Sky[i]; arr != nil {
		return block, chunk.Nibble(arr, x, y, z), true
	}
	// データの無いセクション: 上へたどって最初にデータのあるセクションの一番下の層
	for j := i + 1; j < n; j++ {
		if arr := c.Light.Sky[j]; arr != nil {
			return block, chunk.Nibble(arr, x, 0, z), true
		}
	}
	return block, 15, true
}

// Dimension は今いるディメンションの情報を返す。
func (cl *Client) Dimension() Dimension {
	ws := worldKey.of(cl)
	ws.mu.RLock()
	defer ws.mu.RUnlock()
	return ws.dim
}

// LoadedChunks は受け取って持っているチャンクの数を返す。
func (cl *Client) LoadedChunks() int {
	ws := worldKey.of(cl)
	ws.mu.RLock()
	defer ws.mu.RUnlock()
	if ws.w == nil {
		return 0
	}
	return ws.w.Len()
}

// MaxWorldViewBlocks は WorldView 1 つで写し取れるブロックの数の上限（1 ブロック 4 バイト）。
const MaxWorldViewBlocks = 1 << 25

// ErrWorldViewTooBig は WorldView の範囲が MaxWorldViewBlocks より大きいときのエラー。
var ErrWorldViewTooBig = errors.New("mcdriver: WorldView の範囲が大きすぎる")

// WorldView は地形のある範囲を写し取った、読み取り専用の写し。
// 鍵をかけずに読めるので、経路探索のように大量に読むときに使う。写したあとの変化は反映されない。
// 複数の goroutine から同時に読んでよい。
type WorldView struct {
	min, max   BlockPos
	sx, sy, sz int
	// states は状態 ID。番号は ((y-min.Y)*sz + (z-min.Z))*sx + (x-min.X)。受け取っていない場所は -1
	states []data.StateID
}

// WorldView は a から b まで（両端を含む）の直方体を写し取る。a と b はどの角でもよい。
// 範囲が MaxWorldViewBlocks を超えると ErrWorldViewTooBig。
func (cl *Client) WorldView(a, b BlockPos) (*WorldView, error) {
	lo := BlockPos{X: min(a.X, b.X), Y: min(a.Y, b.Y), Z: min(a.Z, b.Z)}
	hi := BlockPos{X: max(a.X, b.X), Y: max(a.Y, b.Y), Z: max(a.Z, b.Z)}
	sx, sy, sz := int64(hi.X-lo.X)+1, int64(hi.Y-lo.Y)+1, int64(hi.Z-lo.Z)+1
	if sx*sy*sz > MaxWorldViewBlocks {
		return nil, fmt.Errorf("%w: %d×%d×%d", ErrWorldViewTooBig, sx, sy, sz)
	}
	v := &WorldView{min: lo, max: hi, sx: int(sx), sy: int(sy), sz: int(sz)}
	v.states = make([]data.StateID, sx*sy*sz)
	for i := range v.states {
		v.states[i] = -1
	}
	ws := worldKey.of(cl)
	ws.mu.RLock()
	defer ws.mu.RUnlock()
	if ws.w != nil {
		v.fill(ws.w)
	}
	return v, nil
}

// fill は World から範囲の中のブロックを写す。
func (v *WorldView) fill(w *chunk.World) {
	f := w.Format
	y0, y1 := max(v.min.Y, f.MinY), min(v.max.Y, f.MinY+f.Height-1)
	for cx := v.min.X >> 4; cx <= v.max.X>>4; cx++ {
		for cz := v.min.Z >> 4; cz <= v.max.Z>>4; cz++ {
			col := w.Column(int32(cx), int32(cz))
			if col == nil {
				continue
			}
			// このチャンクと範囲の重なり（ワールドの座標）
			x0, x1 := max(v.min.X, cx<<4), min(v.max.X, cx<<4+15)
			z0, z1 := max(v.min.Z, cz<<4), min(v.max.Z, cz<<4+15)
			for y := y0; y <= y1; y++ {
				sec := &col.Sections[(y-f.MinY)>>4]
				ly := y & 15
				uv, uniform := sec.Blocks.Uniform()
				for z := z0; z <= z1; z++ {
					row := v.index(x0, y, z)
					if uniform {
						for i := range x1 - x0 + 1 {
							v.states[row+i] = data.StateID(uv)
						}
						continue
					}
					for x := x0; x <= x1; x++ {
						v.states[row+x-x0] = data.StateID(sec.Blocks.Get(chunk.BlockIndex(x&15, ly, z&15)))
					}
				}
			}
		}
	}
}

func (v *WorldView) index(x, y, z int) int {
	return ((y-v.min.Y)*v.sz+(z-v.min.Z))*v.sx + (x - v.min.X)
}

// Min と Max は写した範囲の両端（Max も含む）を返す。
func (v *WorldView) Min() BlockPos { return v.min }

// Max は写した範囲のもう一方の端（含む）を返す。
func (v *WorldView) Max() BlockPos { return v.max }

// Contains は p が写した範囲の中なら true を返す。
func (v *WorldView) Contains(p BlockPos) bool {
	return p.X >= v.min.X && p.X <= v.max.X && p.Y >= v.min.Y && p.Y <= v.max.Y && p.Z >= v.min.Z && p.Z <= v.max.Z
}

// StateID は p のブロックの状態 ID を返す。範囲の外、受け取っていないチャンク、世界の高さの外なら ok は false。
func (v *WorldView) StateID(p BlockPos) (id data.StateID, ok bool) {
	if !v.Contains(p) {
		return 0, false
	}
	id = v.states[v.index(p.X, p.Y, p.Z)]
	return id, id >= 0
}

// Block は p のブロックの状態を返す。ok の意味は StateID と同じ。
func (v *WorldView) Block(p BlockPos) (data.BlockState, bool) {
	id, ok := v.StateID(p)
	if !ok {
		return data.BlockState{}, false
	}
	return data.StateByID(id)
}

// ---- パケットの処理 ----

func (ws *worldState) blockLocked(p BlockPos) (int32, error) {
	if ws.w == nil {
		return 0, chunk.ErrNotLoaded
	}
	return ws.w.Block(p.X, p.Y, p.Z)
}

// worldReset はディメンションに入り直したときに、受け取ったチャンクを全部捨てて高さを決め直す。
func (cl *Client) worldReset(dimType int32, dimName string) error {
	dim, err := cl.dimensionOf(dimType, dimName)
	if err != nil {
		return err
	}
	f := chunk.Format{
		MinY:           dim.MinY,
		Height:         dim.Height,
		BlockStateBits: chunk.BitsFor(data.NumBlockStates),
		BiomeBits:      chunk.BitsFor(cl.biomeCount()),
		IsAir:          isAirState,
	}
	w, err := chunk.NewWorld(f)
	if err != nil {
		return fmt.Errorf("mcdriver: ディメンション %s: %w", dimName, err)
	}
	ws := worldKey.of(cl)
	ws.mu.Lock()
	ws.w, ws.dim = w, dim
	ws.staleLight = map[chunk.SectionPos]struct{}{}
	ws.mu.Unlock()
	return nil
}

// defaultBiomeCount は Registry Data にバイオームが無いときに使う数（1.21.4 の公式のバイオームの数）。
const defaultBiomeCount = 65

// biomeCount は受け取ったバイオームの registry の項目の数を返す（パレットなしのときのビット数を決める）。
func (cl *Client) biomeCount() int {
	if n := len(cl.reg.Entries("minecraft:worldgen/biome")); n > 0 {
		return n
	}
	return defaultBiomeCount
}

// dimensionOf は dimension_type の番号から高さを求める。Registry Data に中身が無いときは、
// 公式のディメンションなら名前から決める。
func (cl *Client) dimensionOf(dimType int32, dimName string) (Dimension, error) {
	d := Dimension{Name: dimName}
	entries := cl.reg.Entries("minecraft:dimension_type")
	if dimType >= 0 && int(dimType) < len(entries) {
		if m, ok := entries[dimType].Data.(map[string]any); ok {
			minY, ok1 := m["min_y"].(int32)
			height, ok2 := m["height"].(int32)
			if ok1 && ok2 {
				d.MinY, d.Height = int(minY), int(height)
				// has_skylight は TAG_Byte。無ければ公式の既定（true）
				d.HasSkylight = true
				if v, ok := m["has_skylight"].(int8); ok {
					d.HasSkylight = v != 0
				}
				return d, nil
			}
		}
		// 中身が無い（既知のデータパックとして省かれた）なら、公式のディメンションの値を使う
		switch entries[dimType].ID {
		case "minecraft:overworld", "minecraft:overworld_caves":
			d.MinY, d.Height, d.HasSkylight = -64, 384, true
			return d, nil
		case "minecraft:the_nether":
			d.MinY, d.Height, d.HasSkylight = 0, 256, false
			return d, nil
		case "minecraft:the_end":
			d.MinY, d.Height, d.HasSkylight = 0, 256, false
			return d, nil
		}
	}
	return d, fmt.Errorf("mcdriver: dimension_type %d（%s）の高さが分からない（registry に %d 個）", dimType, dimName, len(entries))
}

// airStates は空気（空気・洞窟の空気・奈落の空気）の状態 ID。
var airStates = func() map[int32]bool {
	m := map[int32]bool{}
	for _, n := range []string{"air", "cave_air", "void_air"} {
		if b, ok := data.BlockByName(n); ok {
			m[int32(b.DefaultState)] = true
		}
	}
	return m
}()

func isAirState(id int32) bool { return airStates[id] }

func (cl *Client) worldOnLogin(d *protocol.Decoder) error {
	lp, err := packets.ReadLoginPlay(d)
	if err != nil {
		return fmt.Errorf("mcdriver: Login (play): %w", err)
	}
	return cl.worldReset(lp.DimensionType, lp.DimensionName)
}

func (cl *Client) worldOnRespawn(d *protocol.Decoder) error {
	rp, err := packets.ReadRespawn(d)
	if err != nil {
		return fmt.Errorf("mcdriver: Respawn: %w", err)
	}
	ws := worldKey.of(cl)
	ws.mu.RLock()
	same := ws.w != nil && ws.dim.Name == rp.DimensionName
	ws.mu.RUnlock()
	if same {
		// 同じディメンションでのリスポーンでは、公式のクライアントもチャンクを捨てない
		return nil
	}
	return cl.worldReset(rp.DimensionType, rp.DimensionName)
}

// world は今の World を返す（Login (play) の前なら nil）。
func (ws *worldState) world() *chunk.World {
	ws.mu.RLock()
	defer ws.mu.RUnlock()
	return ws.w
}

func (cl *Client) worldOnChunk(d *protocol.Decoder) error {
	ws := worldKey.of(cl)
	w := ws.world()
	if w == nil {
		return nil
	}
	// 展開は鍵の外で行う（読む側を待たせない）。Format は作ったあと変わらない
	col, err := chunk.ReadChunkPacket(d, w.Format)
	if err != nil {
		return fmt.Errorf("mcdriver: Chunk Data and Update Light: %w", err)
	}
	ws.mu.Lock()
	// 展開しているあいだに入れ替わっていないか（受信の goroutine は 1 つなので起きないが念のため）
	if ws.w == w {
		replaced := w.Column(col.X, col.Z) != nil
		w.Put(col)
		// このチャンクの明るさは今届いたので新しい
		ws.clearStaleColumn(col.X, col.Z)
		if replaced {
			// 差し替え: 中身がどう変わったか分からないので、隣のチャンクの明るさを全部古いとする
			for dx := int32(-1); dx <= 1; dx++ {
				for dz := int32(-1); dz <= 1; dz++ {
					if dx != 0 || dz != 0 {
						ws.markStaleColumn(col.X+dx, col.Z+dz)
					}
				}
			}
		}
	}
	ws.mu.Unlock()
	return nil
}

func (cl *Client) worldOnForget(d *protocol.Decoder) error {
	// Unload Chunk は Z が先
	z, err := d.ReadInt()
	if err != nil {
		return fmt.Errorf("mcdriver: Unload Chunk: %w", err)
	}
	x, err := d.ReadInt()
	if err != nil {
		return fmt.Errorf("mcdriver: Unload Chunk: %w", err)
	}
	ws := worldKey.of(cl)
	ws.mu.Lock()
	if ws.w != nil {
		ws.w.Unload(x, z)
		ws.clearStaleColumn(x, z)
	}
	ws.mu.Unlock()
	return nil
}

// ignorable は、公式のクライアントが黙って無視するブロックの更新のエラー（読み込んでいないチャンク、高さの外）なら true を返す。
func ignorable(err error) bool {
	return errors.Is(err, chunk.ErrNotLoaded) || errors.Is(err, chunk.ErrOutOfRange)
}

func (cl *Client) worldOnBlockUpdate(d *protocol.Decoder) error {
	pos, id, err := chunk.ReadBlockUpdate(d)
	if err != nil {
		return fmt.Errorf("mcdriver: Block Update: %w", err)
	}
	ws := worldKey.of(cl)
	ws.mu.Lock()
	defer ws.mu.Unlock()
	if ws.w == nil {
		return nil
	}
	old, _ := ws.w.Block(int(pos.X), int(pos.Y), int(pos.Z)) // 出来事の知らせ用（読めなければ SetBlock も失敗する）
	if err := ws.w.SetBlock(int(pos.X), int(pos.Y), int(pos.Z), id); err != nil {
		if ignorable(err) {
			return nil
		}
		return fmt.Errorf("mcdriver: Block Update %v: %w", pos, err)
	}
	cl.emitBlockChange(BlockPos{X: int(pos.X), Y: int(pos.Y), Z: int(pos.Z)}, old, id)
	ws.markStaleAround(chunk.SectionPos{X: int(pos.X) >> 4, Y: int(pos.Y) >> 4, Z: int(pos.Z) >> 4})
	return nil
}

func (cl *Client) worldOnSectionUpdate(d *protocol.Decoder) error {
	sp, changes, err := chunk.ReadSectionUpdate(d)
	if err != nil {
		return fmt.Errorf("mcdriver: Update Section Blocks: %w", err)
	}
	ws := worldKey.of(cl)
	ws.mu.Lock()
	defer ws.mu.Unlock()
	if ws.w == nil {
		return nil
	}
	// 出来事の知らせ用に、変わる前の状態を読んでおく
	olds := make([]int32, len(changes))
	for i, ch := range changes {
		olds[i], _ = ws.w.Block(sp.X*16+ch.X, sp.Y*16+ch.Y, sp.Z*16+ch.Z)
	}
	if err := ws.w.ApplySectionUpdate(sp, changes); err != nil {
		if ignorable(err) {
			return nil
		}
		return fmt.Errorf("mcdriver: Update Section Blocks %+v: %w", sp, err)
	}
	for i, ch := range changes {
		cl.emitBlockChange(BlockPos{X: sp.X*16 + ch.X, Y: sp.Y*16 + ch.Y, Z: sp.Z*16 + ch.Z}, olds[i], ch.State)
	}
	ws.markStaleAround(sp)
	return nil
}

func (cl *Client) worldOnLightUpdate(d *protocol.Decoder) error {
	x, z, ld, err := chunk.ReadUpdateLight(d)
	if err != nil {
		return fmt.Errorf("mcdriver: Update Light: %w", err)
	}
	ws := worldKey.of(cl)
	ws.mu.Lock()
	defer ws.mu.Unlock()
	if ws.w == nil {
		return nil
	}
	c := ws.w.Column(x, z)
	if c == nil {
		// まだ受け取っていないチャンクの明るさは捨てる（チャンクのデータに明るさも入って届く）
		return nil
	}
	if err := c.ApplyLight(ld); err != nil {
		return fmt.Errorf("mcdriver: Update Light (%d, %d): %w", x, z, err)
	}
	// 明るさが届いたセクションの印を消す（明るさのセクション i は、ワールドの一番下のセクションの 1 つ下から数える）
	base := ws.w.Format.MinY>>4 - 1
	for i := range ws.w.Format.LightSections() {
		if ld.SkyMask.Get(i) || ld.BlockMask.Get(i) || ld.EmptySkyMask.Get(i) || ld.EmptyBlockMask.Get(i) {
			delete(ws.staleLight, chunk.SectionPos{X: int(x), Y: base + i, Z: int(z)})
		}
	}
	return nil
}

// markStaleAround は sp とその周り 26 個のセクションの明るさに「古い」の印を付ける。mu を持って呼ぶ。
// 光は最大 15 ブロック届くので、セクションの角のブロックが変わると斜めの隣のセクションまで変わりうる。
// さらに、その下のセクションにも全部印を付ける（上にブロックが増えたり減ったりすると、空の明るさの影は下へいくらでも届く）。
// 読み込んでいないチャンクのセクションには付けない。
func (ws *worldState) markStaleAround(sp chunk.SectionPos) {
	lo, _ := ws.lightSectionRange()
	for dx := -1; dx <= 1; dx++ {
		for dz := -1; dz <= 1; dz++ {
			// 読み込んでいないチャンクには付けない（読み込むときは新しい明るさが一緒に届く。
			// 付けると、そのチャンクが読み込まれないまま離れたときに印が残り続ける）
			if ws.w.Column(int32(sp.X+dx), int32(sp.Z+dz)) == nil {
				continue
			}
			for y := min(lo, sp.Y-1); y <= sp.Y+1; y++ {
				ws.staleLight[chunk.SectionPos{X: sp.X + dx, Y: y, Z: sp.Z + dz}] = struct{}{}
			}
		}
	}
}

// lightSectionRange は明るさのあるセクションの y の範囲（両端を含む）を返す。
func (ws *worldState) lightSectionRange() (lo, hi int) {
	f := ws.w.Format
	lo = f.MinY>>4 - 1
	return lo, lo + f.LightSections() - 1
}

// markStaleColumn はチャンク (x, z) の全部のセクションに印を付ける。読み込んでいなければ何もしない。mu を持って呼ぶ。
func (ws *worldState) markStaleColumn(x, z int32) {
	if ws.w.Column(x, z) == nil {
		return
	}
	lo, hi := ws.lightSectionRange()
	for y := lo; y <= hi; y++ {
		ws.staleLight[chunk.SectionPos{X: int(x), Y: y, Z: int(z)}] = struct{}{}
	}
}

// clearStaleColumn はチャンク (x, z) の印を全部消す。mu を持って呼ぶ。
func (ws *worldState) clearStaleColumn(x, z int32) {
	lo, hi := ws.lightSectionRange()
	for y := lo; y <= hi; y++ {
		delete(ws.staleLight, chunk.SectionPos{X: int(x), Y: y, Z: int(z)})
	}
}

func (cl *Client) worldOnBatchStart(*protocol.Decoder) error {
	worldKey.of(cl).batch.start(time.Now())
	return nil
}

func (cl *Client) worldOnBatchFinished(d *protocol.Decoder) error {
	n, err := d.ReadVarInt()
	if err != nil {
		return fmt.Errorf("mcdriver: Chunk Batch Finished: %w", err)
	}
	b := &worldKey.of(cl).batch
	b.finish(time.Now(), int(n))
	// 受け取ったことを伝えないと、サーバーは次のチャンクを送ってこない
	return cl.c.Send(data.PlayServerboundChunkBatchReceived, func(e *protocol.Encoder) error {
		e.WriteFloat(b.desiredChunksPerTick())
		return nil
	})
}

// chunkBatchRate は 1 ティックに受け取れるチャンクの数の見積もり（公式のクライアントの ChunkBatchSizeCalculator と同じ計算）。
// バッチの始めから終わりまでの時間をチャンクの数で割り、それまでの平均と混ぜる。
type chunkBatchRate struct {
	started       time.Time
	nanosPerChunk float64 // 1 チャンクにかかる時間の平均（ナノ秒）
	oldWeight     int     // 平均に使ったサンプルの重み（49 まで）
}

func newChunkBatchRate() chunkBatchRate {
	return chunkBatchRate{nanosPerChunk: 2_000_000, oldWeight: 1}
}

func (b *chunkBatchRate) start(now time.Time) { b.started = now }

func (b *chunkBatchRate) finish(now time.Time, n int) {
	if n <= 0 || b.started.IsZero() {
		return
	}
	per := float64(now.Sub(b.started).Nanoseconds()) / float64(n)
	// 1 回の値で大きく動かないように、今の平均の 1/3 から 3 倍に収める
	per = min(max(per, b.nanosPerChunk/3), b.nanosPerChunk*3)
	b.nanosPerChunk = (b.nanosPerChunk*float64(b.oldWeight) + per) / float64(b.oldWeight+1)
	b.oldWeight = min(49, b.oldWeight+1)
	b.started = time.Time{}
}

// desiredChunksPerTick は 1 ティック（のうち 7 ミリ秒）で受け取れるチャンクの数を返す。
func (b *chunkBatchRate) desiredChunksPerTick() float32 {
	return float32(7_000_000 / b.nanosPerChunk)
}
