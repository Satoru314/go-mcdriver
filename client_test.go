package mcdriver

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Satoru314/go-mcdriver/data"
	"github.com/Satoru314/go-mcdriver/internal/nbt"
	"github.com/Satoru314/go-mcdriver/internal/packets"
	"github.com/Satoru314/go-mcdriver/internal/protocol"
)

// ログインから Play に入り、位置の強制修正への応答、毎ティックの位置、
// Play の Keep Alive と Ping への応答までの流れ。圧縮なし・あり（全部・大きいものだけ）で確かめる。
func TestLoginToPlay(t *testing.T) {
	for _, tc := range []struct {
		name      string
		threshold int
	}{
		{"圧縮なし", -1},
		{"全部を圧縮", 0},
		{"256バイト以上を圧縮", 256},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := newFakeServer(t)
			ch := connectAsync(context.Background(), Config{Addr: s.addr(), Username: "bot1"})
			s.login("bot1", tc.threshold)
			s.configure(true)
			s.enterPlay()
			r := wait(t, ch)
			if r.err != nil {
				t.Fatalf("Connect: %v", r.err)
			}
			c := r.c

			// 受け取ったものが保管されている
			c.mu.Lock()
			lp := c.login
			c.mu.Unlock()
			if lp.EntityID != 1234 || lp.DimensionName != "minecraft:overworld" || lp.SeaLevel != -63 || !lp.IsFlat || len(lp.DimensionNames) != 3 {
				t.Errorf("Login (play) %+v", lp)
			}
			if c.profile.Name != "bot1" {
				t.Errorf("Profile %+v", c.profile)
			}
			dims := c.reg.Entries("minecraft:dimension_type")
			if len(dims) != 2 || dims[0].ID != "minecraft:overworld" || dims[1].Data != nil {
				t.Errorf("Registry Data %+v", dims)
			} else if m, ok := dims[0].Data.(map[string]any); !ok || m["height"] != int32(384) {
				t.Errorf("Registry Data の NBT %v", dims[0].Data)
			}
			if ids, ok := c.reg.Tag("minecraft:block", "minecraft:mineable/pickaxe"); !ok || len(ids) != 3 || ids[2] != 300 {
				t.Errorf("Update Tags %v %v", ids, ok)
			}
			if f := c.reg.Features(); len(f) != 1 || f[0] != "minecraft:vanilla" {
				t.Errorf("Feature Flags %v", f)
			}

			// 位置を受け取る前は、位置を送らない
			s.nc.SetReadDeadline(time.Now().Add(200 * time.Millisecond))
			if p, err := protocol.ReadPacket(s.br, s.th); err == nil {
				t.Fatalf("位置を受け取る前にパケット 0x%02x が来た", p.ID)
			}

			// 位置の強制修正（絶対）→ 確認、位置、Player Loaded
			s.teleport(packets.PlayerPosition{TeleportID: 5, X: 1.5, Y: 64, Z: -2.5, Yaw: 90, Pitch: 10}, 1.5, 64, -2.5, true)
			// 相対（x と y を足す）。Player Loaded は 2 回目は送らない
			s.teleport(packets.PlayerPosition{TeleportID: 6, X: 1, Y: 0.5, Z: 8, Flags: packets.RelX | packets.RelY}, 2.5, 64.5, 8, false)

			// 毎ティック、同じ位置が送られる
			start := time.Now()
			moves := 0
			for moves < 5 {
				d := s.expect(data.PlayServerboundMovePlayerPosRot, map[int32]bool{data.PlayServerboundClientTickEnd: true})
				m, err := packets.ReadMovePlayerPosRot(d)
				must(t, err)
				if m.X != 2.5 || m.Y != 64.5 || m.Z != 8 || m.Flags&packets.MoveOnGround == 0 {
					t.Fatalf("毎ティックの位置 %+v", m)
				}
				moves++
			}
			if el := time.Since(start); el > time.Second {
				t.Errorf("5 ティックに %v かかった", el)
			}

			// Play の Keep Alive と Ping
			s.send(data.PlayClientboundKeepAlive, func(e *protocol.Encoder) { e.WriteLong(1234567890123) })
			d := s.expect(data.PlayServerboundKeepAlive, tickPackets)
			if v, err := d.ReadLong(); err != nil || v != 1234567890123 {
				t.Fatalf("Keep Alive (play) の応答 %d %v", v, err)
			}
			s.send(data.PlayClientboundPing, func(e *protocol.Encoder) { e.WriteInt(99) })
			d = s.expect(data.PlayServerboundPong, tickPackets)
			if v, err := d.ReadInt(); err != nil || v != 99 {
				t.Fatalf("Pong (play) %d %v", v, err)
			}
			// Chunk Batch Finished への応答
			s.send(data.PlayClientboundChunkBatchFinished, func(e *protocol.Encoder) { e.WriteVarInt(9) })
			d = s.expect(data.PlayServerboundChunkBatchReceived, tickPackets)
			if v, err := d.ReadFloat(); err != nil || v <= 0 {
				t.Fatalf("Chunk Batch Received %v %v", v, err)
			}

			select {
			case <-c.Done():
				t.Fatalf("切れた: %v", c.Err())
			default:
			}
			if c.Err() != nil {
				t.Errorf("Err = %v、期待 nil", c.Err())
			}
		})
	}
}

// Play の段階で Disconnect を受け取ると、Done が閉じ、Err で理由が読めること。
func TestDisconnectPlay(t *testing.T) {
	s := newFakeServer(t)
	ch := connectAsync(context.Background(), Config{Addr: s.addr(), Username: "bot2"})
	s.login("bot2", 256)
	s.configure(true)
	s.enterPlay()
	r := wait(t, ch)
	if r.err != nil {
		t.Fatal(r.err)
	}
	s.send(data.PlayClientboundDisconnect, func(e *protocol.Encoder) {
		must(t, nbt.WriteNetwork(e, map[string]any{"text": "Kicked: ", "extra": []any{"test over"}}))
	})
	select {
	case <-r.c.Done():
	case <-time.After(3 * time.Second):
		t.Fatal("Done が閉じない")
	}
	var de *DisconnectError
	if !errors.As(r.c.Err(), &de) {
		t.Fatalf("Err = %v、期待 *DisconnectError", r.c.Err())
	}
	if de.Reason != "Kicked: test over" || de.State != data.StatePlay {
		t.Errorf("理由 %q（%s）", de.Reason, de.State)
	}
}

// ログインの段階の Disconnect（JSON）と、設定の段階の Disconnect（NBT の文字列）。
func TestDisconnectLoginAndConfig(t *testing.T) {
	t.Run("ログイン", func(t *testing.T) {
		s := newFakeServer(t)
		ch := connectAsync(context.Background(), Config{Addr: s.addr(), Username: "bot3"})
		s.accept()
		s.expect(data.HandshakeServerboundIntention, nil)
		s.expect(data.LoginServerboundHello, nil)
		s.send(data.LoginClientboundLoginDisconnect, func(e *protocol.Encoder) {
			must(t, e.WriteString(`{"text":"You are not white-listed on this server!"}`, 262144))
		})
		r := wait(t, ch)
		var de *DisconnectError
		if !errors.As(r.err, &de) || de.Reason != "You are not white-listed on this server!" || de.State != data.StateLogin {
			t.Fatalf("Connect のエラー %v", r.err)
		}
	})
	t.Run("設定", func(t *testing.T) {
		s := newFakeServer(t)
		ch := connectAsync(context.Background(), Config{Addr: s.addr(), Username: "bot4"})
		s.login("bot4", 0)
		s.send(data.ConfigurationClientboundDisconnect, func(e *protocol.Encoder) {
			must(t, nbt.WriteNetwork(e, "Server closed"))
		})
		r := wait(t, ch)
		var de *DisconnectError
		if !errors.As(r.err, &de) || de.Reason != "Server closed" || de.State != data.StateConfiguration {
			t.Fatalf("Connect のエラー %v", r.err)
		}
	})
}

// Encryption Request が来たら、エラーで切ること。
func TestEncryptionRequest(t *testing.T) {
	s := newFakeServer(t)
	ch := connectAsync(context.Background(), Config{Addr: s.addr(), Username: "bot5"})
	s.accept()
	s.expect(data.HandshakeServerboundIntention, nil)
	s.expect(data.LoginServerboundHello, nil)
	s.send(data.LoginClientboundHello, func(e *protocol.Encoder) {
		must(t, e.WriteString("", 20))
		must(t, e.WritePrefixedBytes([]byte{1, 2, 3}))
		must(t, e.WritePrefixedBytes([]byte{4, 5, 6, 7}))
		e.WriteBool(true)
	})
	r := wait(t, ch)
	if !errors.Is(r.err, ErrEncryption) {
		t.Fatalf("Connect のエラー %v、期待 ErrEncryption", r.err)
	}
}

// Play の段階で Start Configuration が来たら、設定の段階に戻り、また Play に入れること。
func TestReconfigure(t *testing.T) {
	s := newFakeServer(t)
	ch := connectAsync(context.Background(), Config{Addr: s.addr(), Username: "bot6"})
	s.login("bot6", 128)
	s.configure(true)
	s.enterPlay()
	r := wait(t, ch)
	if r.err != nil {
		t.Fatal(r.err)
	}
	s.teleport(packets.PlayerPosition{TeleportID: 1, X: 0.5, Y: -60, Z: 0.5}, 0.5, -60, 0.5, true)

	s.send(data.PlayClientboundStartConfiguration, nil)
	s.expect(data.PlayServerboundConfigurationAcknowledged, tickPackets)
	// 設定の段階では位置を送らない。次に来るのは Keep Alive の応答のはず
	time.Sleep(150 * time.Millisecond) // 3 ティック待つ
	s.send(data.ConfigurationClientboundKeepAlive, func(e *protocol.Encoder) { e.WriteLong(77) })
	p := s.read()
	if p.ID != data.ConfigurationServerboundKeepAlive {
		t.Fatalf("設定の段階で 0x%02x が来た（位置を送ってしまった？）", p.ID)
	}
	s.configure(false)
	s.enterPlay()
	s.teleport(packets.PlayerPosition{TeleportID: 2, X: 10, Y: -60, Z: 10}, 10, -60, 10, true)

	r.c.mu.Lock()
	n := r.c.reconfigures
	r.c.mu.Unlock()
	if n != 1 {
		t.Errorf("reconfigures = %d", n)
	}
	select {
	case <-r.c.Done():
		t.Fatalf("切れた: %v", r.c.Err())
	default:
	}
}

// ctx が終わると、ログインの途中でも Connect が戻ること。
func TestConnectContext(t *testing.T) {
	s := newFakeServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	start := time.Now()
	ch := connectAsync(ctx, Config{Addr: s.addr(), Username: "bot7"})
	s.accept()
	s.expect(data.HandshakeServerboundIntention, nil)
	s.expect(data.LoginServerboundHello, nil)
	// 何も返さない
	r := wait(t, ch)
	if !errors.Is(r.err, context.DeadlineExceeded) {
		t.Fatalf("Connect のエラー %v、期待 DeadlineExceeded", r.err)
	}
	if el := time.Since(start); el > 2*time.Second {
		t.Errorf("戻るまで %v", el)
	}
}

// Close すると Done が閉じ、Err が ErrClosed になり、サーバー側の接続も切れること。
func TestClose(t *testing.T) {
	s := newFakeServer(t)
	ch := connectAsync(context.Background(), Config{Addr: s.addr(), Username: "bot8"})
	s.login("bot8", -1)
	s.configure(true)
	s.enterPlay()
	r := wait(t, ch)
	if r.err != nil {
		t.Fatal(r.err)
	}
	if r.c.Err() != nil {
		t.Fatalf("Close の前の Err = %v", r.c.Err())
	}
	r.c.Close()
	r.c.Close()
	select {
	case <-r.c.Done():
	default:
		t.Fatal("Done が閉じていない")
	}
	if !errors.Is(r.c.Err(), ErrClosed) {
		t.Errorf("Err = %v、期待 ErrClosed", r.c.Err())
	}
	s.nc.SetReadDeadline(time.Now().Add(2 * time.Second))
	for {
		if _, err := protocol.ReadPacket(s.br, s.th); err != nil {
			if strings.Contains(err.Error(), "timeout") {
				t.Fatal("サーバー側の接続が切れない")
			}
			break
		}
	}
}

// Config の確かめ。
func TestConnectBadConfig(t *testing.T) {
	ctx := context.Background()
	if _, err := Connect(ctx, Config{Addr: "127.0.0.1:1", Username: ""}); err == nil {
		t.Error("空の名前でエラーにならない")
	}
	if _, err := Connect(ctx, Config{Addr: "127.0.0.1:1", Username: "abcdefghijklmnopq"}); err == nil {
		t.Error("17 文字の名前でエラーにならない")
	}
	if _, err := Connect(ctx, Config{Addr: "", Username: "bot"}); err == nil {
		t.Error("空の Addr でエラーにならない")
	}
	h, p, err := splitAddr("example.com")
	if err != nil || h != "example.com" || p != 25565 {
		t.Errorf("splitAddr(example.com) = %q %d %v", h, p, err)
	}
	h, p, err = splitAddr("[::1]:25570")
	if err != nil || h != "::1" || p != 25570 {
		t.Errorf("splitAddr([::1]:25570) = %q %d %v", h, p, err)
	}
}
