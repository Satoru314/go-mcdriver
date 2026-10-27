package conn

import (
	"bufio"
	"context"
	"errors"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/Satoru314/go-mcdriver/internal/protocol"
)

// 相手側（生の net.Conn）とつながった Conn を作る。
func pipe(t *testing.T) (*Conn, net.Conn) {
	t.Helper()
	a, b := net.Pipe()
	t.Cleanup(func() { a.Close(); b.Close() })
	return New(a), b
}

// 閾値を切り替えると、読み書きの両方が新しい枠になること。
func TestConnThreshold(t *testing.T) {
	c, peer := pipe(t)
	br := bufio.NewReader(peer)
	big := make([]byte, 300)
	for i := range big {
		big[i] = byte(i)
	}

	for _, th := range []int{protocol.NoCompression, 0, 256} {
		c.SetThreshold(th)
		for _, data := range [][]byte{{1, 2, 3}, big} {
			// Conn → 相手
			errc := make(chan error, 1)
			go func() { errc <- c.WritePacket(0x10, data) }()
			p, err := protocol.ReadPacket(br, th)
			if err != nil {
				t.Fatalf("閾値 %d: 相手が読む: %v", th, err)
			}
			if err := <-errc; err != nil {
				t.Fatalf("閾値 %d: 書く: %v", th, err)
			}
			if p.ID != 0x10 || string(p.Data) != string(data) {
				t.Fatalf("閾値 %d: ID %d、%d バイト。期待 0x10、%d バイト", th, p.ID, len(p.Data), len(data))
			}
			// 相手 → Conn
			go func() { errc <- protocol.WritePacket(peer, protocol.Packet{ID: 0x20, Data: data}, th) }()
			p, err = c.ReadPacket()
			if err != nil {
				t.Fatalf("閾値 %d: 読む: %v", th, err)
			}
			if err := <-errc; err != nil {
				t.Fatal(err)
			}
			if p.ID != 0x20 || string(p.Data) != string(data) {
				t.Fatalf("閾値 %d: ID %d、%d バイト。期待 0x20、%d バイト", th, p.ID, len(p.Data), len(data))
			}
		}
	}
}

// 複数の goroutine から同時に書いても、パケットが混ざらないこと。
func TestConnConcurrentWrite(t *testing.T) {
	c, peer := pipe(t)
	c.SetThreshold(64)
	const writers, each = 8, 50
	var wg sync.WaitGroup
	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			data := make([]byte, 10+w*20) // 閾値の上と下が混ざるように
			for i := range data {
				data[i] = byte(w)
			}
			for i := 0; i < each; i++ {
				if err := c.WritePacket(int32(w), data); err != nil {
					t.Error(err)
					return
				}
			}
		}(w)
	}
	br := bufio.NewReader(peer)
	counts := make([]int, writers)
	for n := 0; n < writers*each; n++ {
		p, err := protocol.ReadPacket(br, 64)
		if err != nil {
			t.Fatalf("%d 個目: %v", n, err)
		}
		w := int(p.ID)
		if w >= writers || len(p.Data) != 10+w*20 {
			t.Fatalf("壊れたパケット: ID %d、%d バイト", p.ID, len(p.Data))
		}
		for _, b := range p.Data {
			if int(b) != w {
				t.Fatalf("中身が混ざっている: ID %d に %d", w, b)
			}
		}
		counts[w]++
	}
	wg.Wait()
	for w, n := range counts {
		if n != each {
			t.Errorf("ID %d: %d 個、期待 %d", w, n, each)
		}
	}
}

// ctx が終わると、止まっている ReadPacket が ctx のエラーで戻ること。
func TestConnWatchContext(t *testing.T) {
	c, _ := pipe(t)
	ctx, cancel := context.WithCancel(context.Background())
	stop := c.WatchContext(ctx)
	defer stop()
	errc := make(chan error, 1)
	go func() {
		_, err := c.ReadPacket()
		errc <- err
	}()
	time.Sleep(50 * time.Millisecond)
	cancel()
	select {
	case err := <-errc:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("エラー %v、期待 context.Canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("ReadPacket が戻らない")
	}
	// 中止したあとの読み書きもエラーになる
	if _, err := c.ReadPacket(); !errors.Is(err, context.Canceled) {
		t.Errorf("中止後の ReadPacket: %v", err)
	}
	if err := c.WritePacket(1, nil); !errors.Is(err, context.Canceled) {
		t.Errorf("中止後の WritePacket: %v", err)
	}
}

// 何も届かなければ ReadTimeout で戻ること。
func TestConnReadTimeout(t *testing.T) {
	c, _ := pipe(t)
	c.ReadTimeout = 100 * time.Millisecond
	start := time.Now()
	_, err := c.ReadPacket()
	var ne net.Error
	if !errors.As(err, &ne) || !ne.Timeout() {
		t.Fatalf("エラー %v、期待 時間切れ", err)
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Errorf("戻るまで %v", d)
	}
}

// Close したあとは ErrClosed になること。
func TestConnClose(t *testing.T) {
	c, _ := pipe(t)
	errc := make(chan error, 1)
	go func() {
		_, err := c.ReadPacket()
		errc <- err
	}()
	time.Sleep(20 * time.Millisecond)
	c.Close()
	c.Close() // 2 回目も大丈夫
	select {
	case err := <-errc:
		if !errors.Is(err, ErrClosed) {
			t.Fatalf("エラー %v、期待 ErrClosed", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("ReadPacket が戻らない")
	}
	select {
	case <-c.Closed():
	default:
		t.Error("Closed が閉じていない")
	}
}
