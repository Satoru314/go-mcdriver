package rcon

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"sync"
	"time"
)

// ErrAuthFailed はパスワードが違うときのエラー。
var ErrAuthFailed = errors.New("rcon: 認証に失敗した（パスワードが違う）")

// ErrClosed は閉じた、または途中で壊れた接続を使ったときのエラー。
var ErrClosed = errors.New("rcon: 接続は使えない（閉じた、または通信の途中で中断された）")

// Client は RCON の接続 1 本。
//
// 複数の goroutine から同時に Command を呼んでよい（中で 1 つずつ順に送る）。
// context の時間切れや中止で通信を途中で打ち切った接続は、応答の対応が
// ずれるので以後は使えない（ErrClosed を返す）。作り直すこと。
type Client struct {
	mu     sync.Mutex
	conn   net.Conn
	r      *bufio.Reader
	nextID int32
	broken error

	// dlMu と gen は、ctx の打ち切りが遅れて届いて次のコマンドを壊さないようにするためのもの
	dlMu sync.Mutex
	gen  uint64
}

// Dial は addr（"host:port"）に接続し、password で認証する。
func Dial(ctx context.Context, addr, password string) (*Client, error) {
	var d net.Dialer
	conn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("rcon: 接続できない: %w", err)
	}
	c := &Client{conn: conn, r: bufio.NewReader(conn), nextID: 1}
	if err := c.auth(ctx, password); err != nil {
		conn.Close()
		return nil, err
	}
	return c, nil
}

// Close は接続を閉じる。
func (c *Client) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.broken == nil {
		c.broken = ErrClosed
	}
	return c.conn.Close()
}

// allocID は次のリクエスト ID を返す。-1 は認証失敗の印なので使わない。
func (c *Client) allocID() int32 {
	id := c.nextID
	c.nextID++
	if c.nextID <= 0 {
		c.nextID = 1
	}
	return id
}

// watch は ctx が終わったら通信を打ち切るようにする。戻り値の関数で見張りをやめる。
//
// ctx の期限を接続の締め切りに写すことはしない。写すと、接続の締め切りが ctx より
// わずかに先に切れたとき、fail の時点で ctx.Err() がまだ nil で、context のエラーを
// 返せないことがあるため（reports/2026-10-27-unit0-TestCommandContextTimeout.md）。
// AfterFunc は ctx が終わったあとに呼ばれるので、打ち切られたときは ctx.Err() が必ず非 nil。
func (c *Client) watch(ctx context.Context) (stop func()) {
	c.dlMu.Lock()
	c.gen++
	gen := c.gen
	c.conn.SetDeadline(time.Time{})
	c.dlMu.Unlock()
	stopAF := context.AfterFunc(ctx, func() {
		c.dlMu.Lock()
		defer c.dlMu.Unlock()
		// 見張りをやめたあと（次のコマンドに移ったあと）に遅れて呼ばれたときは何もしない
		if c.gen == gen {
			// 過去の時刻を締め切りにすると、読み書きがすぐにエラーで戻る
			c.conn.SetDeadline(time.Unix(1, 0))
		}
	})
	return func() {
		stopAF()
		c.dlMu.Lock()
		c.gen++
		c.dlMu.Unlock()
	}
}

// fail は通信の失敗を記録し、以後この接続を使えなくする。
func (c *Client) fail(ctx context.Context, err error) error {
	if ctxErr := ctx.Err(); ctxErr != nil {
		err = fmt.Errorf("rcon: 中断された: %w", ctxErr)
	}
	c.broken = err
	c.conn.Close()
	return err
}

func (c *Client) auth(ctx context.Context, password string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	stop := c.watch(ctx)
	defer stop()

	id := c.allocID()
	if _, err := c.conn.Write(Packet{ID: id, Type: TypeAuth, Body: password}.Encode()); err != nil {
		return c.fail(ctx, fmt.Errorf("rcon: 認証を送れない: %w", err))
	}
	for {
		p, err := ReadPacket(c.r)
		if err != nil {
			return c.fail(ctx, fmt.Errorf("rcon: 認証の応答を読めない: %w", err))
		}
		// Source RCON の仕様では、認証の応答の前に空の応答（種類 0）が来ることがある。読み飛ばす
		if p.Type != TypeAuthResponse {
			continue
		}
		if p.ID == -1 {
			c.broken = ErrAuthFailed
			return ErrAuthFailed
		}
		if p.ID != id {
			return c.fail(ctx, fmt.Errorf("rcon: 認証の応答の ID が違う: 送った %d, 来た %d", id, p.ID))
		}
		return nil
	}
}

// Command はコマンドを 1 つ送り、応答の本文を返す。先頭の "/" は付けない。
//
// 応答が 4096 バイトを超えると、サーバーは複数のパケットに分けて送る。
// その終わりを知るために、最初の応答が来たあとで種類の不明なパケットを送り、
// それへの応答（"Unknown request ..."）が来るまでの本文をつなげて返す。
//
// 終わりの印をコマンドと同時に送らないのは、Minecraft のサーバーが 1 回の読み込み
// （最大 1460 バイト）から 1 つのパケットしか取り出さず、同じ読み込みに入った
// 2 つ目のパケットを捨ててしまうため。サーバーはコマンドの応答を送り終えてから
// 次の読み込みをするので、最初の応答を受け取ってから送れば別々に読まれる。
func (c *Client) Command(ctx context.Context, cmd string) (string, error) {
	if len(cmd) > MaxCommandLen {
		return "", fmt.Errorf("%w: コマンドが %d バイト（上限 %d）", ErrPacketTooLarge, len(cmd), MaxCommandLen)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.broken != nil {
		return "", c.broken
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	stop := c.watch(ctx)
	defer stop()

	cmdID := c.allocID()
	endID := c.allocID()
	if _, err := c.conn.Write(Packet{ID: cmdID, Type: TypeCommand, Body: cmd}.Encode()); err != nil {
		return "", c.fail(ctx, fmt.Errorf("rcon: コマンドを送れない: %w", err))
	}

	var body strings.Builder
	sentEnd := false
	for {
		p, err := ReadPacket(c.r)
		if err != nil {
			return "", c.fail(ctx, fmt.Errorf("rcon: 応答を読めない: %w", err))
		}
		switch {
		case p.ID == cmdID:
			body.WriteString(p.Body)
			if !sentEnd {
				// 終わりの印。種類 0（応答）は、サーバーから見て不明な依頼になる
				if _, err := c.conn.Write(Packet{ID: endID, Type: TypeResponse}.Encode()); err != nil {
					return "", c.fail(ctx, fmt.Errorf("rcon: 終わりの印を送れない: %w", err))
				}
				sentEnd = true
			}
		case p.ID == endID && sentEnd:
			return body.String(), nil
		case p.ID == -1:
			return "", c.fail(ctx, errors.New("rcon: 認証されていないと言われた"))
		default:
			return "", c.fail(ctx, fmt.Errorf("rcon: 思っていない ID の応答: %d（待っていたのは %d と %d）", p.ID, cmdID, endID))
		}
	}
}
