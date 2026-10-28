# go-mcdriver

Minecraft Java Edition 1.21.4 用の bot ドライバ（Go）。bot をユーザーとしてログインさせ、ゲームの情報を受け取り、行動を指示できる。
既存の bot ライブラリに頼らず、通信からゼロから作っている。設計は [docs/design.md](docs/design.md)。

- 対象は 1.21.4 だけ。ログインはオフラインモード（`online-mode=false`）だけに対応する（正規アカウントの認証と暗号化は無い）
- 「体と感覚」まで（状態を読む、低レベルの行動）を持つ。経路探索や建築などの判断は使う側で書く

## 使い方

```sh
go get github.com/Satoru314/go-mcdriver
```

```go
package main

import (
	"context"
	"log"
	"time"

	mcdriver "github.com/Satoru314/go-mcdriver"
)

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	c, err := mcdriver.Connect(ctx, mcdriver.Config{Addr: "127.0.0.1:25565", Username: "bot1"})
	if err != nil {
		log.Fatal(err)
	}
	defer c.Close()

	// 出来事を読む（切断するとチャネルが閉じる）
	go func() {
		for ev := range c.Events() {
			switch ev := ev.(type) {
			case mcdriver.DeathEvent:
				log.Println("死んだ:", ev.Message)
				c.Respawn()
			case mcdriver.DamageEvent:
				log.Printf("ダメージ %s %.1f", ev.Type, ev.Amount)
			}
		}
	}()

	// 東を向いて 2 秒歩く
	c.Look(-90, 0)
	c.SetControls(mcdriver.Controls{Forward: true})
	time.Sleep(2 * time.Second)
	c.SetControls(mcdriver.Controls{})

	s := c.Self()
	day, tod := c.Time()
	log.Printf("位置 %v HP %.1f 満腹度 %d / %d 日目 %d / 天気 %v", s.Pos, s.Health, s.Food, day, tod, c.Weather().Kind)

	<-c.Done()
	log.Println("切断:", c.Err())
}
```

状態を読む関数は、呼んだ時点の写しを返す。どれも複数の goroutine から同時に呼べる。
時間のかかる行動は `context.Context` を受け取り、時間切れや中止で必ず戻る。

## 主な API

| 分類 | API | 中身 |
|---|---|---|
| 接続 | `Connect(ctx, Config)`, `Close()`, `Done()`, `Err()` | ログインしてゲームに入る。切断の理由は `Err()`（サーバーに切られたら `*DisconnectError`） |
| 自分の体 | `Self()`, `Respawn()` | 位置、速度、向き、接地、HP、満腹度、経験値、死亡、ゲームモード、最後に受けたダメージ |
| 地形 | `Block(p)`, `Light(p)`, `WorldView(a, b)`, `Dimension()`, `LoadedChunks()` | ブロックの状態（`data.BlockState`）と明るさ。`WorldView` は範囲をまとめて写す（経路探索向け） |
| 生き物と物 | `Entities()`, `Entity(id)`, `PlayerNames()` | 見えているエンティティ（種類、位置、速度、向き、体力、落ちているアイテムの中身） |
| 持ち物 | `Inventory()`, `Container()`, `SelectHotbar(i)`, `ClickSlot(ctx, ...)`, `Drop(ctx, ...)`, `CloseContainer()` | 持ち物と開いている容器。クリックはサーバーに受け入れられたかを確かめて戻る |
| 移動 | `SetControls(Controls)`, `Controls()`, `Look(yaw, pitch)`, `LookAt(p)`, `Sprinting()`, `Swimming()`, `Pose()` | 前後左右・ジャンプ・走る・しゃがむ。物理（重力、衝突、水）はライブラリが毎ティック計算して位置を送る |
| 行動 | `Dig(ctx, p, f)`, `DigTicks(p)`, `Place(ctx, against, f, PlaceOpts)`, `UseBlock(ctx, p, f)`, `StartUseItem()`, `StopUseItem()`, `Attack(ctx, id)`, `AttackCharge()` | 掘る（壊れたことをサーバーの確認とブロックの変化で確かめる）、向きと面の位置を指定して置く、ブロックを使う（容器なら窓が開くまで待つ）、食べる・弓を引いて放つ、攻撃と溜め |
| 出来事 | `Events()`, `EventsDropped()` | 下の「出来事」 |
| 世界 | `Time()`, `TimeInfo()`, `Weather()` | 日の数とその日の時刻（0〜23999）、天気（晴れ・雨・雷雨と強さ） |
| データ | `data` パッケージ | ブロック、アイテム（道具、食べ物、属性の修飾）、レシピ、当たり判定、エンティティの大きさ（サーバーの jar の公式データから生成） |

### エラー

- 行動の失敗は `errors.Is` で見分けられる値を包んで返す（`ErrOutOfReach`、`ErrNotLoaded`、`ErrNotPlaced`、`ErrClickRejected` など。`go doc` を見よ）
- 設定の段階に戻っているあいだ（サーバーの切り替えなど）に送る操作は `ErrNotPlaying`
- 切断したあとに呼んだ操作は、切断の理由（`Err()`）を包んだエラーを返す。`Close` で閉じたなら `errors.Is(err, mcdriver.ErrClosed)` が真
- 受け取ったパケットの処理や毎ティックの処理が panic したら（ドライバの不具合）、接続を切り、`Err()` は `ErrInternal` を包んだエラー（呼び出しの記録つき）になる。プロセスは落ちない

座標は Minecraft と同じ（+x が東、+z が南、+y が上）。位置と速度は gonum の `r3.Vec`、ブロックの位置は `BlockPos`（整数）。

### 出来事（Events）

`Events()` は出来事のチャネル（何度呼んでも同じもの）。届く型:

| 型 | いつ |
|---|---|
| `DeathEvent` | 自分が死んだ（理由つき） |
| `RespawnEvent` | リスポーンした・ディメンションを移った（`AfterDeath` で区別） |
| `DamageEvent` | ダメージを受けた（種類 `Type` と減った量 `Amount`） |
| `TeleportEvent` | サーバーに位置を強制的に直された（/tp、ログイン、動きが認められなかったとき） |
| `BlockChangeEvent` | ブロックが変わった（位置と前後の状態） |
| `EntitySpawnEvent` / `EntityRemoveEvent` | 生き物や物が現れた・見えなくなった |
| `ContainerOpenEvent` / `ContainerCloseEvent` | 容器の窓が開いた・閉じた |
| `WeatherEvent` | 天気（晴れ・雨・雷雨）が変わった |
| `DisconnectEvent` | 接続が終わった。最後に 1 つだけ入り、そのあとチャネルが閉じる |

チャネルには `Config.EventBuffer` 個（既定 256）までためる。読むのが遅れていっぱいになったら、**古いものから捨てて**新しいものを入れる
（受信は止めない）。捨てた数は `EventsDropped()` で分かる。読まなくてもよい。

## 確認用のツール

```sh
go run ./cmd/mcprobe -addr 127.0.0.1:25565 -name probe1
```

繋いで、位置、HP、満腹度、持ち物、時刻、天気、近くの生き物と、届いた出来事を表示し続ける（bot は動かない）。
`-interval`（表示の間隔）、`-radius`（近くの生き物の距離）、`-quiet`（ブロックと生き物の出来事を出さない）、`-respawn=false`（死んでもリスポーンしない）。

## テスト

| 種類 | コマンド | 時間 |
|---|---|---|
| 部品ごとのテスト | `go test -race ./...` | 数十秒。偽のサーバー（テストの中の goroutine）を使う |
| 実地テスト | `go test -tags integration ./...` | 数分。Docker のテスト用サーバーに繋ぐ |
| 長時間運転 | `go test -tags soak -run TestSoak -v -timeout 30m . -soak.duration 10m` | 指定した時間 |

docker グループが反映されていないシェルでは `sg docker -c 'go test -tags integration ./...'` のように動かす。

### 公式データ

`data/*_gen.go` はサーバーの jar の公式データから作る。作り直し・全件の照合をするときは:

```sh
go run ./cmd/gendata          # jar を取って（sha1 を確かめて）データを書き出し、data/*_gen.go を作る
go test ./data                # 書き出したデータ（.gendata/generated）と全件で照らし合わせる
```

`.gendata/` が無いと、全件の照合のテストは飛ばされる。

### テスト用サーバー

実地テストは `internal/testserver` が Docker で立てる Paper 1.21.4（build 232）を使う。

- コンテナ名 `mcdriver-testserver`、ゲームは `127.0.0.1:25865`、RCON は `127.0.0.1:25875`（127.0.0.1 にしか開かない）
- 平らなワールド、時刻と天気は止める、敵は勝手に湧かない（gamerule は繋ぐたびに設定し直す）
- 起動したままにして次のテストでも使う（初回は Paper の準備で数分かかる。待つ時間は `MCDRIVER_TESTSERVER_WAIT` で変えられる）
- テストごとに専用の区画（x を 1000 ずつずらす）と bot 名を割り当てるので、並列に動かせる。時刻と天気を変えるテストは 1 つずつ
- jar や区画の番号は `~/.cache/go-mcdriver`（`MCDRIVER_CACHE_DIR` で変えられる）
- 止めるとき: `docker rm -f mcdriver-testserver`

### 長時間運転

`soak_test.go`（`-tags soak`）は bot を数体つなぎ、歩く・走る・ジャンプ・tp を繰り返しながら動かし、
一定の間隔で切断の回数、位置の強制修正の回数、goroutine の数、ヒープの量を記録して、最後に増え続けていないかを判定する。

| 引数 | 既定 | 中身 |
|---|---|---|
| `-soak.duration` | 10m | 動かす時間（`go test` の `-timeout` はこれより 2 分以上長くする） |
| `-soak.bots` | 3 | bot の数 |
| `-soak.interval` | 30s | 記録の間隔 |
| `-soak.csv` | なし | 記録を書き出す CSV |

## CI

`.github/workflows/test.yml`（GitHub Actions）。push と pull request で動く。

- `unit`: gofmt、`go vet`（タグ無し・`integration`・`soak`）、`go run ./cmd/gendata` で公式データを書き出して `data/` が変わらないことを確かめ、`go test -race ./...`
- `integration`: ubuntu の Docker で `internal/testserver` がテスト用サーバーを立て、`go test -tags integration ./...`（時間の上限 30 分）
- キャッシュ: Go のモジュール、gendata の作業フォルダ（`.gendata`）、テスト用サーバーの Paper の jar（`~/.cache/go-mcdriver`）
