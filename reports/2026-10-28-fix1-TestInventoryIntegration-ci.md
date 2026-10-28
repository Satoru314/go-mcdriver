# CI でだけ落ちた TestInventoryIntegration（クリックの結果の取り違え）

- CI: `gh run view 40654753278 -R Satoru314/go-mcdriver --log-failed`
- 失敗: 右クリックで 1 個置く 2 回目の `ClickSlot(36, 1, 0)` が「スロット 36 は stone×1、予測は stone×2」で ErrClickRejected

## 原因

前の ClickSlot の答え（窓の全体）が 1 つ余り、それを次のクリックの結果と取り違えていた。

1. Paper（CraftBukkit）は窓の全体（Set Container Content）を送るたびに、すぐあとで左手のスロット（45）を
   Set Container Slot で送り、そのたびに state ID を 1 進める（`AbstractContainerMenu.sendAllDataToRemote` の
   `synchronizer.sendOffHandSlotChange()`）
2. 前の作りの ClickSlot は、クリックを本物の state ID で送り、続けて確かめ用のクリック（state ID -1）を送って、
   「窓の全体が 1 つ届いたら」結果とみなしていた
3. 確かめ用のクリックの答えの窓の全体を受け取った直後（左手のスロットを受け取る前）に次のクリックを作ると、
   古い state ID で送られる。サーバーは合わないので、クリックを実行したあと窓の全体を送る。確かめ用のクリックの分と
   合わせて窓の全体が 2 つ届く
4. ClickSlot は 1 つ目で戻り、2 つ目（確かめ用のクリックの答え）は、その次の ClickSlot が送ったあとに届く。
   次の ClickSlot はそれを自分の結果とみなすが、中身はそのクリックの前のままなので「予測と違う」になる
   （200ms あとに見ると、サーバーではちゃんと置けている）

CI の記録の state ID: 4 回目のクリック → 0x30, 0x31（クリックしたスロットと結果のスロット）、0x32（クリック自身の窓の全体）、
0x33（左手）。5 回目のクリックの直後に届いた 0x34 の窓の全体は、4 回目の確かめ用のクリックの答え。

## 再現

- 手元（CT102）で CPU を 1 つにすると出る: `GOMAXPROCS=1 taskset -c 4 <go test -c で作った実地テスト> -test.run TestInventoryIntegration -test.count=25` で 25 回中 3 回
- 速いクリックを続ける実地テスト `TestInventoryRapidClicksIntegration`（石 64 を拾って右クリックで 26 個置くのを 5 回）は、
  前の作りでは CPU を 1 つにして 5 回中 5 回落ちる

## 直し方

- クリックは、わざと合わない state ID（-1）で送る。サーバーは state ID が合わなくてもクリックを実行し、必ず窓の全体を
  送り直すので、クリック 1 回に答えの窓の全体がちょうど 1 つになる。確かめ用のクリックはやめた
  （合わない state ID でもクリックが実行されることは、Paper の `handleContainerClick` と実地で確かめた）
- それでもクリックと関係のない窓の全体が先に届くことがある（Paper はクラフトの結果を取ったとき `CraftItemEvent` のあとに
  もう 1 回送る など）。そこで、届いた内容が予測と違っても、250ms（clickRecheck）のあいだ次の内容を待ち、
  予測どおりのものが届けば受け入れられたとする
- 直したあと: CPU 1 つで TestInventory* を 8 回、-race で 6 回、すべて通る

## ついでに見つけたもの

TestInventoryIntegration の「カーソルに物があるときに地面にアイテムが出ない」の確かめが、give のときにサーバーが出す
見せかけのアイテム（拾えない PickupDelay 32767、次のティックで消える）を数えて落ちることがあった（CPU 1 つで 25 回中 3 回）。
`nbt=!{PickupDelay:32767s}` で除いた。
