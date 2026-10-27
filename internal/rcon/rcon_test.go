package rcon

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// 仕様どおりのバイト列になるか。
func TestPacketEncode(t *testing.T) {
	got := Packet{ID: 1, Type: TypeAuth, Body: "pw"}.Encode()
	want := []byte{
		0x0c, 0x00, 0x00, 0x00, // 長さ = 4 + 4 + 2 + 2 = 12
		0x01, 0x00, 0x00, 0x00, // ID = 1
		0x03, 0x00, 0x00, 0x00, // 種類 = 3（認証）
		'p', 'w', // 本文
		0x00, 0x00, // 終端
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("Encode:\n got  % x\n want % x", got, want)
	}

	// ID が負（-1 = 認証失敗）でも 2 の補数で書かれる
	got = Packet{ID: -1, Type: TypeAuthResponse}.Encode()
	want = []byte{0x0a, 0, 0, 0, 0xff, 0xff, 0xff, 0xff, 0x02, 0, 0, 0, 0, 0}
	if !bytes.Equal(got, want) {
		t.Fatalf("Encode(-1):\n got  % x\n want % x", got, want)
	}
}

func TestReadPacket(t *testing.T) {
	cases := []Packet{
		{ID: 7, Type: TypeResponse, Body: ""},
		{ID: 42, Type: TypeCommand, Body: "setblock 0 0 0 stone"},
		{ID: -1, Type: TypeAuthResponse, Body: ""},
		{ID: 3, Type: TypeResponse, Body: "日本語の本文"},
	}
	var buf bytes.Buffer
	for _, p := range cases {
		buf.Write(p.Encode())
	}
	for _, want := range cases {
		got, err := ReadPacket(&buf)
		if err != nil {
			t.Fatalf("ReadPacket: %v", err)
		}
		if got != want {
			t.Fatalf("ReadPacket: got %+v, want %+v", got, want)
		}
	}
	if _, err := ReadPacket(&buf); !errors.Is(err, io.EOF) {
		t.Fatalf("空の入力: got %v, want io.EOF", err)
	}
}

func TestReadPacketBroken(t *testing.T) {
	// 長さが短すぎる
	if _, err := ReadPacket(bytes.NewReader([]byte{0x04, 0, 0, 0, 1, 0, 0, 0})); err == nil {
		t.Fatal("短すぎる長さでエラーにならない")
	}
	// 長さが大きすぎる
	if _, err := ReadPacket(bytes.NewReader([]byte{0xff, 0xff, 0xff, 0x00})); !errors.Is(err, ErrPacketTooLarge) {
		t.Fatalf("大きすぎる長さ: got %v", err)
	}
	// 途中で切れている
	full := Packet{ID: 1, Type: 0, Body: "abc"}.Encode()
	if _, err := ReadPacket(bytes.NewReader(full[:len(full)-3])); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("途中で切れた入力: got %v, want ErrUnexpectedEOF", err)
	}
}

// fakeServer は Minecraft の RCON の動きをまねるテスト用のサーバー。
//   - 1 回の読み込みから 1 つのパケットしか取り出さない（残りは捨てる）
//   - 応答は 4096 バイトごとに分けて送る
//   - 不明な種類には "Unknown request <16進>" を返す
type fakeServer struct {
	ln          net.Listener
	password    string
	handler     func(cmd string) string
	emptyBefore atomic.Bool // 認証の応答の前に空の応答を送る（Source RCON の動き）
}

func startFake(t *testing.T, password string, handler func(string) string) *fakeServer {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	s := &fakeServer{ln: ln, password: password, handler: handler}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go s.serve(conn)
		}
	}()
	return s
}

func (s *fakeServer) addr() string { return s.ln.Addr().String() }

func (s *fakeServer) serve(conn net.Conn) {
	defer conn.Close()
	authed := false
	buf := make([]byte, 1460)
	for {
		n, err := conn.Read(buf)
		if err != nil {
			return
		}
		p, err := ReadPacket(bytes.NewReader(buf[:n]))
		if err != nil {
			return
		}
		switch p.Type {
		case TypeAuth:
			if s.emptyBefore.Load() {
				conn.Write(Packet{ID: p.ID, Type: TypeResponse}.Encode())
			}
			if p.Body == s.password {
				authed = true
				conn.Write(Packet{ID: p.ID, Type: TypeAuthResponse}.Encode())
			} else {
				conn.Write(Packet{ID: -1, Type: TypeAuthResponse}.Encode())
			}
		case TypeCommand:
			if !authed {
				conn.Write(Packet{ID: -1, Type: TypeAuthResponse}.Encode())
				continue
			}
			resp := s.handler(p.Body)
			for {
				chunk := resp
				if len(chunk) > 4096 {
					chunk = chunk[:4096]
				}
				conn.Write(Packet{ID: p.ID, Type: TypeResponse, Body: chunk}.Encode())
				resp = resp[len(chunk):]
				if resp == "" {
					break
				}
			}
		default:
			conn.Write(Packet{ID: p.ID, Type: TypeResponse, Body: fmt.Sprintf("Unknown request %x", p.Type)}.Encode())
		}
	}
}

func ctxT(t *testing.T) context.Context {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	return ctx
}

func TestDialAndCommand(t *testing.T) {
	s := startFake(t, "secret", func(cmd string) string { return "echo:" + cmd })
	c, err := Dial(ctxT(t), s.addr(), "secret")
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer c.Close()
	for _, cmd := range []string{"list", "setblock 1 2 3 stone", ""} {
		got, err := c.Command(ctxT(t), cmd)
		if err != nil {
			t.Fatalf("Command(%q): %v", cmd, err)
		}
		if want := "echo:" + cmd; got != want {
			t.Fatalf("Command(%q) = %q, want %q", cmd, got, want)
		}
	}
}

func TestAuthFailed(t *testing.T) {
	s := startFake(t, "secret", func(string) string { return "" })
	_, err := Dial(ctxT(t), s.addr(), "wrong")
	if !errors.Is(err, ErrAuthFailed) {
		t.Fatalf("Dial(wrong) err = %v, want ErrAuthFailed", err)
	}
}

func TestAuthEmptyResponseFirst(t *testing.T) {
	s := startFake(t, "secret", func(string) string { return "ok" })
	s.emptyBefore.Store(true)
	c, err := Dial(ctxT(t), s.addr(), "secret")
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer c.Close()
	if got, err := c.Command(ctxT(t), "x"); err != nil || got != "ok" {
		t.Fatalf("Command = %q, %v", got, err)
	}
}

// 4096 バイトを超える応答が、分割されて届いてもつながって返るか。
func TestLongResponse(t *testing.T) {
	long := strings.Repeat("0123456789", 1500) // 15000 バイト = 4 つに分割
	s := startFake(t, "pw", func(string) string { return long })
	c, err := Dial(ctxT(t), s.addr(), "pw")
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	got, err := c.Command(ctxT(t), "help")
	if err != nil {
		t.Fatal(err)
	}
	if got != long {
		t.Fatalf("長い応答の長さ: got %d, want %d", len(got), len(long))
	}
	// 続けて次のコマンドも正しく対応する
	got, err = c.Command(ctxT(t), "help")
	if err != nil || got != long {
		t.Fatalf("2 回目: len=%d err=%v", len(got), err)
	}
}

func TestCommandTooLong(t *testing.T) {
	s := startFake(t, "pw", func(cmd string) string { return cmd })
	c, err := Dial(ctxT(t), s.addr(), "pw")
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if _, err := c.Command(ctxT(t), strings.Repeat("a", MaxCommandLen+1)); !errors.Is(err, ErrPacketTooLarge) {
		t.Fatalf("長すぎるコマンド: got %v", err)
	}
	// ちょうど上限の長さは通る（1 回の読み込みに収まる）
	max := strings.Repeat("a", MaxCommandLen)
	if got, err := c.Command(ctxT(t), max); err != nil || got != max {
		t.Fatalf("上限の長さのコマンド: len=%d err=%v", len(got), err)
	}
}

// 応答が来ないとき、context の時間切れで戻り、以後その接続は使えない。
func TestCommandContextTimeout(t *testing.T) {
	block := make(chan struct{})
	t.Cleanup(func() { close(block) })
	s := startFake(t, "pw", func(cmd string) string {
		if cmd == "hang" {
			<-block
		}
		return "ok"
	})
	c, err := Dial(ctxT(t), s.addr(), "pw")
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err = c.Command(ctx, "hang")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want DeadlineExceeded", err)
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Fatalf("戻るまでに %v かかった", d)
	}
	if _, err := c.Command(ctxT(t), "list"); err == nil {
		t.Fatal("中断した接続が使えてしまう")
	}
}

// 締め切りのない context を中止したときも戻る。
func TestCommandContextCancel(t *testing.T) {
	block := make(chan struct{})
	t.Cleanup(func() { close(block) })
	s := startFake(t, "pw", func(string) string { <-block; return "" })
	c, err := Dial(ctxT(t), s.addr(), "pw")
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(100*time.Millisecond, cancel)
	if _, err := c.Command(ctx, "hang"); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want Canceled", err)
	}
}

// 複数の goroutine から同時に呼んでも、応答が取り違えられない。
func TestConcurrentCommands(t *testing.T) {
	s := startFake(t, "pw", func(cmd string) string { return "r-" + cmd })
	c, err := Dial(ctxT(t), s.addr(), "pw")
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	var wg sync.WaitGroup
	errs := make(chan error, 200)
	for g := range 10 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range 20 {
				cmd := fmt.Sprintf("g%d-%d", g, i)
				got, err := c.Command(ctxT(t), cmd)
				if err != nil {
					errs <- err
					return
				}
				if got != "r-"+cmd {
					errs <- fmt.Errorf("%s: got %q", cmd, got)
				}
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
}

func TestClosed(t *testing.T) {
	s := startFake(t, "pw", func(string) string { return "" })
	c, err := Dial(ctxT(t), s.addr(), "pw")
	if err != nil {
		t.Fatal(err)
	}
	c.Close()
	if _, err := c.Command(ctxT(t), "list"); !errors.Is(err, ErrClosed) {
		t.Fatalf("閉じたあとの Command: got %v, want ErrClosed", err)
	}
}
