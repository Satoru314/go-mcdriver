//go:build integration

// 第一段階（単位 3 のログイン）の合格テスト。internal/testserver のテスト用サーバー（Docker）を使う。
//
//	sg docker -c 'go test -tags integration -run TestStage1 -v -timeout 5m .'
//
// 合格の条件:
//   - bot がログインし、Connect が 10 秒以内に戻る
//   - RCON の list に出る
//   - 60 秒たっても切られない
//   - 位置の強制修正は、ログインのときの 1 回だけ
//   - 地面の高さ（平らなワールドの地表の上。RCON で確かめた高さ）への tp で切られない
//   - kick の理由が Err() から読める
//
// 空中への tp は物理（単位 7）の合格条件にしたので、ここでは試さない。
package mcdriver

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Satoru314/go-mcdriver/internal/testserver"
)

func TestStage1Login(t *testing.T) {
	ts := testserver.Start(t)
	rc := ts.RCON(t)
	area := ts.NewArea(t)
	name := area.BotName
	defer func() {
		if t.Failed() {
			t.Logf("サーバーのログ（最後の 40 行）:\n%s", testserver.Logs(context.Background(), 40))
		}
	}()

	// ログインする。Connect が 10 秒以内に戻る
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	start := time.Now()
	cl, err := Connect(ctx, Config{Addr: ts.GameAddr, Username: name})
	cancel()
	if err != nil {
		t.Fatalf("Connect: %v（%v）", err, time.Since(start))
	}
	defer cl.Close()
	t.Logf("Connect が戻るまで %v", time.Since(start))

	// RCON の list に出る
	out := testserver.Command(t, rc, "list")
	t.Logf("list: %q", out)
	if !strings.Contains(out, name) {
		t.Errorf("list に %s が出ない: %q", name, out)
	}

	// 60 秒たっても切られない
	select {
	case <-cl.Done():
		t.Fatalf("60 秒のあいだに切断された: %v", cl.Err())
	case <-time.After(60 * time.Second):
	}
	if err := cl.Err(); err != nil {
		t.Errorf("60 秒後に Err() が nil でない: %v", err)
	}

	// 位置の強制修正は、ログインのときの 1 回だけ
	tp, pos := stage1Teleports(cl)
	t.Logf("60 秒後: 強制修正 %d 回、位置 %v", tp, pos)
	if tp != 1 {
		t.Errorf("強制修正の数 = %d、期待は 1（ログインのときだけ）", tp)
	}

	// 地面の高さへの tp で切られない。区画の地表を RCON で確かめてから tp する
	area.ForceLoad(t, rc)
	x, z := area.Center()
	if out := testserver.Command(t, rc, fmt.Sprintf("execute if block %d %d %d minecraft:grass_block", x, testserver.SurfaceY, z)); !strings.Contains(out, "passed") {
		t.Fatalf("y=%d が地表（草ブロック）でない: %q", testserver.SurfaceY, out)
	}
	if out := testserver.Command(t, rc, fmt.Sprintf("execute if block %d %d %d minecraft:air", x, testserver.StandY, z)); !strings.Contains(out, "passed") {
		t.Fatalf("y=%d が空気でない: %q", testserver.StandY, out)
	}
	out = testserver.Command(t, rc, fmt.Sprintf("tp %s %d.5 %d %d.5", name, x, testserver.StandY, z))
	t.Logf("tp: %q", out)
	if !strings.Contains(out, "Teleported") {
		t.Fatalf("tp の応答: %q", out)
	}
	// サーバーは足場のない状態が 4 秒続くと切るので、それより長く待つ
	select {
	case <-cl.Done():
		t.Fatalf("地面への tp のあとに切断された: %v", cl.Err())
	case <-time.After(8 * time.Second):
	}
	tp, pos = stage1Teleports(cl)
	t.Logf("tp の 8 秒後: 強制修正 %d 回、位置 %v", tp, pos)
	if tp != 2 {
		t.Errorf("tp のあとの強制修正の数 = %d、期待は 2（ログインと tp）", tp)
	}
	want := [3]float64{float64(x) + 0.5, float64(testserver.StandY), float64(z) + 0.5}
	if pos != want {
		t.Errorf("tp のあとの位置 = %v、期待は %v", pos, want)
	}

	// kick の理由が読める
	reason := "stage1 kick " + name
	out = testserver.Command(t, rc, "kick "+name+" "+reason)
	t.Logf("kick: %q", out)
	select {
	case <-cl.Done():
	case <-time.After(10 * time.Second):
		t.Fatalf("kick のあと 10 秒たっても Done が閉じない")
	}
	kerr := cl.Err()
	t.Logf("Err() = %v", kerr)
	var de *DisconnectError
	if !errors.As(kerr, &de) {
		t.Errorf("Err() が *DisconnectError でない: %T %v", kerr, kerr)
	} else if !strings.Contains(de.Reason, reason) {
		t.Errorf("kick の理由 = %q、%q を含むはず", de.Reason, reason)
	}
}

// stage1Teleports は受け取った位置の強制修正の数と、今の位置を返す。
func stage1Teleports(cl *Client) (int, [3]float64) {
	cl.mu.Lock()
	defer cl.mu.Unlock()
	return cl.teleports, [3]float64{cl.x, cl.y, cl.z}
}
