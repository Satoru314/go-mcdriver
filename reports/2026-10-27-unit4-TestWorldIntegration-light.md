# 単位 4 TestWorldIntegration/明るさ: ブロックを変えても明るさが更新されない

日付: 2026-10-27 / ブランチ world（地形をクライアントに組み込んだもの）

## 何をしたか

- テスト用サーバー: Docker `mcdriver-world`（eclipse-temurin:21-jre、Paper 1.21.4 build 232、sha256 確認済み）。
  online-mode=false、level-type=minecraft:flat、RCON あり、spawn-protection=0、gamemode=creative、
  view-distance=6。127.0.0.1:25765（ゲーム）/ 25775（RCON）だけに公開
- テスト `world_integration_test.go`（`-tags integration`）の `TestWorldIntegration/明るさ`
- 実行: `MCDRIVER_ADDR=127.0.0.1:25765 MCDRIVER_RCON=127.0.0.1:25775 MCDRIVER_RCON_PASS=... go test -tags integration -run TestWorldIntegration -v -count=1 -timeout 110s .`

## 期待と実際

| 確かめたこと | 期待 | 実際 |
|---|---|---|
| 空の下（y=-60）の明るさ | block 0 / sky 15 | 合っている |
| 土の中（y=-62）の空の明るさ | 0 | 合っている |
| RCON で空中に光る石（glowstone）を置き、5 秒以内に隣の明るさ | block 14 | **block 0（sky 15）のまま** |
| 光る石から 3 つ先 | block 12 | **block 0** |

チャンクのデータに入って届く明るさは正しく読めている。ブロックを変えたあとの明るさの変化が届かない。

## ログ

```
=== RUN   TestWorldIntegration/明るさ
    world_integration_test.go:449: 光る石の隣 (-9, -57, -9): block 0 sky 15 ok true、期待 block 14
    world_integration_test.go:452: 光る石から 3 つ先 block 0、期待 12
--- FAIL: TestWorldIntegration/明るさ (5.01s)
```

ほかの部分（地表、setblock / fill、パレットなし）は合格（下の「付録」）。

## 関係するパケット

調査用に、受け取った Update Light（0x2B）の中身を書き出す一時的なテストを入れて調べた（コミットはしていない）。

1. `明るさ` だけを動かしたとき: glowstone の Block Update（0x09）は 1 つ届いたが、**Update Light は 1 つも届かなかった**
2. 3 か所に glowstone を置いて 8 秒待ったとき（bot dbgbot）: Block Update 3、Update Light は 1 つだけで、
   光る石のあるチャンクではないチャンク (1,1) の block light（マスク [2]、最大値 1）だった。
   3 か所とも Light() は block 0 のまま
3. 全体を通したとき: Update Light は 7 つ。どれも fill した場所のチャンクではなく、その周りのチャンク
   （(0,-3), (-2,-3), (-3,-2), (-3,0), (-3,-3) など。bot はチャンク (-1,-1)）だった

```
worldbot light_update (0,-3) sky=[8] blk=[] esky=[] eblk=[8] nsky=1 nblk=0
worldbot light_update (-2,-3) sky=[8] blk=[] esky=[] eblk=[8] nsky=1 nblk=0
worldbot light_update (-3,-2) sky=[8] blk=[] esky=[] eblk=[8] nsky=1 nblk=0
worldbot light_update (-3,0) sky=[8] blk=[] esky=[] eblk=[8] nsky=1 nblk=0
worldbot light_update (-3,-3) sky=[8] blk=[] esky=[] eblk=[8] nsky=1 nblk=0
worldbot light_update (-2,-3) sky=[] blk=[] esky=[] eblk=[] nsky=0 nblk=0
worldbot light_update (-3,-3) sky=[] blk=[] esky=[] eblk=[] nsky=0 nblk=0
dbgbot   light_update (1,1)   sky=[] blk=[2] esky=[] eblk=[] nsky=0 nblk=1 blk[0]max=1
```

## 原因の予想

- 公式のサーバーは、明るさの変化（Update Light）を「見えている範囲の端にあるチャンク」を見ているプレイヤーにしか送らない
  （ChunkHolder.broadcastChanges が getPlayers(pos, true) の onlyOnWatchDistanceEdge で送り先を絞る）。
  公式のクライアントは自分で明るさを計算する（クライアント側の LevelLightEngine がブロックの変化から明るさを広げ直す）ので、
  内側のチャンクには送る必要がない、という作り。
  届いた Update Light が bot から離れたチャンクだけだったことと合う（Paper は view-distance+1 の範囲まで送るので、その端）
- ドライバは明るさを自分で計算していないので、チャンクを受け取ったあとにブロックが変わると、そのまわりの Light() が古いままになる
- ドライバの読み方（Update Light を読んで反映する処理）自体は、届いたパケットについては正しく動いている（偽のサーバーのテストで確認）

## 直し方の候補（決めるのは管理役）

1. クライアント側の明るさの計算を作る（ブロックの変化のたびに、公式と同じやり方で block light と sky light を広げ直す）。
   ブロックごとの光の強さ（luminance）と光の通しやすさ（opacity、形による遮り）を公式データから取る必要がある。
   今の `data` には光の強さも光の通しやすさも無い（gendata が書き出していない）
2. Light() の意味を「チャンクを受け取った時点（とサーバーが送ってきた変化）の明るさ」とし、ブロックが変わったところは
   ok=false にする（古い値を返さない）。そのチャンクがまた送られてくるまで分からない
3. 今のまま（古い値のこともある）として、設計書と API の説明に書く

## 付録: 同じ実行のほかの結果

```
bot の足元 (-4, -60, -3)、チャンク 33 個が 1.06s で届いた。Dimension {overworld MinY:-64 Height:384 HasSkylight:true}、バイオームの registry 65 個
--- PASS: TestWorldIntegration/地表
--- PASS: TestWorldIntegration/setblockとfill
--- PASS: TestWorldIntegration/パレットなし
worldbot2 が受け取ったセクションの種類:
  biomes bpe=0 値1つ long 0 個                120
  blocks bpe=0 値1つ long 0 個                111
  blocks bpe=15 パレットなし long 1024 個       1
  blocks bpe=4 パレットあり long 256 個         8
worldbot が受け取った地形のパケット:
  level_chunk_with_light(0x28) 157
  section_blocks_update(0x4E) 19
  light_update(0x2B) 7
  block_update(0x09) 5
強制修正 1 回、送った Chunk Batch Received の最後の値 3.5
```

- Paper 1.21.4 は Chunk Batch Start / Finished を送ってこない（1 つも届かない。check3 の記録でも 0）。
  そのため Chunk Batch Received を一度も送っていないが、チャンクは届き続ける

## その後（2026-10-27）

管理役が案②に決めた。ブロックが変わったセクションと周り 26 個に「古い」の印を付け、明るさが届いたら消す形にした。
実地テストの項目は「光る石を置いたあと、まわりの Light() が古い値を返さない」に変え、合格した。
