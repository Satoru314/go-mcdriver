package testserver

import (
	"context"
	"strings"
	"testing"
	"time"
)

const (
	// ThrottleMessage は、同じ IP から短い間隔でログインしたときにサーバーが断る理由に含まれる文字列。
	// Paper（Bukkit）の settings.connection-throttle による。
	ThrottleMessage = "Connection throttled"
	// RetryWait は断られてからやり直すまで待つ時間（connection-throttle の既定 4000 ms より長く）。
	RetryWait = 5 * time.Second
	// RetryMax はやり直す回数の上限（最初の 1 回は数えない）。
	RetryMax = 6
)

// Retry は f を呼び、エラーが ThrottleMessage を含むときだけ RetryWait 待って、最大 RetryMax 回やり直す。
// それ以外のエラーと nil はそのまま返す。ctx が終わったら待つのをやめて最後のエラーを返す。
// 同じ IP（Docker のポート転送で 172.17.0.1）から別々のテストが続けて繋ぐと、
// 今動いているサーバー（connection-throttle が既定のまま）に断られるため。
// f は呼ばれるたびに新しく接続する（時間切れの context も f の中で作る）。
func Retry(ctx context.Context, t testing.TB, f func() error) error {
	t.Helper()
	for i := 0; ; i++ {
		err := f()
		if err == nil || !strings.Contains(err.Error(), ThrottleMessage) || i >= RetryMax {
			return err
		}
		t.Logf("testserver: ログインを断られた（%d 回目）。%v 待ってやり直す: %v", i+1, RetryWait, err)
		select {
		case <-ctx.Done():
			return err
		case <-time.After(RetryWait):
		}
	}
}
