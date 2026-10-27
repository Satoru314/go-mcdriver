package testserver

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Satoru314/go-mcdriver/internal/rcon"
)

const (
	// AreaSpacing は区画どうしの x の間隔。
	AreaSpacing = 1000
	// AreaSize は区画の大きさ（x と z、ブロック）。ForceLoad で読み込んでおく範囲。
	AreaSize = 32
	// maxAreaID は区画の番号の上限。x = 1000 × 番号 がワールドの端（約 3000 万）を超えないようにする。
	// 上限を超えたら 1 に戻る。
	maxAreaID = 29000

	// SurfaceY は平らなワールドの地表のブロック（草ブロック）の y。
	// 下から岩盤 -64、土 -63 と -62、草ブロック -61。
	SurfaceY = -61
	// StandY は地表に立ったときの足の y（地表の 1 つ上の空気）。
	StandY = SurfaceY + 1
)

// Area はテスト 1 つに割り当てる、ほかのテストと重ならない区画と bot 名。
type Area struct {
	// ID は区画の番号（1 から）。プロセスをまたいで重ならない。
	ID int
	// BotName はこの区画のテストで使う bot の名前（16 文字以内）。
	BotName string
	// X0, Z0 は区画の北西の角。区画は x が X0〜X0+AreaSize-1、z が Z0〜Z0+AreaSize-1。
	X0, Z0 int
}

// Center は区画の真ん中のブロックの x と z を返す。
func (a Area) Center() (x, z int) {
	return a.X0 + AreaSize/2, a.Z0 + AreaSize/2
}

// NewArea は新しい区画と bot 名を割り当てる。並列のテストや別のプロセスから呼んでも重ならない。
// 番号はキャッシュの場所のファイルに記録し、使い終わっても戻さない（bot 名も毎回変わるので、
// サーバーに残った前のプレイヤーの記録（位置など）を引き継がない）。
func (s *Server) NewArea(t testing.TB) Area {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	id, err := allocAreaID(ctx, s.cacheDir)
	if err != nil {
		t.Fatalf("区画を割り当てられない: %v", err)
	}
	a := areaFor(id)
	t.Logf("testserver: 区画 %d（x=%d〜%d, z=%d〜%d）、bot 名 %s", a.ID, a.X0, a.X0+AreaSize-1, a.Z0, a.Z0+AreaSize-1, a.BotName)
	return a
}

func areaFor(id int) Area {
	return Area{
		ID:      id,
		BotName: "mcdbot" + strconv.Itoa(id),
		X0:      id * AreaSpacing,
		Z0:      0,
	}
}

// allocAreaID は dir/areas の番号を 1 つ進めて返す。鍵（flock）をかけて読み書きする。
func allocAreaID(ctx context.Context, dir string) (int, error) {
	f, err := lockFile(ctx, filepath.Join(dir, "areas"))
	if err != nil {
		return 0, err
	}
	defer f.Close()
	b, err := os.ReadFile(f.Name())
	if err != nil {
		return 0, err
	}
	last := 0
	if s := strings.TrimSpace(string(b)); s != "" {
		if last, err = strconv.Atoi(s); err != nil {
			return 0, fmt.Errorf("区画の番号のファイルが壊れている（%s）: %q", f.Name(), s)
		}
	}
	id := last + 1
	if id < 1 || id > maxAreaID {
		id = 1
	}
	if err := f.Truncate(0); err != nil {
		return 0, err
	}
	if _, err := f.WriteAt([]byte(strconv.Itoa(id)+"\n"), 0); err != nil {
		return 0, err
	}
	return id, nil
}

// ForceLoad は区画のチャンクを読み込んだままにする（setblock などは読み込まれていない場所に効かない）。
// テストの終わりに元に戻す。
func (a Area) ForceLoad(t testing.TB, c *rcon.Client) {
	t.Helper()
	x1, z1 := a.X0+AreaSize-1, a.Z0+AreaSize-1
	out := Command(t, c, fmt.Sprintf("forceload add %d %d %d %d", a.X0, a.Z0, x1, z1))
	t.Logf("testserver: %s", out)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		c.Command(ctx, fmt.Sprintf("forceload remove %d %d %d %d", a.X0, a.Z0, x1, z1))
	})
}
