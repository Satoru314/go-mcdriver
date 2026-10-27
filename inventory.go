package mcdriver

// 持ち物と、開いている容器（チェスト、かまど、作業台など）。
//
// 受け取るパケット: Open Screen、Open Horse Screen、Set Container Content、Set Container Slot、
// Set Container Property、Close Container、Set Cursor Item、Set Held Item、Set Player Inventory Slot、Respawn。
// 送るパケット: Click Container、Set Held Item（serverbound）、Close Container（serverbound）。
//
// スロットの部品情報（耐久値、エンチャントなど）は使う側に出さない。読み飛ばして、
// サーバーへ送り返すときのためにそのままの形で中に持つ（packets.Slot.Components）。
// 形を知らない部品が来たり、読めないスロットがあったりしたら、そのパケットだけを捨てる（切断しない）。
// その窓は Stale になり、次の Set Container Content で新しくなるまで古いままになる。

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/Satoru314/go-mcdriver/data"
	"github.com/Satoru314/go-mcdriver/internal/conn"
	"github.com/Satoru314/go-mcdriver/internal/packets"
	"github.com/Satoru314/go-mcdriver/internal/protocol"
)

// プレイヤーの窓（持ち物の窓、番号 0）のスロットの番号。
const (
	SlotCraftResult = 0  // 2×2 のクラフトの結果
	SlotCraftGrid   = 1  // 2×2 のクラフトの材料（1〜4）
	SlotHead        = 5  // 頭の防具
	SlotChest       = 6  // 胴の防具
	SlotLegs        = 7  // 脚の防具
	SlotFeet        = 8  // 足の防具
	SlotMain        = 9  // 持ち物の中（9〜35）
	SlotHotbar      = 36 // ホットバー（36〜44）。ホットバーの i 番目は SlotHotbar+i
	SlotOffhand     = 45 // 左手
	InventorySlots  = 46 // プレイヤーの窓のスロットの数
)

// SlotOutside はクリックで「窓の外」を指す番号（持っているものを捨てる）。
const SlotOutside = -999

// Item はスロット 1 つの中身。アイテムの種類と個数だけを持つ（部品情報は出さない）。
type Item struct {
	// Item はアイテムの種類。空のスロットは nil。中身を書き換えないこと
	Item *data.Item
	// Count は個数。空のスロットは 0
	Count int
}

// Empty は空のスロットかを返す。
func (it Item) Empty() bool { return it.Count <= 0 }

// Name はアイテムの名前（"minecraft:" なし）。空なら ""。
func (it Item) Name() string {
	if it.Empty() || it.Item == nil {
		return ""
	}
	return it.Item.Name
}

func (it Item) String() string {
	if it.Empty() {
		return "空"
	}
	return fmt.Sprintf("%s×%d", it.Name(), it.Count)
}

// Inventory は自分の持ち物の写し。
type Inventory struct {
	// Slots はプレイヤーの窓の番号の付け方（SlotHotbar など）で並べた全スロット
	Slots [InventorySlots]Item
	// Selected は選んでいるホットバーの番号（0〜8）
	Selected int
	// Cursor はカーソルが持っているもの（クリックで拾ったもの）
	Cursor Item
	// Stale は、読めなかったパケットがあり、次の Set Container Content まで古いかもしれないことを表す
	Stale bool
}

// Hotbar はホットバーの i 番目（0〜8）を返す。
func (inv Inventory) Hotbar(i int) Item {
	if i < 0 || i > 8 {
		return Item{}
	}
	return inv.Slots[SlotHotbar+i]
}

// Held は手に持っている（選んでいるホットバーの）ものを返す。
func (inv Inventory) Held() Item { return inv.Hotbar(inv.Selected) }

// Count はアイテム name（"minecraft:" はあってもなくてもよい）の個数の合計を返す（カーソルは含めない）。
func (inv Inventory) Count(name string) int {
	it, ok := data.ItemByName(name)
	if !ok {
		return 0
	}
	n := 0
	for _, s := range inv.Slots {
		if !s.Empty() && s.Item == it {
			n += s.Count
		}
	}
	return n
}

// Find はアイテム name が入っている最初のスロットの番号を返す。無ければ -1。
func (inv Inventory) Find(name string) int {
	it, ok := data.ItemByName(name)
	if !ok {
		return -1
	}
	for i, s := range inv.Slots {
		if !s.Empty() && s.Item == it {
			return i
		}
	}
	return -1
}

// Container は開いている容器の窓の写し。
type Container struct {
	// ID は窓の番号（サーバーが決める。1 以上）
	ID int
	// Type は窓の種類（minecraft:menu の名前、"minecraft:" なし）。例 "generic_9x3"（チェスト）、"furnace"、"crafting"。
	// 馬の窓は "horse"
	Type string
	// Title は題名（色などを除いた文字）
	Title string
	// Slots は窓の全スロット（クリックの番号と同じ）。容器自身のスロットのあとに、持ち物の中（27）と
	// ホットバー（9）が続く。Set Container Content を受け取る前は空
	Slots []Item
	// Size は容器自身のスロットの数（Slots[:Size]）。持ち物の部分の始まりの番号でもある。
	// Set Container Content の前は窓の種類から決めた数（Slots はまだ空）
	Size int
	// Properties は Set Container Property の値（かまどの燃焼の残り・進み具合など）。番号 → 値
	Properties map[int]int
	// Stale は、読めなかったパケットがあり、次の Set Container Content まで古いかもしれないことを表す
	Stale bool
}

// ClickMode はクリックの種類（Click Container の Mode）。
type ClickMode int32

const (
	ClickPickup     ClickMode = ClickMode(packets.ClickPickup)     // 左右クリック（button 0 左, 1 右）
	ClickQuickMove  ClickMode = ClickMode(packets.ClickQuickMove)  // シフトクリック（button 0 か 1）
	ClickSwap       ClickMode = ClickMode(packets.ClickSwap)       // 数字キー（button 0〜8）・左手との入れ替え（button 40）
	ClickClone      ClickMode = ClickMode(packets.ClickClone)      // 中クリック（クリエイティブのみ）
	ClickThrow      ClickMode = ClickMode(packets.ClickThrow)      // 捨てる（button 0 で 1 個、1 で全部）
	ClickQuickCraft ClickMode = ClickMode(packets.ClickQuickCraft) // ドラッグ
	ClickPickupAll  ClickMode = ClickMode(packets.ClickPickupAll)  // ダブルクリック
)

var (
	// ErrClickRejected は、クリックのあとのサーバーの内容が、予測した結果と違ったときのエラー。
	// 持ち物と容器はサーバーの内容に直してある。
	ErrClickRejected = errors.New("mcdriver: クリックがサーバーに受け入れられなかった（結果が予測と違う）")
	// ErrClickNoAnswer は、クリックのあとサーバーから内容が届かなかったときのエラー。
	ErrClickNoAnswer = errors.New("mcdriver: クリックのあとサーバーから内容が届かない")
	// ErrNotPlaying は、Play の段階でない（設定の段階に戻っている）ので送れないときのエラー。
	ErrNotPlaying = errors.New("mcdriver: Play の段階でないので送れない")
)

// clickAnswerTimeout はクリックのあと内容を待つ長さの上限（ctx がもっと短ければそちら）。
const clickAnswerTimeout = 5 * time.Second

// ---- 状態 ----

// windowState は開いている容器の窓の状態。
type windowState struct {
	id          int32
	typeName    string
	title       string
	slots       []packets.Slot
	playerStart int // 持ち物の部分の始まり（-1 なら持ち物の部分が無い）
	stateID     int32
	props       map[int]int
	stale       bool
}

// invState は持ち物の状態。鍵 mu で守る。
type invState struct {
	mu       sync.Mutex
	slots    [InventorySlots]packets.Slot // プレイヤーの窓
	stateID  int32                        // プレイヤーの窓の state ID
	stale    bool
	selected int
	carried  packets.Slot
	win      *windowState // 開いている容器。無ければ nil

	// Set Container Content の到着の知らせ（ClickSlot が待つ）
	contentSeq map[int32]uint64 // 窓の番号 → 届いた数（読めなかったものも数える）
	contentBad map[int32]bool   // 窓の番号 → 最後に届いたものが読めなかったか
	notify     chan struct{}    // 届くたびに閉じて作り直す

	// 読めなかったパケットの記録
	readErrors int
	lastErr    error

	clickMu sync.Mutex // クリックを 1 つずつにする
}

var invKey = newFeature(func(*Client) *invState {
	return &invState{
		contentSeq: map[int32]uint64{},
		contentBad: map[int32]bool{},
		notify:     make(chan struct{}),
	}
})

func init() {
	onPlay(data.PlayClientboundOpenScreen, (*Client).invOnOpenScreen)
	onPlay(data.PlayClientboundHorseScreenOpen, (*Client).invOnHorseScreen)
	onPlay(data.PlayClientboundContainerSetContent, (*Client).invOnContent)
	onPlay(data.PlayClientboundContainerSetSlot, (*Client).invOnSlot)
	onPlay(data.PlayClientboundContainerSetData, (*Client).invOnProperty)
	onPlay(data.PlayClientboundContainerClose, (*Client).invOnClose)
	onPlay(data.PlayClientboundSetCursorItem, (*Client).invOnCursor)
	onPlay(data.PlayClientboundSetHeldSlot, (*Client).invOnHeldSlot)
	onPlay(data.PlayClientboundSetPlayerInventory, (*Client).invOnPlayerInventory)
	onPlay(data.PlayClientboundRespawn, (*Client).invOnRespawn)
}

// ---- 公開 API ----

// Inventory は自分の持ち物の写しを返す。
func (cl *Client) Inventory() Inventory {
	st := invKey.of(cl)
	st.mu.Lock()
	defer st.mu.Unlock()
	var inv Inventory
	for i, s := range st.slots {
		inv.Slots[i] = toItem(s)
	}
	inv.Selected = st.selected
	inv.Cursor = toItem(st.carried)
	inv.Stale = st.stale
	return inv
}

// Container は開いている容器の窓の写しを返す。開いていなければ ok は false。
func (cl *Client) Container() (c Container, ok bool) {
	st := invKey.of(cl)
	st.mu.Lock()
	defer st.mu.Unlock()
	w := st.win
	if w == nil {
		return Container{}, false
	}
	c = Container{ID: int(w.id), Type: w.typeName, Title: w.title, Stale: w.stale, Properties: map[int]int{}}
	c.Slots = make([]Item, len(w.slots))
	for i, s := range w.slots {
		c.Slots[i] = toItem(s)
	}
	c.Size = len(w.slots)
	if ps := w.start(); ps >= 0 {
		c.Size = ps
	}
	for k, v := range w.props {
		c.Properties[k] = v
	}
	return c, true
}

// InventoryReadErrors は、読めずに捨てた持ち物のパケットの数と、最後のエラーを返す。
func (cl *Client) InventoryReadErrors() (n int, last error) {
	st := invKey.of(cl)
	st.mu.Lock()
	defer st.mu.Unlock()
	return st.readErrors, st.lastErr
}

// SelectHotbar はホットバーの i 番目（0〜8）を選ぶ（手に持つ）。
func (cl *Client) SelectHotbar(i int) error {
	if i < 0 || i > 8 {
		return fmt.Errorf("mcdriver: ホットバーの番号は 0〜8: %d", i)
	}
	st := invKey.of(cl)
	st.mu.Lock()
	defer st.mu.Unlock()
	if err := cl.invSend(data.PlayServerboundSetCarriedItem, func(e *protocol.Encoder) error {
		e.WriteShort(int16(i))
		return nil
	}); err != nil {
		return err
	}
	st.selected = i
	return nil
}

// CloseContainer は開いている容器を閉じる（開いていなければ持ち物の窓を閉じる。
// どちらでも、カーソルが持っているものやクラフトの材料はサーバーが持ち物に戻す）。
func (cl *Client) CloseContainer() error {
	st := invKey.of(cl)
	st.mu.Lock()
	defer st.mu.Unlock()
	var id int32
	if st.win != nil {
		id = st.win.id
	}
	if err := cl.invSend(data.PlayServerboundContainerClose, func(e *protocol.Encoder) error {
		e.WriteVarInt(id)
		return nil
	}); err != nil {
		return err
	}
	st.win = nil
	return nil
}

// Drop はスロット slot（今の窓の番号）のものを捨てる。all なら全部、そうでなければ 1 個。
// ClickSlot(ctx, slot, 0 か 1, ClickThrow) と同じ。カーソルが何か持っているときは何も起きない。
func (cl *Client) Drop(ctx context.Context, slot int, all bool) error {
	button := 0
	if all {
		button = 1
	}
	return cl.ClickSlot(ctx, slot, button, ClickThrow)
}

// ClickSlot は今の窓（容器が開いていればその窓、無ければ持ち物の窓）のスロット slot をクリックする
// （Click Container）。slot は窓の番号（SlotOutside で窓の外）。
//
// 送るときは、最後に受け取った state ID と、変わるスロットとカーソルの予測を付け、手元の持ち物も予測どおりに変える。
// そのあとサーバーに今の窓の全体を送り直してもらい（下の「確かめ方」）、届いた内容が予測と同じなら nil、
// 違えば ErrClickRejected を返す（持ち物と容器はサーバーの内容に直っている）。
//
// 予測するのは ClickPickup（左右クリック）、ClickSwap（数字キー）、ClickThrow（捨てる）。
// ほかの種類（シフトクリックやドラッグなど）と、結果のスロット（クラフトの結果、かまどの出口など）や
// バンドルのクリックは予測せず、サーバーの内容が届いたら nil を返す（結果は Inventory や Container で見る）。
//
// 確かめ方: クリックのあとに、窓の外（-1）を何もせずにクリックするパケットを、わざと合わない state ID（-1）で送る。
// サーバーは state ID が合わないと窓の全体（Set Container Content）を送り直すので、それがクリックの結果になる。
func (cl *Client) ClickSlot(ctx context.Context, slot, button int, mode ClickMode) error {
	st := invKey.of(cl)
	st.clickMu.Lock()
	defer st.clickMu.Unlock()

	st.mu.Lock()
	winID, slots := st.windowLocked()
	if slot != SlotOutside && slot != -1 && (slot < 0 || slot >= len(slots)) {
		st.mu.Unlock()
		return fmt.Errorf("mcdriver: スロットの番号 %d が窓（%d スロット）の外", slot, len(slots))
	}
	if button < -128 || button > 127 {
		st.mu.Unlock()
		return fmt.Errorf("mcdriver: button %d が範囲の外", button)
	}
	pr := st.predictLocked(slot, button, mode)
	click := packets.ContainerClick{
		WindowID: winID,
		StateID:  st.stateIDLocked(),
		Slot:     int16(slot),
		Button:   int8(button),
		Mode:     int32(mode),
		Carried:  pr.carried,
	}
	if pr.ok {
		for j, s := range pr.changed {
			click.Changed = append(click.Changed, packets.ChangedSlot{Slot: int16(j), Item: s})
		}
		if len(click.Changed) > packets.MaxChangedSlots {
			st.mu.Unlock()
			return fmt.Errorf("mcdriver: 変わるスロットが多すぎる: %d", len(click.Changed))
		}
	}
	probe := packets.ContainerClick{WindowID: winID, StateID: -1, Slot: -1, Button: 0, Mode: packets.ClickPickup, Carried: click.Carried}
	seq := st.contentSeq[winID]
	err := cl.invSend(data.PlayServerboundContainerClick, func(e *protocol.Encoder) error {
		packets.WriteContainerClick(e, click)
		return nil
	})
	if err == nil {
		// 手元も予測どおりに変える（公式のクライアントと同じ）
		if pr.ok {
			for j, s := range pr.changed {
				st.setWindowSlotLocked(winID, j, s)
			}
			st.carried = pr.carried
		}
		err = cl.invSend(data.PlayServerboundContainerClick, func(e *protocol.Encoder) error {
			packets.WriteContainerClick(e, probe)
			return nil
		})
	}
	st.mu.Unlock()
	if err != nil {
		return err
	}

	// サーバーから窓の全体が届くのを待つ
	timer := time.NewTimer(clickAnswerTimeout)
	defer timer.Stop()
	for {
		st.mu.Lock()
		got := st.contentSeq[winID] > seq
		bad := st.contentBad[winID]
		ch := st.notify
		if got {
			defer st.mu.Unlock()
			if bad {
				return fmt.Errorf("mcdriver: クリックのあとの窓の内容が読めなかった: %w", st.lastErr)
			}
			cur, curWin := st.windowSlotsLocked()
			if curWin != winID {
				return fmt.Errorf("%w: 窓が変わった（%d → %d）", ErrClickRejected, winID, curWin)
			}
			if !pr.ok {
				return nil
			}
			for j, want := range pr.changed {
				if j >= len(cur) || !cur[j].Equal(want) {
					var have packets.Slot
					if j < len(cur) {
						have = cur[j]
					}
					return fmt.Errorf("%w: スロット %d は %v、予測は %v", ErrClickRejected, j, toItem(have), toItem(want))
				}
			}
			if !st.carried.Equal(pr.carried) {
				return fmt.Errorf("%w: カーソルは %v、予測は %v", ErrClickRejected, toItem(st.carried), toItem(pr.carried))
			}
			return nil
		}
		st.mu.Unlock()
		select {
		case <-ch:
		case <-ctx.Done():
			return fmt.Errorf("mcdriver: クリックの結果を待つ: %w", context.Cause(ctx))
		case <-cl.Done():
			return fmt.Errorf("mcdriver: クリックの結果を待つあいだに切断された: %w", cl.Err())
		case <-timer.C:
			return ErrClickNoAnswer
		}
	}
}

// ---- 送る ----

// invSend は Play の段階のパケットを送る。設定の段階に戻っているあいだは ErrNotPlaying。
func (cl *Client) invSend(id int32, build func(e *protocol.Encoder) error) error {
	cl.mu.Lock()
	defer cl.mu.Unlock()
	if cl.state != data.StatePlay {
		return ErrNotPlaying
	}
	return cl.c.Send(id, build)
}

// ---- 受け取る ----

// invFail は読めなかったパケットを記録する。窓 window（-1 なら分からない）を古いとする。切断はしない。
func (st *invState) failLocked(window int32, what string, err error) {
	st.readErrors++
	st.lastErr = fmt.Errorf("mcdriver: %s を読めなかった（捨てた）: %w", what, err)
	switch {
	case window == 0:
		st.stale = true
	case st.win != nil && window == st.win.id:
		st.win.stale = true
	case window < 0:
		st.stale = true
		if st.win != nil {
			st.win.stale = true
		}
	}
}

func (cl *Client) invOnOpenScreen(d *protocol.Decoder) error {
	st := invKey.of(cl)
	p, err := packets.ReadOpenScreen(d)
	st.mu.Lock()
	defer st.mu.Unlock()
	if err != nil {
		st.failLocked(-1, "Open Screen", err)
		return nil
	}
	name := fmt.Sprintf("unknown_%d", p.Type)
	if r, ok := data.RegistryByName("menu"); ok && p.Type >= 0 && int(p.Type) < len(r.Entries) {
		name = r.Entries[p.Type]
	}
	st.win = &windowState{id: p.WindowID, typeName: name, title: conn.TextPlain(p.Title), playerStart: -2, props: map[int]int{}}
	return nil
}

func (cl *Client) invOnHorseScreen(d *protocol.Decoder) error {
	st := invKey.of(cl)
	id, err := d.ReadVarInt()
	st.mu.Lock()
	defer st.mu.Unlock()
	if err != nil {
		st.failLocked(-1, "Open Horse Screen", err)
		return nil
	}
	st.win = &windowState{id: id, typeName: "horse", playerStart: -2, props: map[int]int{}}
	return nil
}

func (cl *Client) invOnContent(d *protocol.Decoder) error {
	st := invKey.of(cl)
	// 窓の番号は先に読んでおく（中身が読めなくても、どの窓のものかは分かる）
	head := *d
	win, _ := head.ReadVarInt()
	p, err := packets.ReadContainerContent(d)
	st.mu.Lock()
	defer st.mu.Unlock()
	defer st.signalLocked(win, err != nil)
	if err != nil {
		st.failLocked(win, "Set Container Content", err)
		return nil
	}
	switch {
	case p.WindowID == 0:
		for i := range st.slots {
			var s packets.Slot
			if i < len(p.Slots) {
				s = p.Slots[i]
			}
			st.setInvSlotLocked(i, s)
		}
		st.stateID = p.StateID
		st.stale = false
	case st.win != nil && p.WindowID == st.win.id:
		w := st.win
		w.slots = p.Slots
		w.stateID = p.StateID
		w.stale = false
		w.playerStart = playerStart(w.typeName, len(p.Slots))
		for j := range w.slots {
			if i, ok := w.invIndex(j); ok {
				st.slots[i] = w.slots[j]
			}
		}
	default:
		// 開いていない窓のものは捨てる（公式のクライアントと同じ）
		return nil
	}
	st.carried = p.Carried
	return nil
}

// signalLocked は窓 win の Set Container Content が届いたことを知らせる。
func (st *invState) signalLocked(win int32, bad bool) {
	st.contentSeq[win]++
	st.contentBad[win] = bad
	close(st.notify)
	st.notify = make(chan struct{})
}

func (cl *Client) invOnSlot(d *protocol.Decoder) error {
	st := invKey.of(cl)
	head := *d
	win, _ := head.ReadVarInt()
	p, err := packets.ReadContainerSlot(d)
	st.mu.Lock()
	defer st.mu.Unlock()
	if err != nil {
		st.failLocked(win, "Set Container Slot", err)
		return nil
	}
	switch {
	case p.WindowID == -1 && p.Slot == -1:
		st.carried = p.Item
	case p.WindowID == -2:
		if i, ok := playerInvToWindow(int(p.Slot)); ok {
			st.setInvSlotLocked(i, p.Item)
		}
	case p.WindowID == 0:
		if p.Slot >= 0 && int(p.Slot) < InventorySlots {
			st.setInvSlotLocked(int(p.Slot), p.Item)
			st.stateID = p.StateID
		}
	case st.win != nil && p.WindowID == st.win.id:
		st.setWindowSlotLocked(p.WindowID, int(p.Slot), p.Item)
		st.win.stateID = p.StateID
	}
	return nil
}

func (cl *Client) invOnProperty(d *protocol.Decoder) error {
	st := invKey.of(cl)
	p, err := packets.ReadContainerProperty(d)
	st.mu.Lock()
	defer st.mu.Unlock()
	if err != nil {
		st.failLocked(-1, "Set Container Property", err)
		return nil
	}
	if st.win != nil && p.WindowID == st.win.id {
		st.win.props[int(p.Property)] = int(p.Value)
	}
	return nil
}

func (cl *Client) invOnClose(d *protocol.Decoder) error {
	st := invKey.of(cl)
	id, err := d.ReadVarInt()
	st.mu.Lock()
	defer st.mu.Unlock()
	if err != nil {
		st.failLocked(-1, "Close Container", err)
		return nil
	}
	// 公式のクライアントは番号を見ずに閉じる
	st.win = nil
	_ = id
	return nil
}

func (cl *Client) invOnCursor(d *protocol.Decoder) error {
	st := invKey.of(cl)
	s, err := packets.ReadSlot(d)
	st.mu.Lock()
	defer st.mu.Unlock()
	if err != nil {
		st.failLocked(-1, "Set Cursor Item", err)
		return nil
	}
	st.carried = s
	return nil
}

func (cl *Client) invOnHeldSlot(d *protocol.Decoder) error {
	st := invKey.of(cl)
	i, err := d.ReadVarInt()
	st.mu.Lock()
	defer st.mu.Unlock()
	if err != nil {
		st.failLocked(-1, "Set Held Item", err)
		return nil
	}
	if i >= 0 && i <= 8 {
		st.selected = int(i)
	}
	return nil
}

func (cl *Client) invOnPlayerInventory(d *protocol.Decoder) error {
	st := invKey.of(cl)
	p, err := packets.ReadPlayerInventorySlot(d)
	st.mu.Lock()
	defer st.mu.Unlock()
	if err != nil {
		st.failLocked(0, "Set Player Inventory Slot", err)
		return nil
	}
	if i, ok := playerInvToWindow(int(p.Slot)); ok {
		st.setInvSlotLocked(i, p.Item)
	}
	return nil
}

func (cl *Client) invOnRespawn(*protocol.Decoder) error {
	st := invKey.of(cl)
	st.mu.Lock()
	defer st.mu.Unlock()
	// リスポーンやディメンションの移動では、開いている窓は閉じる
	st.win = nil
	return nil
}

// ---- 窓とスロットの番号 ----

// windowLocked は今の窓の番号と、そのスロット（写しではない）を返す。
func (st *invState) windowLocked() (int32, []packets.Slot) {
	if st.win != nil {
		return st.win.id, st.win.slots
	}
	return 0, st.slots[:]
}

// windowSlotsLocked は windowLocked と同じものを（スロット, 窓の番号）の順で返す。
func (st *invState) windowSlotsLocked() ([]packets.Slot, int32) {
	id, s := st.windowLocked()
	return s, id
}

// stateIDLocked は今の窓の state ID を返す。
func (st *invState) stateIDLocked() int32 {
	if st.win != nil {
		return st.win.stateID
	}
	return st.stateID
}

// setInvSlotLocked はプレイヤーの窓のスロット i を変え、開いている容器の持ち物の部分にも写す。
func (st *invState) setInvSlotLocked(i int, s packets.Slot) {
	if i < 0 || i >= InventorySlots {
		return
	}
	st.slots[i] = s
	if w := st.win; w != nil {
		if j, ok := w.windowIndex(i); ok && j < len(w.slots) {
			w.slots[j] = s
		}
	}
}

// setWindowSlotLocked は窓 win のスロット j を変える。容器の持ち物の部分ならプレイヤーの窓にも写す。
func (st *invState) setWindowSlotLocked(win int32, j int, s packets.Slot) {
	if win == 0 {
		st.setInvSlotLocked(j, s)
		return
	}
	w := st.win
	if w == nil || w.id != win || j < 0 {
		return
	}
	if j >= len(w.slots) {
		if j >= packets.MaxWindowSlots {
			return
		}
		// Set Container Content の前に届いた。長さを伸ばしておく
		w.slots = append(w.slots, make([]packets.Slot, j+1-len(w.slots))...)
	}
	w.slots[j] = s
	if i, ok := w.invIndex(j); ok {
		st.slots[i] = s
	}
}

// start は持ち物の部分の始まり。まだ分からなければ窓の種類から決める。
func (w *windowState) start() int {
	if w.playerStart == -2 {
		if n, ok := menuSizes[w.typeName]; ok {
			return n
		}
		return -1
	}
	return w.playerStart
}

// invIndex は容器の窓のスロット j に当たる、プレイヤーの窓の番号を返す。
func (w *windowState) invIndex(j int) (int, bool) {
	ps := w.start()
	if ps < 0 || j < ps {
		return 0, false
	}
	k := j - ps
	switch {
	case k < 27:
		return SlotMain + k, true
	case k < 36:
		return SlotHotbar + k - 27, true
	}
	return 0, false
}

// windowIndex はプレイヤーの窓の番号 i に当たる、容器の窓のスロットを返す。
func (w *windowState) windowIndex(i int) (int, bool) {
	ps := w.start()
	if ps < 0 {
		return 0, false
	}
	switch {
	case i >= SlotMain && i < SlotHotbar:
		return ps + i - SlotMain, true
	case i >= SlotHotbar && i < SlotHotbar+9:
		return ps + 27 + i - SlotHotbar, true
	}
	return 0, false
}

// menuSizes は窓の種類ごとの、容器自身のスロットの数（= 持ち物の部分の始まり）。
// 書見台（lectern）は持ち物の部分が無いので -1。
var menuSizes = map[string]int{
	"generic_9x1": 9, "generic_9x2": 18, "generic_9x3": 27, "generic_9x4": 36, "generic_9x5": 45, "generic_9x6": 54,
	"generic_3x3": 9, "crafter_3x3": 9, "anvil": 3, "beacon": 1, "blast_furnace": 3, "brewing_stand": 5,
	"crafting": 10, "enchantment": 2, "furnace": 3, "grindstone": 3, "hopper": 5, "lectern": -1,
	"loom": 4, "merchant": 3, "shulker_box": 27, "smithing": 4, "smoker": 3, "cartography_table": 3, "stonecutter": 2,
}

// playerStart は窓の種類とスロットの数から、持ち物の部分の始まりを決める。
func playerStart(typeName string, n int) int {
	if ps, ok := menuSizes[typeName]; ok {
		if ps < 0 || ps+36 <= n {
			return ps
		}
	}
	if n >= 36 {
		return n - 36
	}
	return -1
}

// resultSlots は窓の種類ごとの、結果（取り出すだけ）のスロット。クリックの予測をしない。
var resultSlots = map[string][]int{
	"crafting": {0}, "furnace": {2}, "smoker": {2}, "blast_furnace": {2}, "anvil": {2}, "grindstone": {2},
	"smithing": {3}, "stonecutter": {1}, "cartography_table": {2}, "loom": {3}, "merchant": {2}, "crafter_3x3": {45},
}

// playerInvToWindow は持ち物の番号（0〜8 ホットバー, 9〜35 中, 36〜39 足〜頭, 40 左手）を
// プレイヤーの窓の番号にする。
func playerInvToWindow(i int) (int, bool) {
	switch {
	case i >= 0 && i < 9:
		return SlotHotbar + i, true
	case i >= 9 && i < 36:
		return i, true
	case i >= 36 && i < 40:
		return SlotFeet - (i - 36), true
	case i == 40:
		return SlotOffhand, true
	}
	return 0, false
}

func toItem(s packets.Slot) Item {
	if s.Empty() {
		return Item{}
	}
	it, _ := data.ItemByID(data.ItemID(s.Item))
	return Item{Item: it, Count: int(s.Count)}
}

// ---- クリックの予測（公式の AbstractContainerMenu.doClick のうち、よく使うものだけ） ----

// prediction はクリックの予測。ok が false なら予測しない（changed は空、carried は今のまま）。
type prediction struct {
	ok      bool
	changed map[int]packets.Slot
	carried packets.Slot
}

func maxStack(s packets.Slot) int32 {
	if it, ok := data.ItemByID(data.ItemID(s.Item)); ok && it.MaxStackSize > 0 {
		return int32(it.MaxStackSize)
	}
	return 64
}

func isBundle(s packets.Slot) bool {
	if s.Empty() {
		return false
	}
	it, ok := data.ItemByID(data.ItemID(s.Item))
	if !ok {
		return false
	}
	n := it.Name
	return len(n) >= 6 && n[len(n)-6:] == "bundle"
}

func withCount(s packets.Slot, n int32) packets.Slot {
	if n <= 0 {
		return packets.Slot{}
	}
	s.Count = n
	return s
}

// isResultSlotLocked は今の窓のスロット j が結果のスロットかを返す。
func (st *invState) isResultSlotLocked(j int) bool {
	if st.win == nil {
		return j == SlotCraftResult
	}
	for _, r := range resultSlots[st.win.typeName] {
		if r == j {
			return true
		}
	}
	return false
}

// hotbarWindowSlotLocked はホットバーの b 番目（40 なら左手）の、今の窓の番号を返す。
func (st *invState) hotbarWindowSlotLocked(b int) (int, bool) {
	if st.win == nil {
		switch {
		case b >= 0 && b < 9:
			return SlotHotbar + b, true
		case b == 40:
			return SlotOffhand, true
		}
		return 0, false
	}
	if b < 0 || b >= 9 {
		return 0, false
	}
	j, ok := st.win.windowIndex(SlotHotbar + b)
	if !ok || j >= len(st.win.slots) {
		return 0, false
	}
	return j, true
}

// predictLocked はクリックの結果を予測する。
func (st *invState) predictLocked(slot, button int, mode ClickMode) prediction {
	_, slots := st.windowLocked()
	carried := st.carried
	none := prediction{carried: carried}
	pr := prediction{ok: true, changed: map[int]packets.Slot{}, carried: carried}
	set := func(j int, s packets.Slot) { pr.changed[j] = s }
	inWindow := slot >= 0 && slot < len(slots)

	switch mode {
	case ClickPickup:
		if button != 0 && button != 1 {
			return none
		}
		left := button == 0
		if slot == SlotOutside {
			if !carried.Empty() {
				if left {
					pr.carried = packets.Slot{}
				} else {
					pr.carried = withCount(carried, carried.Count-1)
				}
			}
			return pr
		}
		if !inWindow {
			return pr // 何も起きない
		}
		if st.isResultSlotLocked(slot) {
			return none
		}
		cur := slots[slot]
		if isBundle(cur) || isBundle(carried) {
			return none
		}
		switch {
		case cur.Empty() && carried.Empty():
		case cur.Empty():
			n := int32(1)
			if left {
				n = carried.Count
			}
			n = min(n, maxStack(carried))
			set(slot, withCount(carried, n))
			pr.carried = withCount(carried, carried.Count-n)
		case carried.Empty():
			take := (cur.Count + 1) / 2
			if left {
				take = cur.Count
			}
			pr.carried = withCount(cur, take)
			set(slot, withCount(cur, cur.Count-take))
		case cur.SameItem(carried):
			n := int32(1)
			if left {
				n = carried.Count
			}
			n = min(n, maxStack(cur)-cur.Count)
			if n > 0 {
				set(slot, withCount(cur, cur.Count+n))
				pr.carried = withCount(carried, carried.Count-n)
			}
		default:
			if carried.Count <= maxStack(carried) {
				set(slot, carried)
				pr.carried = cur
			}
		}
		return pr

	case ClickSwap:
		hb, ok := st.hotbarWindowSlotLocked(button)
		if !ok || !inWindow || st.isResultSlotLocked(slot) {
			return none
		}
		if hb == slot {
			return pr
		}
		h, s := slots[hb], slots[slot]
		if isBundle(h) || isBundle(s) {
			return none
		}
		switch {
		case h.Empty() && s.Empty():
		case h.Empty():
			set(hb, s)
			set(slot, packets.Slot{})
		case s.Empty():
			m := maxStack(h)
			if h.Count > m {
				set(slot, withCount(h, m))
				set(hb, withCount(h, h.Count-m))
			} else {
				set(slot, h)
				set(hb, packets.Slot{})
			}
		default:
			if h.Count > maxStack(h) {
				return none
			}
			set(slot, h)
			set(hb, s)
		}
		return pr

	case ClickThrow:
		if !carried.Empty() || !inWindow {
			return pr // 何も起きない
		}
		if st.isResultSlotLocked(slot) {
			return none
		}
		cur := slots[slot]
		if cur.Empty() {
			return pr
		}
		n := int32(1)
		if button != 0 {
			n = cur.Count
		}
		set(slot, withCount(cur, cur.Count-n))
		return pr
	}
	return none
}
