package mcdriver

// テスト用の偽のサーバー。テストの goroutine がサーバー役になり、仕様どおりのパケットを返す。
// Connect は別の goroutine で動かす（Connect は Login (play) を受け取るまで戻らないため）。

import (
	"bufio"
	"context"
	"net"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/Satoru314/go-mcdriver/data"
	"github.com/Satoru314/go-mcdriver/internal/conn"
	"github.com/Satoru314/go-mcdriver/internal/nbt"
	"github.com/Satoru314/go-mcdriver/internal/packets"
	"github.com/Satoru314/go-mcdriver/internal/protocol"
)

// fakeServer は 1 本の接続を受けるサーバー。
type fakeServer struct {
	t  *testing.T
	ln net.Listener
	nc net.Conn
	br *bufio.Reader
	th int // 圧縮の閾値
}

func newFakeServer(t *testing.T) *fakeServer {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	s := &fakeServer{t: t, ln: ln, th: protocol.NoCompression}
	t.Cleanup(func() {
		ln.Close()
		if s.nc != nil {
			s.nc.Close()
		}
	})
	return s
}

func (s *fakeServer) addr() string { return s.ln.Addr().String() }

// accept はクライアントの接続を待つ。
func (s *fakeServer) accept() {
	s.t.Helper()
	type res struct {
		nc  net.Conn
		err error
	}
	ch := make(chan res, 1)
	go func() {
		nc, err := s.ln.Accept()
		ch <- res{nc, err}
	}()
	select {
	case r := <-ch:
		if r.err != nil {
			s.t.Fatal(r.err)
		}
		s.nc = r.nc
		s.br = bufio.NewReader(r.nc)
	case <-time.After(5 * time.Second):
		// Connect が先にエラーで戻っていれば、その理由を出す（接続が来ない原因を分けるため）
		if err, ok := connectErrs.Load(s.addr()); ok {
			s.t.Fatalf("接続が来ない（Connect は先にエラーで戻った: %v）", err)
		}
		s.t.Fatal("接続が来ない（Connect はまだ戻っていない）")
	}
}

// read はパケットを 1 つ読む。
func (s *fakeServer) read() protocol.Packet {
	s.t.Helper()
	s.nc.SetReadDeadline(time.Now().Add(5 * time.Second))
	p, err := protocol.ReadPacket(s.br, s.th)
	if err != nil {
		s.t.Fatalf("サーバーが読む: %v", err)
	}
	return p
}

// tickPackets は毎ティック送られるパケット（expect で読み飛ばす）。
var tickPackets = map[int32]bool{
	data.PlayServerboundMovePlayerPosRot:     true,
	data.PlayServerboundMovePlayerPos:        true,
	data.PlayServerboundMovePlayerRot:        true,
	data.PlayServerboundMovePlayerStatusOnly: true,
	data.PlayServerboundPlayerInput:          true,
	data.PlayServerboundPlayerCommand:        true,
	data.PlayServerboundClientTickEnd:        true,
}

// expect は id のパケットが来るまで読む。skip にあるものは読み飛ばし、それ以外が来たら失敗。
func (s *fakeServer) expect(id int32, skip map[int32]bool) *protocol.Decoder {
	s.t.Helper()
	for {
		p := s.read()
		if p.ID == id {
			return protocol.NewDecoder(p.Data)
		}
		if !skip[p.ID] {
			s.t.Fatalf("パケット 0x%02x を待っていたが 0x%02x が来た（%d バイト）", id, p.ID, len(p.Data))
		}
	}
}

// send はパケットを送る。
func (s *fakeServer) send(id int32, build func(e *protocol.Encoder)) {
	s.t.Helper()
	e := protocol.NewEncoder()
	if build != nil {
		build(e)
	}
	s.nc.SetWriteDeadline(time.Now().Add(5 * time.Second))
	if err := protocol.WritePacket(s.nc, protocol.Packet{ID: id, Data: e.Bytes()}, s.th); err != nil {
		s.t.Fatalf("サーバーが送る: %v", err)
	}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

// connectErrs は connectAsync の Connect がエラーで戻ったときの理由（Addr → error）。accept の時間切れの説明に使う。
var connectErrs sync.Map

// connectAsync は別の goroutine で Connect する。
func connectAsync(ctx context.Context, cfg Config) <-chan connectResult {
	ch := make(chan connectResult, 1)
	go func() {
		c, err := Connect(ctx, cfg)
		if err != nil {
			connectErrs.Store(cfg.Addr, err)
		}
		ch <- connectResult{c, err}
	}()
	return ch
}

type connectResult struct {
	c   *Client
	err error
}

// wait は Connect の結果を待つ。
func wait(t *testing.T, ch <-chan connectResult) connectResult {
	t.Helper()
	select {
	case r := <-ch:
		if r.c != nil {
			t.Cleanup(func() { r.c.Close() })
		}
		return r
	case <-time.After(5 * time.Second):
		t.Fatal("Connect が戻らない")
		return connectResult{}
	}
}

// login はハンドシェイクから Login Acknowledged までを、サーバー側から確かめながら進める。
// threshold が負なら Set Compression を送らない。
func (s *fakeServer) login(username string, threshold int) {
	t := s.t
	t.Helper()
	s.accept()

	// Handshake
	d := s.expect(data.HandshakeServerboundIntention, nil)
	ver, err := d.ReadVarInt()
	must(t, err)
	host, err := d.ReadString(255)
	must(t, err)
	port, err := d.ReadUShort()
	must(t, err)
	next, err := d.ReadVarInt()
	must(t, err)
	_, wantPort, _ := net.SplitHostPort(s.addr())
	if ver != data.ProtocolVersion || host != "127.0.0.1" || next != 2 || strconv.Itoa(int(port)) != wantPort {
		t.Fatalf("Handshake: version %d, host %q, port %d, next %d", ver, host, port, next)
	}

	// Login Start
	d = s.expect(data.LoginServerboundHello, nil)
	name, err := d.ReadString(16)
	must(t, err)
	uuid, err := d.ReadUUID()
	must(t, err)
	if name != username || uuid != conn.OfflineUUID(username) {
		t.Fatalf("Login Start: name %q, uuid %s", name, uuid)
	}

	if threshold >= 0 {
		s.send(data.LoginClientboundLoginCompression, func(e *protocol.Encoder) { e.WriteVarInt(int32(threshold)) })
		s.th = threshold
	}
	s.send(data.LoginClientboundLoginFinished, func(e *protocol.Encoder) {
		e.WriteUUID(uuid)
		must(t, e.WriteString(name, 16))
		e.WriteVarInt(0) // properties なし
	})
	s.expect(data.LoginServerboundLoginAcknowledged, nil)
}

// configure は設定の段階を、サーバー側から確かめながら進める。first なら Client Information を待つ。
func (s *fakeServer) configure(first bool) {
	t := s.t
	t.Helper()
	if first {
		d := s.expect(data.ConfigurationServerboundClientInformation, nil)
		locale, err := d.ReadString(16)
		must(t, err)
		vd, err := d.ReadInt8()
		must(t, err)
		if locale != "en_us" || vd != 8 {
			t.Fatalf("Client Information: locale %q, view distance %d", locale, vd)
		}
		d.ReadVarInt()          // chat mode
		d.ReadBool()            // colors
		d.ReadUByte()           // skin
		d.ReadVarInt()          // main hand
		d.ReadBool()            // text filtering
		d.ReadBool()            // server listings
		_, err = d.ReadVarInt() // particle status
		must(t, err)
		if d.Remaining() != 0 {
			t.Fatalf("Client Information に余分 %d バイト", d.Remaining())
		}
		d = s.expect(data.ConfigurationServerboundCustomPayload, nil)
		ch, err := d.ReadIdentifier()
		must(t, err)
		if ch != "minecraft:brand" {
			t.Fatalf("Plugin Message のチャネル %q", ch)
		}
	}

	// Feature Flags と Known Packs
	s.send(data.ConfigurationClientboundUpdateEnabledFeatures, func(e *protocol.Encoder) {
		e.WriteVarInt(1)
		must(t, e.WriteIdentifier("minecraft:vanilla"))
	})
	s.send(data.ConfigurationClientboundSelectKnownPacks, func(e *protocol.Encoder) {
		e.WriteVarInt(1)
		for _, str := range []string{"minecraft", "core", "1.21.4"} {
			must(t, e.WriteString(str, 32767))
		}
	})
	d := s.expect(data.ConfigurationServerboundSelectKnownPacks, nil)
	n, err := d.ReadVarInt()
	must(t, err)
	if n != 0 {
		t.Fatalf("Serverbound Known Packs: %d 個、期待 0", n)
	}

	// Registry Data（閾値より大きくなるように長い値を入れる）
	s.send(data.ConfigurationClientboundRegistryData, func(e *protocol.Encoder) {
		must(t, e.WriteIdentifier("minecraft:dimension_type"))
		e.WriteVarInt(2)
		must(t, e.WriteIdentifier("minecraft:overworld"))
		e.WriteBool(true)
		must(t, nbt.WriteNetwork(e, map[string]any{
			"height": int32(384), "min_y": int32(-64),
			"effects": "minecraft:overworld", "padding": string(make([]byte, 600)),
		}))
		must(t, e.WriteIdentifier("minecraft:the_nether"))
		e.WriteBool(false)
	})
	// Update Tags
	s.send(data.ConfigurationClientboundUpdateTags, func(e *protocol.Encoder) {
		e.WriteVarInt(1)
		must(t, e.WriteIdentifier("minecraft:block"))
		e.WriteVarInt(1)
		must(t, e.WriteIdentifier("minecraft:mineable/pickaxe"))
		e.WriteVarInt(3)
		e.WriteVarInt(1)
		e.WriteVarInt(2)
		e.WriteVarInt(300)
	})
	// 知らないパケット（Server Links、中身はでたらめ）は読み飛ばされる
	s.send(data.ConfigurationClientboundServerLinks, func(e *protocol.Encoder) { e.WriteBytes([]byte{0xff, 0xfe, 0x01}) })

	// Keep Alive と Ping
	s.send(data.ConfigurationClientboundKeepAlive, func(e *protocol.Encoder) { e.WriteLong(42) })
	d = s.expect(data.ConfigurationServerboundKeepAlive, nil)
	if v, err := d.ReadLong(); err != nil || v != 42 {
		t.Fatalf("Keep Alive (configuration) の応答 %d %v、期待 42", v, err)
	}
	s.send(data.ConfigurationClientboundPing, func(e *protocol.Encoder) { e.WriteInt(-7) })
	d = s.expect(data.ConfigurationServerboundPong, nil)
	if v, err := d.ReadInt(); err != nil || v != -7 {
		t.Fatalf("Pong (configuration) %d %v、期待 -7", v, err)
	}

	s.send(data.ConfigurationClientboundFinishConfiguration, nil)
	s.expect(data.ConfigurationServerboundFinishConfiguration, nil)
}

// testLoginPlay は偽のサーバーが送る Login (play)。
var testLoginPlay = packets.LoginPlay{
	EntityID:            1234,
	DimensionNames:      []string{"minecraft:overworld", "minecraft:the_nether", "minecraft:the_end"},
	MaxPlayers:          20,
	ViewDistance:        10,
	SimulationDistance:  10,
	EnableRespawnScreen: true,
	DimensionType:       0,
	DimensionName:       "minecraft:overworld",
	HashedSeed:          -123456789,
	GameMode:            1,
	PreviousGameMode:    -1,
	IsFlat:              true,
	PortalCooldown:      0,
	SeaLevel:            -63,
}

// enterPlay は Login (play) を送る。
func (s *fakeServer) enterPlay() {
	s.t.Helper()
	// 知らないパケット（誰も登録していない Boss Event、中身はでたらめ）は読み飛ばされる
	s.send(data.PlayClientboundBossEvent, func(e *protocol.Encoder) { e.WriteBytes([]byte{1, 2, 3, 4, 5}) })
	s.send(data.PlayClientboundLogin, func(e *protocol.Encoder) { must(s.t, packets.WriteLoginPlay(e, testLoginPlay)) })
}

// teleport は Synchronize Player Position を送り、Confirm Teleportation と位置が返ってくることを確かめる。
func (s *fakeServer) teleport(pp packets.PlayerPosition, wantX, wantY, wantZ float64, wantLoaded bool) {
	t := s.t
	t.Helper()
	s.send(data.PlayClientboundPlayerPosition, func(e *protocol.Encoder) { packets.WritePlayerPosition(e, pp) })
	d := s.expect(data.PlayServerboundAcceptTeleportation, tickPackets)
	id, err := d.ReadVarInt()
	must(t, err)
	if id != pp.TeleportID {
		t.Fatalf("Confirm Teleportation の ID %d、期待 %d", id, pp.TeleportID)
	}
	d = s.expect(data.PlayServerboundMovePlayerPosRot, nil)
	m, err := packets.ReadMovePlayerPosRot(d)
	must(t, err)
	if m.X != wantX || m.Y != wantY || m.Z != wantZ {
		t.Fatalf("送られた位置 (%v, %v, %v)、期待 (%v, %v, %v)", m.X, m.Y, m.Z, wantX, wantY, wantZ)
	}
	if wantLoaded {
		s.expect(data.PlayServerboundPlayerLoaded, nil)
	}
}
