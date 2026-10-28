# 単位 7b: 実地テストのログインが "Connection throttled" で断られる

## どのテストか

`sg docker -c 'go test -tags integration -run "TestMoveIntegration|TestMove7bIntegration" -v -count=1 -timeout 6m .'`
（単位 7 の 9 個と単位 7b の 7 個、合わせて 16 個の bot を並列に）

## 期待した値と実際の値

- 期待: すべての bot がログインでき、位置がサーバーの記録と一致する
- 実際: 6 個のシナリオ（高さ5から落ちる、歩く、ジャンプ、走る、壁にぶつかる、深い池から岸に上がる）が
  `Connect mcdbotNNN: mcdriver: サーバーから切断された（login）: Connection throttled! Please wait before reconnecting.`
  で失敗した（testserver.Retry の 6 回のやり直しを使い切った）
- ログインできた残りの 10 個は、止まった位置がすべてサーバーの記録と差 0 で一致し、強制修正の数も合い、
  サーバーのログに "moved wrongly" などは無かった

## ログ

```
move_integration_test.go:60: testserver: ログインを断られた（6 回目）。5s 待ってやり直す: mcdriver: サーバーから切断された（login）: Connection throttled! Please wait before reconnecting.
move_integration_test.go:60: Connect mcdbot168: mcdriver: サーバーから切断された（login）: Connection throttled! Please wait before reconnecting.
```

## 原因の予想

- 動いているテスト用サーバーは bukkit.yml の `connection-throttle: -1` を入れる前に作られたもので、既定の 4000 ms が効いている
  （config.go の注釈どおり、作り直したサーバーにだけ効く）
- Spigot の throttle は、断ったときも最後の時刻を更新するので、ほかのテスト（同じ時間に並行して動いている別の開発係の実地テスト、
  または同じプロセスの Retry 中のほかの bot）の接続が 4 秒以内に入ると、やり直しがずっと断られ続ける
- moveConnect の 4.5 秒の間あけは同じプロセスの中だけで効く

物理のせいではない（ログインの前の段階）。失敗したシナリオだけを `-parallel 2` で回し直した結果は最終報告に書く。

## 直し方の案（管理役が決める）

- テスト用サーバーを作り直して `connection-throttle: -1` を効かせる
- または、ログインの間あけをプロセスをまたいで（キャッシュのロックファイルなどで）そろえる

## 回し直した結果

- `-parallel 2` で 6 個を回し直す: 歩く、高さ5から落ちる、ジャンプ、深い池から岸に上がる は合格（位置の差 0）。走る、壁にぶつかる は再びログインを断られた
- `-parallel 1` で 2 個を回し直す: 走る は合格。壁にぶつかる は bot 1 個だけでも 30 秒のあいだ 7 回続けて断られた
  → 同じ IP からの別の接続（ほかの開発係のテストなど）が続いていると考えられる。これ以上は回し直さずに止めた
- これで単位 7 と 7b の 16 個のうち 15 個は、このブランチで合格を確かめた。「壁にぶつかる」だけがログインできずに確かめられていない
