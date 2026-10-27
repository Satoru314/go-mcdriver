// Package conn は Minecraft Java Edition 1.21.4（protocol 769）の接続を扱う。
//
//   - Conn: net.Conn を包み、パケットの読み書き、圧縮の閾値の切り替え、書き込みの直列化を行う
//   - Login: ハンドシェイクからログイン完了（Login Acknowledged）まで（オフラインのみ）
//   - Configure: 設定の段階（Configuration）を Finish Configuration まで進める
//   - OfflineUUID: オフラインモードの UUID
//   - TextPlain: 文字の部品（Text Component）を読める文字列にする
//
// internal/protocol はゲームのことを知らない下の層なので、パケットの中身と状態の流れはここに置く。
package conn

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/Satoru314/go-mcdriver/internal/protocol"
)

const (
	// DefaultReadTimeout はサーバーから何も届かないときに切るまでの時間。
	// サーバーは 15 秒ごとに Keep Alive を送るので、その 2 倍にする（公式クライアントと同じ 30 秒）。
	DefaultReadTimeout = 30 * time.Second
	// DefaultWriteTimeout は 1 つのパケットの書き込みにかけてよい時間。
	DefaultWriteTimeout = 10 * time.Second
)

// ErrClosed は Close したあとの読み書きで返すエラー。
var ErrClosed = errors.New("conn: 接続は閉じている")

// Conn は 1 本の接続。
//
// 読むのは 1 つの goroutine だけ（ReadPacket を同時に呼ばない）。
// 書くのは複数の goroutine から同時に呼んでよい（1 パケットずつ直列にする）。
type Conn struct {
	nc net.Conn
	br *bufio.Reader

	// 読む側の閾値。読む goroutine だけが触る（SetThreshold も読む goroutine から呼ぶ）
	readThreshold int

	wmu            sync.Mutex
	writeThreshold int // wmu で守る

	ReadTimeout  time.Duration // 0 なら期限なし
	WriteTimeout time.Duration // 0 なら期限なし

	closeOnce sync.Once
	closed    chan struct{}

	// dmu は期限の設定と、ctx による中止（cancelErr）を守る。
	// 中止したあとに ReadPacket が期限を延ばしてしまわないようにするため。
	dmu       sync.Mutex
	cancelErr error
}

// New は nc を包んだ Conn を作る。最初は圧縮なし。
func New(nc net.Conn) *Conn {
	return &Conn{
		nc:             nc,
		br:             bufio.NewReaderSize(nc, 64*1024),
		readThreshold:  protocol.NoCompression,
		writeThreshold: protocol.NoCompression,
		ReadTimeout:    DefaultReadTimeout,
		WriteTimeout:   DefaultWriteTimeout,
		closed:         make(chan struct{}),
	}
}

// Dial は addr（host:port）に TCP で繋ぐ。ctx で中止できる。
func Dial(ctx context.Context, addr string) (*Conn, error) {
	var d net.Dialer
	nc, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, err
	}
	return New(nc), nil
}

// SetThreshold は圧縮の閾値を切り替える（Set Compression を受け取ったとき）。負なら圧縮なし。
// 読む側はこの直後のパケットから、書く側は次に書くパケットから新しい閾値を使う。
// 読む goroutine から呼ぶこと。
func (c *Conn) SetThreshold(threshold int) {
	if threshold < 0 {
		threshold = protocol.NoCompression
	}
	c.readThreshold = threshold
	c.wmu.Lock()
	c.writeThreshold = threshold
	c.wmu.Unlock()
}

// Threshold は今の（読む側の）圧縮の閾値を返す。
func (c *Conn) Threshold() int { return c.readThreshold }

// ReadPacket はパケットを 1 つ読む。ReadTimeout のあいだ何も届かなければエラーになる。
func (c *Conn) ReadPacket() (protocol.Packet, error) {
	if err := c.setDeadline(c.nc.SetReadDeadline, c.ReadTimeout); err != nil {
		return protocol.Packet{}, err
	}
	p, err := protocol.ReadPacket(c.br, c.readThreshold)
	if err != nil {
		return protocol.Packet{}, c.wrapErr(err)
	}
	return p, nil
}

// WritePacket はパケットを 1 つ書く。複数の goroutine から呼んでよい。
func (c *Conn) WritePacket(id int32, data []byte) error {
	c.wmu.Lock()
	defer c.wmu.Unlock()
	b, err := protocol.EncodePacket(protocol.Packet{ID: id, Data: data}, c.writeThreshold)
	if err != nil {
		return err
	}
	if err := c.setDeadline(c.nc.SetWriteDeadline, c.WriteTimeout); err != nil {
		return err
	}
	if _, err := c.nc.Write(b); err != nil {
		return c.wrapErr(err)
	}
	return nil
}

// Send は build で中身を作ってパケットを書く。
func (c *Conn) Send(id int32, build func(e *protocol.Encoder) error) error {
	e := protocol.NewEncoder()
	if build != nil {
		if err := build(e); err != nil {
			return fmt.Errorf("パケット 0x%02x を作る: %w", id, err)
		}
	}
	return c.WritePacket(id, e.Bytes())
}

// Close は接続を閉じる。何度呼んでもよい。読み書きしている goroutine はエラーで戻る。
func (c *Conn) Close() error {
	var err error
	c.closeOnce.Do(func() {
		close(c.closed)
		err = c.nc.Close()
	})
	return err
}

// Closed は Close されたら閉じるチャネルを返す。
func (c *Conn) Closed() <-chan struct{} { return c.closed }

// WatchContext は ctx が終わったら読み書きを止める（期限を過去にする）。
// 止めたあとの読み書きは ctx.Err() を返す（接続はもう使えない）。
// 戻り値の stop を呼ぶと見張りをやめる。stop が false を返したら、すでに止めたあと。
func (c *Conn) WatchContext(ctx context.Context) (stop func() bool) {
	return context.AfterFunc(ctx, func() {
		c.dmu.Lock()
		defer c.dmu.Unlock()
		c.cancelErr = context.Cause(ctx)
		_ = c.nc.SetDeadline(time.Unix(1, 0))
	})
}

// setDeadline は timeout 後の期限を set で設定する。中止されていたらそのエラーを返す。
func (c *Conn) setDeadline(set func(time.Time) error, timeout time.Duration) error {
	c.dmu.Lock()
	defer c.dmu.Unlock()
	if c.cancelErr != nil {
		return c.cancelErr
	}
	var t time.Time // 0 なら期限なし
	if timeout > 0 {
		t = time.Now().Add(timeout)
	}
	if err := set(t); err != nil {
		return c.wrapErr(err)
	}
	return nil
}

// wrapErr は Close 後のエラーを ErrClosed に、ctx で中止したあとのエラーを ctx のエラーにする。
func (c *Conn) wrapErr(err error) error {
	select {
	case <-c.closed:
		return ErrClosed
	default:
	}
	c.dmu.Lock()
	cerr := c.cancelErr
	c.dmu.Unlock()
	if cerr != nil {
		return cerr
	}
	return err
}
