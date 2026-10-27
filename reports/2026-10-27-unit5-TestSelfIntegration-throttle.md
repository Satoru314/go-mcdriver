# 単位 5: TestSelfIntegration が「Connection throttled」でログインできない（テスト用サーバーの設定）

- 日付: 2026-10-27
- テスト: `TestSelfIntegration`（self_integration_test.go）
- 実行: `sg docker -c 'go test -tags integration -run TestSelfIntegration -v -count=1 -timeout 45s .'`

## 期待した値と実際の値

- 期待: `Connect` がログインできる
- 実際: `Connect: mcdriver: サーバーから切断された（login）: Connection throttled! Please wait before reconnecting.`（0.06 秒で失敗）

単独で回したときは何度も通っている（下の「通った記録」）。失敗したのは次の 2 つの場合だけ:

1. `-count=3` で同じテストを続けて回したとき（2 回目以降が 1 秒以内に繋ぎ直す）
2. ほかの開発係の実地テスト（同じ時刻に mcdbot22 の stage1、mcdbot37 / mcdbot37b など）が同じテスト用サーバーに繋いでいるとき

## ログ

テスト用サーバーのログ（13:26 UTC ごろ。どの接続も 172.17.0.1 から来る）:

```
[13:26:29 INFO]: mcdbot36[/172.17.0.1:36022] logged in with entity id 64 at ([world]4.5, -60.0, -8.5)
[13:26:35 INFO]: mcdbot22 lost connection: stage1 kick mcdbot22
[13:26:35 INFO]: mcdbot37[/172.17.0.1:51300] logged in with entity id 70 at ([world]3.5, -60.0, -7.5)
[13:26:43 INFO]: mcdbot37b[/172.17.0.1:48636] logged in with entity id 71 at ([world]-4.5, -60.0, 4.5)
```

コンテナの中の `bukkit.yml`:

```
7:  connection-throttle: 4000
```

## 原因の予想

Paper（Bukkit）の `settings.connection-throttle`（既定 4000 ms）。同じ IP から 4 秒以内に次のログインが来ると断る。
Docker のポート転送のため、テストの接続はすべて 172.17.0.1 から来るので、別々のテスト（別の開発係、`-count`、`t.Parallel` の bot）が 4 秒以内に繋ぐと、あとのほうが断られる。
設計書の「テストはできるだけ並列で動かす」とぶつかる。

## 直し方の案（管理役が決める）

- internal/testserver が作るサーバーの `bukkit.yml` で `connection-throttle: -1`（無効）にする（単位 0 のファイルなので、自分では変えていない）
- または、テストの側で Connect を throttle のときだけ数回やり直す

## 通った記録（単独で回したとき）

```
RCON effect give mcdbot28 minecraft:instant_damage 1 0 → "Applied effect Instant Damage to mcdbot28"
ダメージのあと HP 14、LastDamage {Type:minecraft:magic TypeID:27 CauseEntityID:-1 DirectEntityID:-1 ...}
経験値 {Bar:0 Level:0 Total:0} → {Bar:0.42857128 Level:7 Total:100}
死亡: HP 0、DeathMessage "death.attack.genericKill [mcdbot28]"
リスポーンのあと: 位置 {-2.5 -60 -2.5} HP 20 満腹度 20 経験値 {Bar:0 Level:0 Total:0} ゲームモード survival
リスポーンのあとのダメージ: tp から 600ms で HP 14
--- PASS: TestSelfIntegration (0.93s)
```

## 管理役の判断と対応（2026-10-27）

- internal/testserver が新しくコンテナを作るとき、bukkit.yml（`settings.connection-throttle: -1`）を置く。
  今動いている mcdriver-testserver は作り直していないので、まだ 4000 のまま（作り直したときに効く。実際の作り直しでは未確認）
- `testserver.Retry(ctx, t, f)`: エラーが "Connection throttled" を含むときだけ 5 秒待って最大 6 回やり直す。
  self、stage1、world、check3 の実地テストの Connect をこれで包んだ
- `-count=3` で TestSelfIntegration を続けて回して 3 回とも通過（2・3 回目は 1 回断られ、やり直して通った）。
  TestStage1Login も通過。TestWorldIntegration/明るさ と TestCheck3Login（flying で切断）の失敗は、
  それぞれ既存の記録（2026-10-27-unit4-TestWorldIntegration-light.md、2026-10-27-check3-tp.md）と同じで、この変更の前のコードでも同じように失敗する
