# 単位 10: TestTimeWeatherIntegration の失敗（雨の開始・終了の Game Event の番号が逆）

## どのテストか

- `events_integration_test.go` の `TestTimeWeatherIntegration`（実地テスト、Paper 1.21.4 build 232）
- `sg docker -c 'go test -tags integration -run "TestEventsIntegration|TestTimeWeatherIntegration" -v -count=1 -timeout 110s .'`
- 時刻の部分（time set / time add）と `TestEventsIntegration` は通った。失敗は天気の `Weather().Raining` だけ

## 期待した値と実際の値

| コマンド | 期待 | 実際 |
|---|---|---|
| `weather rain` のあと | `Raining: true` | `Raining: false`（`Kind: rain` にはなる） |
| `weather thunder` のあと | `Raining: true` | `Raining: false`（`Kind: thunder` にはなる） |
| `weather clear` のあと | `Raining: false` | `Raining: true`（`Kind: clear` にはなる） |

`Kind`（晴れ・雨・雷雨）は雨と雷の強さ（Game Event 7 と 8）から決めているので正しく出る。
間違っているのは、雨の開始・終了（Game Event 1 と 2）で決める `Raining` の印だけ。

## ログ

```
RCON weather rain → "Set the weather to rain"
weather rain: 990ms で {Kind:rain Raining:false RainLevel:0.20000002 ThunderLevel:0}
RCON weather thunder → "Set the weather to rain & thunder"
weather thunder: 4.55s で {Kind:thunder Raining:false RainLevel:1 ThunderLevel:0.90999943}
RCON weather clear → "Set the weather to clear"
weather clear: 4.05s で {Kind:clear Raining:true RainLevel:0.19000068 ThunderLevel:0.11}
```

## 関係するパケットの記録

一時的な記録用のテストで、Game Event の 1 と 2 だけをそのまま書き出した（記録用のテストは消した）:

```
RCON weather rain → Set the weather to rain
23:51:58.777 Game Event 0200000000 (event 2 value 0)
RCON weather clear → Set the weather to clear
23:51:59.827 Game Event 0100000000 (event 1 value 0)
```

雨が始まると event 2、止むと event 1 が届く。

## 原因の予想

`internal/packets/self.go`（単位 5 で作ったもの）の定数の名前が逆:

```go
GameEventBeginRaining       uint8 = 1
GameEventEndRaining         uint8 = 2
```

本物のサーバーでは 1 が「雨の終了」、2 が「雨の開始」（wiki.vg の表と同じ）。
今これらの定数を使っているのは `time.go`（単位 10）の `timeOnGameEvent` だけ（self.go は使っていない）。

## 直し方の案（決めるのは管理役）

- `internal/packets/self.go` の 2 つの定数の値を入れ替える（1 = EndRaining, 2 = BeginRaining）。偽のサーバーの部品ごとのテスト（`events_test.go`）は定数の名前で書いているので、そのまま通るはず
- 指示があるまで、この単位では直していない
