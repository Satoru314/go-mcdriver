package testserver

import (
	"context"
	"errors"
	"testing"
	"time"
)

// throttle のエラーのときだけやり直し、ほかのエラーと成功はすぐ返すこと。
func TestRetry(t *testing.T) {
	throttled := errors.New("サーバーから切断された（login）: Connection throttled! Please wait before reconnecting.")
	other := errors.New("接続できない")

	n := 0
	if err := Retry(context.Background(), t, func() error { n++; return other }); err != other || n != 1 {
		t.Errorf("ほかのエラー: %v、%d 回", err, n)
	}
	n = 0
	if err := Retry(context.Background(), t, func() error { n++; return nil }); err != nil || n != 1 {
		t.Errorf("成功: %v、%d 回", err, n)
	}

	// 1 回断られてから通る（RetryWait だけ待つ）
	n = 0
	start := time.Now()
	err := Retry(context.Background(), t, func() error {
		n++
		if n == 1 {
			return throttled
		}
		return nil
	})
	if err != nil || n != 2 || time.Since(start) < RetryWait {
		t.Errorf("1 回断られる: %v、%d 回、%v", err, n, time.Since(start))
	}

	// ctx が終わったら待たずに最後のエラーを返す
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	n = 0
	if err := Retry(ctx, t, func() error { n++; return throttled }); err != throttled || n != 1 {
		t.Errorf("ctx が終わっている: %v、%d 回", err, n)
	}
}
