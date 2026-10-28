# 単位 10: 全部の実地テスト（go test -tags integration ./...）で TestEntitiesIntegration と TestStage1Login が失敗（ログインの絞り込み）

## どのテストか

- `sg docker -c 'go test -tags integration -count=1 -timeout 25m ./...'`（雨の番号を直したあと、2026-10-28）
- 失敗: `TestEntitiesIntegration`（30.26s）、`TestStage1Login`（30.05s）。ほかのテストとほかのパッケージは合格
- `TestTimeWeatherIntegration` は、単独で回して合格（Raining が rain/thunder で true、clear で false）

## 期待した値と実際の値

- 期待: Connect が（testserver.Retry のやり直しの中で）成功する
- 実際: 6 回やり直しても `Connection throttled! Please wait before reconnecting.` で断られ、30 秒で失敗

## ログ

```
entities_integration_test.go:340: mcdbot110b の接続が絞られた（mcdriver: サーバーから切断された（login）: Connection throttled! Please wait before reconnecting.）。5 秒後にやり直す
entities_integration_test.go:340: mcdbot110b の接続が絞られた（mcdriver: サーバーから切断された（login）: Connection throttled! Please wait before reconnecting.）。5 秒後にやり直す
entities_integration_test.go:340: mcdbot110b の接続が絞られた（mcdriver: サーバーから切断された（login）: Connection throttled! Please wait before reconnecting.）。5 秒後にやり直す
entities_integration_test.go:340: mcdbot110b の接続が絞られた（mcdriver: サーバーから切断された（login）: Connection throttled! Please wait before reconnecting.）。5 秒後にやり直す
entities_integration_test.go:340: mcdbot110b の接続が絞られた（mcdriver: サーバーから切断された（login）: Connection throttled! Please wait before reconnecting.）。5 秒後にやり直す
entities_integration_test.go:342: 2 体目の Connect: mcdriver: サーバーから切断された（login）: Connection throttled! Please wait before reconnecting.
stage1_integration_test.go:44: testserver: ログインを断られた（1 回目）。5s 待ってやり直す: mcdriver: サーバーから切断された（login）: Connection throttled! Please wait before reconnecting.
stage1_integration_test.go:44: testserver: ログインを断られた（2 回目）。5s 待ってやり直す: mcdriver: サーバーから切断された（login）: Connection throttled! Please wait before reconnecting.
stage1_integration_test.go:44: testserver: ログインを断られた（3 回目）。5s 待ってやり直す: mcdriver: サーバーから切断された（login）: Connection throttled! Please wait before reconnecting.
stage1_integration_test.go:44: testserver: ログインを断られた（4 回目）。5s 待ってやり直す: mcdriver: サーバーから切断された（login）: Connection throttled! Please wait before reconnecting.
stage1_integration_test.go:44: testserver: ログインを断られた（5 回目）。5s 待ってやり直す: mcdriver: サーバーから切断された（login）: Connection throttled! Please wait before reconnecting.
stage1_integration_test.go:44: testserver: ログインを断られた（6 回目）。5s 待ってやり直す: mcdriver: サーバーから切断された（login）: Connection throttled! Please wait before reconnecting.
stage1_integration_test.go:52: Connect: mcdriver: サーバーから切断された（login）: Connection throttled! Please wait before reconnecting.（1.15207ms）
```

## 原因の予想

- 動いているテスト用サーバー（起動 2026-10-27T13:08:51Z）の bukkit.yml が `connection-throttle: 4000` のまま:
  `docker exec mcdriver-testserver cat /data/bukkit.yml` → `connection-throttle: 4000`
- `internal/testserver/config.go` は新しく作るコンテナにだけ `connection-throttle: -1` を置く（「作り直したサーバーにだけ効く」）。このコンテナはその変更より前に作られた
- 全部の実地テストを並列で回すと、同じ IP（Docker のポート転送）から 4 秒以内のログインが続き、Retry（5 秒おき）でも空きを取れない bot が出る
- 前の記録 `reports/2026-10-27-unit5-TestSelfIntegration-throttle.md` と同じ種類。単位 10 の変更（出来事・時刻・天気）とは関係しないと考える

## 直し方の案（決めるのは管理役）

- テスト用サーバーを作り直す（`docker rm -f mcdriver-testserver` のあと、次のテストで新しく作られる。初回は数分かかる）
- または動いているコンテナの /data/bukkit.yml を書き換えて再起動する
