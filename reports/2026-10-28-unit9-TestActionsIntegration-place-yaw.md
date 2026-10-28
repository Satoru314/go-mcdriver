# 単位 9: TestActionsIntegration/place — 向きのパケットがサーバーに捨てられる

## どのテストか

`actions_integration_test.go` の `TestActionsIntegration/place`。樫の階段を Yaw 0, 90, 180, -90 の順に地面の上に置き、
RCON の `execute if block` で facing を確かめる。

## 期待した値と実際の値

| Yaw | 期待 | 実際 |
|---|---|---|
| 0 | facing=south | south |
| 90 | west | west |
| 180 | north | north |
| -90 | east | **north** |

-90 で置いた直後の `data get entity <bot> Rotation` は `[-180.0f, 39.007473f]`（ひとつ前の 180 で置いたときの向きのまま。
pitch も前のまま）。ドライバの側では cl.yaw = -90、moveState.lastYaw = -90 で、向きのパケット（Set Player Rotation）を
送ったことを controls.go に一時的に入れた出力で確かめた（DEBUGROT yaw -90 pitch 35 flags 1）。
強制修正は増えず、サーバーのログに警告も無い。

## 調べたこと（Look を 0.3 秒おきに変えて RCON で Rotation を読む）

- tp（yaw -90, pitch 0）のあと、`Look(180, 39)` → 届く（サーバーは -180 と記録）。続けて `Look(-90, 35)` → **捨てられる**
- 同じ並びでも、`Look(-180, 39)` → `Look(-90, 35)` は届く。`Look(180, 39)` → `Look(270, 35)` も届く
- 捨てられるのは tp のあと 1 回だけ。そのあと同じ並び（180 → -90）をしても届く
- 最初に `Look(0,10)` → `Look(-90,35)` → `Look(180,39)` → `Look(-90,35)` とすると、最後の 1 回だけ捨てられる

## 原因の予想

Paper（CraftBukkit）の handleMovePlayer の PlayerMoveEvent の処理と思われる。サーバーはプレイヤーの向きを
`Mth.wrapDegrees` で -180〜180 に直して持つが、PlayerMoveEvent の比べる元（lastYaw）はパケットの値そのまま（180）。
tp のあと justTeleported が立ったままで、`from`（lastYaw = 180）と今の場所（-180）が「違う」と判定されると、
その回の動き（向き）を元に戻して return する（justTeleported を下ろす）。そのため、tp のあと、
サーバーの持つ向きと前に送った値が食い違っている最初の 1 回の向きのパケットが捨てられる。
本家のクライアントも -180〜180 の外の yaw（ぐるぐる回った値）を送るので、Paper の上では同じことが起こりうる。

## 今回入れた対処（actions.go の中だけ）

行動の前に向く向き（aimLocked と Place の Yaw）を `wrapDegrees` で -180 以上 180 未満にしてから使う。
これでサーバーの持つ値と前に送った値が同じになり、上の判定に当たらない。これで place は通った。

残っている問題（管理役の判断が要る）:

- controls.go の `LookAt`（atan2 - 90 で -270〜90 になる）と `Look`（そのまま）は直していない（物理の係の担当のファイル）。
  これらで -180〜180 の外の yaw を送ると、tp のあとの 1 回の向きが捨てられうる
- 向きを直しても、10 度以下の小さな向きの変化（PlayerMoveEvent が出ず lastYaw が古いまま）のあとに tp があると、
  同じように 1 回捨てられる可能性がある（試していない）。確実にするなら、行動の前に向きを送り直す、
  あるいは Use Item On を送る前に 1 ティック余計に待つなどが考えられる
