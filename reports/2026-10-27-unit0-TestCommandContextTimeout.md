# 失敗の記録: unit0 / internal/rcon TestCommandContextTimeout

- 日付: 2026-10-27
- 単位: 0（土台）
- 担当: 開発係 unit0（ブランチ unit0）
- 状態: **作業を止めている**（コードは直していない。internal/testserver は未着手）

## どのテストか

`internal/rcon/rcon_test.go` の `TestCommandContextTimeout`（部品ごとのテスト。偽の RCON サーバーを使い、Docker は使わない）。

応答が返ってこないコマンドを、100ms で時間切れになる context で送り、
`Command` が `context.DeadlineExceeded` を包んだエラーで戻ることを確かめる。

## 期待した値と実際の値

- 期待: `errors.Is(err, context.DeadlineExceeded)` が true
- 実際: `err = rcon: 応答を読めない: read tcp 127.0.0.1:39890->127.0.0.1:42191: i/o timeout`
  （`os.ErrDeadlineExceeded` / i/o timeout を包んだエラー。`context.DeadlineExceeded` ではない）
- 2 秒以内に戻る、中断後の接続が使えない、という残りの確認には届く前に止まっている（戻るまでの時間は 0.10s で、戻ること自体はできている）

## ログ

```
$ go test -race -count=1 -v ./internal/rcon/
--- PASS: TestPacketEncode / TestReadPacket / TestReadPacketBroken / TestDialAndCommand
--- PASS: TestAuthFailed / TestAuthEmptyResponseFirst / TestLongResponse / TestCommandTooLong
=== RUN   TestCommandContextTimeout
    rcon_test.go:266: err = rcon: 応答を読めない: read tcp 127.0.0.1:39890->127.0.0.1:42191: i/o timeout, want DeadlineExceeded
--- FAIL: TestCommandContextTimeout (0.10s)
--- PASS: TestCommandContextCancel / TestConcurrentCommands / TestClosed
FAIL	github.com/Satoru314/go-mcdriver/internal/rcon	0.235s

$ go test -count=20 -run 'TestCommandContextTimeout$' ./internal/rcon/
→ 20 回中 8 回失敗（-race なしでも起きる。-race ありでは 3 回とも失敗）
```

ほかの 11 個のテストは通っている。

## 原因の予想

`internal/rcon/client.go` の 2 か所の組み合わせによる、時刻の競争。

1. `watch`（70 行目〜）は、ctx に締め切りがあると、接続の締め切りを **ctx と同じ時刻** に `conn.SetDeadline(dl)` で設定する
2. `fail`（83 行目〜）は、通信のエラーが起きたときに `ctx.Err()` を見て、nil でなければ「中断された: <ctx のエラー>」に包み直す

締め切りの時刻になると、接続の読み込み（ネットワークのタイマー）と context のタイマーの両方が切れるが、
接続のほうが先に i/o timeout で戻ることがある。そのとき `fail` の中ではまだ `ctx.Err()` が nil なので、
context のエラーに包み直されず、i/o timeout のエラーのまま返る。

試していない直し方の案（どれにするかは管理役・ユーザーと決める）:

- A: `watch` で ctx の締め切りを接続に設定するのをやめ、`context.AfterFunc` による打ち切りだけにする（AfterFunc は ctx が終わったあとに呼ばれるので、`fail` の時点で `ctx.Err()` は必ず非 nil）
- B: `fail` で、エラーが `os.ErrDeadlineExceeded` で ctx に締め切りがあり、その時刻を過ぎていれば `context.DeadlineExceeded` として扱う
- C: テストの期待を「`context.DeadlineExceeded` または `os.ErrDeadlineExceeded`」に緩める（使う側がエラーの種類で判断しにくくなるので、おすすめしない）

案 A がいちばん単純だと考える。

## 関係するパケットの記録

偽のサーバーとの通信なので、Minecraft のパケットは関係しない。
送ったもの: 認証（ID 1, 種類 3）→ 認証の応答（ID 1, 種類 2）→ コマンド "hang"（ID 2, 種類 2）。
偽のサーバーは "hang" に応答しないので、ここで読み込み待ちになり、100ms 後に打ち切られた。

## 今の作業の状態

- 作ったもの（未コミット）: `go.mod`, `doc.go`, `internal/rcon/packet.go`, `internal/rcon/client.go`, `internal/rcon/rcon_test.go`
- 未着手: `internal/testserver`（Docker のテスト用サーバー）
