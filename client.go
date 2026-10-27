// Package mcdriver は Minecraft Java Edition 1.21.4 に bot をログインさせ、
// ゲームの情報を受け取り、行動を指示するためのドライバ。
//
// 今はログインと接続の維持だけができる（単位 3）。
//
//	c, err := mcdriver.Connect(ctx, mcdriver.Config{Addr: "localhost:25565", Username: "bot1"})
//	if err != nil { ... }
//	defer c.Close()
//	<-c.Done()
//	log.Println(c.Err())
//
// サーバーはオフラインモード（online-mode=false）である必要がある。
package mcdriver

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strconv"
	"sync"
	"time"

	"github.com/Satoru314/go-mcdriver/data"
	"github.com/Satoru314/go-mcdriver/internal/conn"
	"github.com/Satoru314/go-mcdriver/internal/packets"
	"github.com/Satoru314/go-mcdriver/internal/protocol"
)

// TickInterval は 1 ティックの長さ。
const TickInterval = 50 * time.Millisecond

// DefaultPort は Addr にポートがないときに使うポート。
const DefaultPort = 25565

var (
	// ErrClosed は Close で閉じたときに Err が返すエラー。
	ErrClosed = errors.New("mcdriver: Close で閉じた")
	// ErrEncryption はサーバーが暗号化（オンラインモード）を求めたときのエラー。
	ErrEncryption = conn.ErrEncryption
	// ErrTransfer はサーバーが別のサーバーへの移動を求めたときのエラー。
	ErrTransfer = conn.ErrTransfer
)

// DisconnectError はサーバーから切断されたときのエラー。Reason に理由が入る。
// errors.As で取り出せる。
type DisconnectError = conn.DisconnectError

// Config は接続の設定。
type Config struct {
	// Addr はサーバーの "host:port"。ポートを省くと 25565
	Addr string
	// Username はプレイヤー名（1〜16 文字）。UUID はオフラインの決まりで名前から作る
	Username string
	// ViewDistance は描画距離（チャンク）。0 なら 8
	ViewDistance int
	// Locale は言語（例 "ja_jp"）。空なら "en_us"
	Locale string
}

// Client はサーバーに繋がった 1 人の bot。
type Client struct {
	cfg     Config
	c       *conn.Conn
	profile conn.Profile
	reg     *conn.Registries
	info    conn.ClientInfo

	// mu は段階と自分の位置を守る。段階を切り替えるパケットと位置のパケットの順番を保つため、
	// それらを送るあいだも mu を持つ。
	mu           sync.Mutex
	state        string // data.StateConfiguration か data.StatePlay
	login        packets.LoginPlay
	hasPos       bool // 位置を受け取った（まだなら位置を送らない）
	loadedSent   bool // Player Loaded を送った
	x, y, z      float64
	yaw, pitch   float32
	onGround     bool
	teleports    int // 受け取った位置の強制修正の数
	reconfigures int // Play から設定の段階に戻った数

	wg       sync.WaitGroup
	done     chan struct{}
	stopOnce sync.Once
	errMu    sync.Mutex
	err      error
}

// Connect はサーバーに繋ぎ、ログインして、ゲームに入る（Login (play) を受け取る）まで待つ。
// ctx は接続してゲームに入るまでのあいだだけ使う。入ったあとに切るには Close を呼ぶ。
func Connect(ctx context.Context, cfg Config) (*Client, error) {
	if cfg.Username == "" || len(cfg.Username) > conn.MaxUsernameLen {
		return nil, fmt.Errorf("mcdriver: Username の長さが正しくない: %q（1〜%d 文字）", cfg.Username, conn.MaxUsernameLen)
	}
	host, port, err := splitAddr(cfg.Addr)
	if err != nil {
		return nil, err
	}
	c, err := conn.Dial(ctx, net.JoinHostPort(host, strconv.Itoa(int(port))))
	if err != nil {
		return nil, fmt.Errorf("mcdriver: 接続: %w", err)
	}

	cl := &Client{
		cfg:      cfg,
		c:        c,
		reg:      conn.NewRegistries(),
		state:    data.StateConfiguration,
		onGround: true, // 物理ができるまでは「立っている」とする（浮いていると見なされて切られないように）
		done:     make(chan struct{}),
		info: conn.ClientInfo{
			Locale:       cfg.Locale,
			ViewDistance: int8(min(max(cfg.ViewDistance, 0), 32)),
			MainHand:     conn.DefaultClientInfo.MainHand,
		},
	}

	stop := c.WatchContext(ctx)
	if err := cl.enter(host, port); err != nil {
		stop()
		c.Close()
		if ctx.Err() != nil {
			return nil, fmt.Errorf("mcdriver: %w", context.Cause(ctx))
		}
		return nil, fmt.Errorf("mcdriver: %w", err)
	}
	if !stop() {
		// 入り終わる前後に ctx が終わった
		c.Close()
		return nil, fmt.Errorf("mcdriver: %w", context.Cause(ctx))
	}

	cl.wg.Add(2)
	go cl.readLoop()
	go cl.tickLoop()
	return cl, nil
}

// splitAddr は "host:port" を分ける。ポートがなければ 25565。
func splitAddr(addr string) (string, uint16, error) {
	if addr == "" {
		return "", 0, errors.New("mcdriver: Addr が空")
	}
	host, ps, err := net.SplitHostPort(addr)
	if err != nil {
		// ポートが無い（"example.com" など）
		return addr, DefaultPort, nil
	}
	p, err := strconv.ParseUint(ps, 10, 16)
	if err != nil {
		return "", 0, fmt.Errorf("mcdriver: Addr のポートが正しくない: %q", addr)
	}
	return host, uint16(p), nil
}

// enter はログインから Login (play) を受け取るまでを行う。
func (cl *Client) enter(host string, port uint16) error {
	prof, err := conn.Login(cl.c, host, port, cl.cfg.Username)
	if err != nil {
		return err
	}
	cl.profile = prof
	if err := conn.Configure(cl.c, cl.info, cl.reg, true); err != nil {
		return err
	}
	cl.mu.Lock()
	cl.state = data.StatePlay
	cl.mu.Unlock()
	// Login (play) が来るまで、届いたものはふつうに処理する
	for {
		p, err := cl.c.ReadPacket()
		if err != nil {
			return fmt.Errorf("Play の段階で読む: %w", err)
		}
		if err := cl.handlePlay(p); err != nil {
			return err
		}
		if p.ID == data.PlayClientboundLogin {
			return nil
		}
	}
}

// Close は接続を閉じ、中の goroutine が終わるまで待つ。何度呼んでもよい。
func (cl *Client) Close() error {
	cl.shutdown(ErrClosed)
	cl.wg.Wait()
	return nil
}

// Done は接続が終わったら閉じるチャネルを返す。
func (cl *Client) Done() <-chan struct{} { return cl.done }

// Err は接続が終わった理由を返す。終わっていなければ nil。
// Close で閉じたなら ErrClosed、サーバーに切られたなら *DisconnectError。
func (cl *Client) Err() error {
	select {
	case <-cl.done:
	default:
		return nil
	}
	cl.errMu.Lock()
	defer cl.errMu.Unlock()
	return cl.err
}

// shutdown は最初の 1 回だけ理由を記録して接続を閉じる。
func (cl *Client) shutdown(err error) {
	cl.stopOnce.Do(func() {
		cl.errMu.Lock()
		cl.err = err
		cl.errMu.Unlock()
		cl.c.Close()
		close(cl.done)
	})
}

// readLoop はパケットを受け取り続ける。
func (cl *Client) readLoop() {
	defer cl.wg.Done()
	for {
		p, err := cl.c.ReadPacket()
		if err != nil {
			cl.shutdown(fmt.Errorf("mcdriver: 受信: %w", err))
			return
		}
		if err := cl.handlePlay(p); err != nil {
			cl.shutdown(err)
			return
		}
	}
}

// tickLoop は毎ティックの処理をする。今は位置をそのまま送るだけ（物理は後の単位）。
func (cl *Client) tickLoop() {
	defer cl.wg.Done()
	t := time.NewTicker(TickInterval)
	defer t.Stop()
	for {
		select {
		case <-cl.done:
			return
		case <-t.C:
		}
		if err := cl.tick(); err != nil {
			cl.shutdown(fmt.Errorf("mcdriver: 送信: %w", err))
			return
		}
	}
}

// tick は 1 ティック分の送信をする。
func (cl *Client) tick() error {
	cl.mu.Lock()
	defer cl.mu.Unlock()
	if cl.state != data.StatePlay || !cl.hasPos {
		return nil
	}
	if err := cl.sendPositionLocked(); err != nil {
		return err
	}
	// 1.21.2 から、公式のクライアントはティックの終わりに Client Tick End を送る
	return cl.c.WritePacket(data.PlayServerboundClientTickEnd, nil)
}

// sendPositionLocked は今の位置と向きを送る。mu を持って呼ぶ。
func (cl *Client) sendPositionLocked() error {
	var flags uint8
	if cl.onGround {
		flags |= packets.MoveOnGround
	}
	return cl.c.Send(data.PlayServerboundMovePlayerPosRot, func(e *protocol.Encoder) error {
		packets.WriteMovePlayerPosRot(e, packets.MovePlayerPosRot{
			X: cl.x, Y: cl.y, Z: cl.z, Yaw: cl.yaw, Pitch: cl.pitch, Flags: flags,
		})
		return nil
	})
}

// handlePlay は Play の段階のパケットを 1 つ処理する。
func (cl *Client) handlePlay(p protocol.Packet) error {
	d := protocol.NewDecoder(p.Data)
	switch p.ID {
	case data.PlayClientboundKeepAlive:
		return conn.ReplyKeepAlive(cl.c, d, data.PlayServerboundKeepAlive)

	case data.PlayClientboundPing:
		return conn.ReplyPing(cl.c, d, data.PlayServerboundPong)

	case data.PlayClientboundLogin:
		lp, err := packets.ReadLoginPlay(d)
		if err != nil {
			return fmt.Errorf("mcdriver: Login (play): %w", err)
		}
		cl.mu.Lock()
		cl.login = lp
		cl.mu.Unlock()

	case data.PlayClientboundPlayerPosition:
		pp, err := packets.ReadPlayerPosition(d)
		if err != nil {
			return fmt.Errorf("mcdriver: Synchronize Player Position: %w", err)
		}
		return cl.onTeleport(pp)

	case data.PlayClientboundChunkBatchFinished:
		// 受け取ったことを伝えないと、サーバーは次のチャンクを送ってこない
		return cl.c.Send(data.PlayServerboundChunkBatchReceived, func(e *protocol.Encoder) error {
			e.WriteFloat(chunksPerTick)
			return nil
		})

	case data.PlayClientboundStartConfiguration:
		return cl.reconfigure()

	case data.PlayClientboundDisconnect:
		reason, err := conn.ReadTextNBT(d)
		if err != nil {
			return fmt.Errorf("mcdriver: Disconnect (play): %w", err)
		}
		return &DisconnectError{State: data.StatePlay, Reason: reason}

	case data.PlayClientboundUpdateTags:
		if err := cl.reg.ReadUpdateTags(d); err != nil {
			return fmt.Errorf("mcdriver: Update Tags (play): %w", err)
		}

	case data.PlayClientboundCookieRequest:
		return conn.ReplyCookie(cl.c, d, data.PlayServerboundCookieResponse)

	case data.PlayClientboundResourcePackPush:
		return conn.ReplyResourcePack(cl.c, d, data.PlayServerboundResourcePack)

	case data.PlayClientboundTransfer:
		return ErrTransfer

	default:
		// 知らないパケットは読み飛ばす（今後の単位で扱う）
	}
	return nil
}

// chunksPerTick は Chunk Batch Received で伝える「1 ティックに受け取れるチャンクの数」。
const chunksPerTick = 20

// onTeleport はサーバーからの位置の強制修正を当てはめ、確認と位置を送り返す。
func (cl *Client) onTeleport(pp packets.PlayerPosition) error {
	cl.mu.Lock()
	defer cl.mu.Unlock()
	cl.x, cl.y, cl.z, cl.yaw, cl.pitch = pp.Apply(cl.x, cl.y, cl.z, cl.yaw, cl.pitch)
	cl.hasPos = true
	cl.teleports++
	if err := cl.c.Send(data.PlayServerboundAcceptTeleportation, func(e *protocol.Encoder) error {
		e.WriteVarInt(pp.TeleportID)
		return nil
	}); err != nil {
		return err
	}
	if err := cl.sendPositionLocked(); err != nil {
		return err
	}
	if !cl.loadedSent {
		// 1.21.4 から、クライアントは世界を読み込めたら Player Loaded を送る。
		// 送るまでサーバーはプレイヤーを動かさず、ダメージも受けない
		cl.loadedSent = true
		return cl.c.WritePacket(data.PlayServerboundPlayerLoaded, nil)
	}
	return nil
}

// reconfigure は Start Configuration を受け取ったときに、設定の段階に戻ってまた Play に入る。
func (cl *Client) reconfigure() error {
	cl.mu.Lock()
	cl.state = data.StateConfiguration
	cl.hasPos = false // 戻ったあとはサーバーが位置を送り直す
	cl.loadedSent = false
	cl.reconfigures++
	err := cl.c.WritePacket(data.PlayServerboundConfigurationAcknowledged, nil)
	cl.mu.Unlock()
	if err != nil {
		return fmt.Errorf("mcdriver: Acknowledge Configuration を送る: %w", err)
	}
	if err := conn.Configure(cl.c, cl.info, cl.reg, false); err != nil {
		return fmt.Errorf("mcdriver: %w", err)
	}
	cl.mu.Lock()
	cl.state = data.StatePlay
	cl.mu.Unlock()
	return nil
}
