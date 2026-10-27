//go:build integration

// 単位 3（ログイン）を本物のサーバー（Docker の Paper 1.21.4）で確かめる確認用のテスト。
// 普段の go test では動かない。手で立てたテスト用サーバーに繋ぐ:
//
//	MCDRIVER_ADDR=127.0.0.1:25665 MCDRIVER_RCON=127.0.0.1:25675 MCDRIVER_RCON_PASS=... \
//	  go test -tags integration -run TestCheck3 -v -timeout 110s .
//
// ドライバとサーバーのあいだに中継（プロキシ）を挟み、受け取ったパケットを ID ごとに数える。
// ドライバのコードは変えない。
package mcdriver

import (
	"bufio"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Satoru314/go-mcdriver/data"
	"github.com/Satoru314/go-mcdriver/internal/protocol"
)

func TestCheck3Login(t *testing.T) {
	addr := check3Env(t, "MCDRIVER_ADDR")
	rconAddr := check3Env(t, "MCDRIVER_RCON")
	rconPass := check3Env(t, "MCDRIVER_RCON_PASS")
	const name = "check3bot"

	rc, err := check3DialRCON(rconAddr, rconPass)
	if err != nil {
		t.Fatalf("RCON: %v", err)
	}
	defer rc.close()

	px := newCheck3Proxy(t, addr)
	defer px.report(t)

	// a. Connect が 10 秒以内に戻る
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	start := time.Now()
	cl, err := Connect(ctx, Config{Addr: px.addr(), Username: name})
	cancel()
	if err != nil {
		t.Fatalf("a. Connect: %v（%v）", err, time.Since(start))
	}
	defer cl.Close()
	t.Logf("a. Connect が戻るまで %v", time.Since(start))

	// b. RCON の list に出る
	out, err := rc.command("list")
	if err != nil {
		t.Fatalf("b. RCON list: %v", err)
	}
	t.Logf("b. list: %q", out)
	if !strings.Contains(out, name) {
		t.Errorf("b. list に %s が出ない: %q", name, out)
	}

	// c. 60 秒たっても切られない
	select {
	case <-cl.Done():
		t.Fatalf("c. 60 秒のあいだに切断された: %v", cl.Err())
	case <-time.After(60 * time.Second):
	}
	if err := cl.Err(); err != nil {
		t.Errorf("c. Err() が nil でない: %v", err)
	}

	// d. 位置の強制修正の数（ログインの 1 回だけが期待）
	cl.mu.Lock()
	tp, rcf := cl.teleports, cl.reconfigures
	pos := [3]float64{cl.x, cl.y, cl.z}
	cl.mu.Unlock()
	t.Logf("d. 60 秒後: 強制修正 %d 回、設定に戻った数 %d、位置 %v", tp, rcf, pos)
	if tp != 1 {
		t.Errorf("d. 強制修正の数 = %d、期待は 1", tp)
	}
	px.report(t)

	// e. tp しても切られない（MCDRIVER_CHECK3_SKIP_TP=1 なら飛ばして f だけ確かめる）
	if os.Getenv("MCDRIVER_CHECK3_SKIP_TP") != "" {
		t.Log("e. 飛ばした（MCDRIVER_CHECK3_SKIP_TP）")
	} else {
		check3Teleport(t, rc, cl, name)
	}

	// f. kick の理由が Err() に入る
	check3Kick(t, rc, cl, name)
}

func check3Teleport(t *testing.T, rc *check3RCON, cl *Client, name string) {
	out, err := rc.command("tp " + name + " 10 100 10")
	if err != nil {
		t.Fatalf("e. RCON tp: %v", err)
	}
	t.Logf("e. tp: %q", out)
	select {
	case <-cl.Done():
		t.Fatalf("e. tp のあとに切断された: %v", cl.Err())
	case <-time.After(5 * time.Second):
	}
	cl.mu.Lock()
	tp := cl.teleports
	pos := [3]float64{cl.x, cl.y, cl.z}
	cl.mu.Unlock()
	t.Logf("e. tp の 5 秒後: 強制修正 %d 回、位置 %v", tp, pos)
}

func check3Kick(t *testing.T, rc *check3RCON, cl *Client, name string) {
	out, err := rc.command("kick " + name + " test")
	if err != nil {
		t.Fatalf("f. RCON kick: %v", err)
	}
	t.Logf("f. kick: %q", out)
	select {
	case <-cl.Done():
	case <-time.After(10 * time.Second):
		t.Fatalf("f. kick のあと 10 秒たっても Done が閉じない")
	}
	kerr := cl.Err()
	t.Logf("f. Err() = %v", kerr)
	var de *DisconnectError
	if !errors.As(kerr, &de) {
		t.Errorf("f. Err() が *DisconnectError でない: %T %v", kerr, kerr)
	} else if !strings.Contains(de.Reason, "test") {
		t.Errorf("f. 理由に test が無い: %q", de.Reason)
	}
}

func check3Env(t *testing.T, key string) string {
	v := os.Getenv(key)
	if v == "" {
		t.Skipf("%s が無いので飛ばす", key)
	}
	return v
}

// ---- RCON（確認用の最小限のクライアント） ----

type check3RCON struct {
	c  net.Conn
	r  *bufio.Reader
	id int32
}

func check3DialRCON(addr, pass string) (*check3RCON, error) {
	c, err := net.DialTimeout("tcp", addr, 5*time.Second)
	if err != nil {
		return nil, err
	}
	rc := &check3RCON{c: c, r: bufio.NewReader(c)}
	id, _, err := rc.roundTrip(3, pass)
	if err != nil {
		c.Close()
		return nil, err
	}
	if id == -1 {
		c.Close()
		return nil, errors.New("RCON の認証に失敗")
	}
	return rc, nil
}

func (rc *check3RCON) close() { rc.c.Close() }

func (rc *check3RCON) command(cmd string) (string, error) {
	_, body, err := rc.roundTrip(2, cmd)
	return body, err
}

// roundTrip は 1 つ送って 1 つ受け取る（今回のコマンドは応答が 1 つに収まる）。
func (rc *check3RCON) roundTrip(typ int32, body string) (int32, string, error) {
	rc.id++
	rc.c.SetDeadline(time.Now().Add(10 * time.Second))
	b := make([]byte, 12, 14+len(body))
	binary.LittleEndian.PutUint32(b[0:], uint32(10+len(body)))
	binary.LittleEndian.PutUint32(b[4:], uint32(rc.id))
	binary.LittleEndian.PutUint32(b[8:], uint32(typ))
	b = append(b, body...)
	b = append(b, 0, 0)
	if _, err := rc.c.Write(b); err != nil {
		return 0, "", err
	}
	for {
		var hdr [12]byte
		if _, err := io.ReadFull(rc.r, hdr[:]); err != nil {
			return 0, "", err
		}
		n := int(binary.LittleEndian.Uint32(hdr[0:]))
		id := int32(binary.LittleEndian.Uint32(hdr[4:]))
		rtyp := int32(binary.LittleEndian.Uint32(hdr[8:]))
		rest := make([]byte, n-8)
		if _, err := io.ReadFull(rc.r, rest); err != nil {
			return 0, "", err
		}
		// 認証のときは、先に空の RESPONSE_VALUE が来ることがあるので飛ばす
		if typ == 3 && rtyp != 2 {
			continue
		}
		return id, string(rest[:len(rest)-2]), nil
	}
}

// ---- パケットを数える中継 ----

type check3Proxy struct {
	ln        net.Listener
	target    string
	threshold atomic.Int32 // 圧縮の閾値（-1 は圧縮なし）

	mu     sync.Mutex
	counts map[string]int // "向き 段階 名前(0xID)" ごとの数
	errs   []string
	once   sync.Once
}

func newCheck3Proxy(t *testing.T, target string) *check3Proxy {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	p := &check3Proxy{ln: ln, target: target, counts: map[string]int{}}
	p.threshold.Store(-1)
	t.Cleanup(func() { ln.Close() })
	go func() {
		cc, err := ln.Accept()
		if err != nil {
			return
		}
		sc, err := net.DialTimeout("tcp", target, 5*time.Second)
		if err != nil {
			p.addErr("サーバーに繋がらない: " + err.Error())
			cc.Close()
			return
		}
		go p.pipe(sc, cc, "clientbound")
		go p.pipe(cc, sc, "serverbound")
	}()
	return p
}

func (p *check3Proxy) addr() string { return p.ln.Addr().String() }

func (p *check3Proxy) addErr(s string) {
	p.mu.Lock()
	p.errs = append(p.errs, s)
	p.mu.Unlock()
}

// pipe は src から枠を 1 つずつ読み、そのまま dst に書き、中身を数える。
func (p *check3Proxy) pipe(src, dst net.Conn, dir string) {
	defer src.Close()
	defer dst.Close()
	r := bufio.NewReader(src)
	state := data.StateHandshake
	if dir == "clientbound" {
		state = data.StateLogin
	}
	for {
		n, err := protocol.ReadVarIntFrom(r)
		if err != nil {
			return
		}
		frame := make([]byte, n)
		if _, err := io.ReadFull(r, frame); err != nil {
			return
		}
		pk, derr := protocol.DecodeFrame(frame, int(p.threshold.Load()))
		// 圧縮の切り替えは、転送する前に反映する（次の枠から効く）
		if derr == nil && dir == "clientbound" && state == data.StateLogin && pk.ID == data.LoginClientboundLoginCompression {
			th, _, err := protocol.DecodeVarInt(pk.Data)
			if err == nil {
				p.threshold.Store(th)
			}
		}
		if _, err := dst.Write(append(protocol.AppendVarInt(nil, n), frame...)); err != nil {
			return
		}
		if derr != nil {
			p.addErr(fmt.Sprintf("%s %s: 枠を読めない: %v", dir, state, derr))
			continue
		}
		name := fmt.Sprintf("0x%02X", pk.ID)
		if pkt, ok := data.PacketByID(state, dir, pk.ID); ok {
			name = fmt.Sprintf("%s(0x%02X)", pkt.Name, pk.ID)
		}
		p.mu.Lock()
		p.counts[dir+" "+state+" "+name]++
		p.mu.Unlock()
		state = check3NextState(dir, state, pk)
	}
}

// check3NextState はパケットによる段階の切り替えを返す。
func check3NextState(dir, state string, pk protocol.Packet) string {
	switch {
	case dir == "serverbound" && state == data.StateHandshake:
		// intention の最後の VarInt が次の段階（2 = login）
		if len(pk.Data) > 0 && pk.Data[len(pk.Data)-1] == 1 {
			return data.StateStatus
		}
		return data.StateLogin
	case dir == "serverbound" && state == data.StateLogin && pk.ID == data.LoginServerboundLoginAcknowledged:
		return data.StateConfiguration
	case dir == "clientbound" && state == data.StateLogin && pk.ID == data.LoginClientboundLoginFinished:
		return data.StateConfiguration
	case dir == "serverbound" && state == data.StateConfiguration && pk.ID == data.ConfigurationServerboundFinishConfiguration:
		return data.StatePlay
	case dir == "clientbound" && state == data.StateConfiguration && pk.ID == data.ConfigurationClientboundFinishConfiguration:
		return data.StatePlay
	case dir == "serverbound" && state == data.StatePlay && pk.ID == data.PlayServerboundConfigurationAcknowledged:
		return data.StateConfiguration
	case dir == "clientbound" && state == data.StatePlay && pk.ID == data.PlayClientboundStartConfiguration:
		return data.StateConfiguration
	}
	return state
}

// report は数えたものをログに書く。
func (p *check3Proxy) report(t *testing.T) {
	p.mu.Lock()
	defer p.mu.Unlock()
	keys := make([]string, 0, len(p.counts))
	for k := range p.counts {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, k := range keys {
		fmt.Fprintf(&b, "  %-70s %d\n", k, p.counts[k])
	}
	t.Logf("中継で数えたパケット:\n%s", b.String())
	for _, e := range p.errs {
		t.Logf("中継のエラー: %s", e)
	}
}
