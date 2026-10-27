package mcdriver

// 機能ごとのファイル（world.go, self.go, entities.go, inventory.go など）が、
// client.go を触らずに「受け取るパケット」と「自分の状態」を足すための仕組み。
//
// 使い方（例: world.go）:
//
//	// 状態の構造体。自分の鍵を持ち、読み出しは写しを返す
//	type worldState struct { mu sync.RWMutex; ... }
//
//	// 状態の鍵（Client ごとに 1 つ作られる）
//	var worldKey = newFeature(func(cl *Client) *worldState { return &worldState{} })
//
//	func init() {
//		onPlay(data.PlayClientboundBlockUpdate, func(cl *Client, d *protocol.Decoder) error {
//			ws := worldKey.of(cl)
//			...
//		})
//	}
//
// 登録はパッケージの初期化（init）のあいだだけ行う。Connect のあとに登録してはいけない。

import (
	"github.com/Satoru314/go-mcdriver/internal/protocol"
)

// playHandler は Play の段階のパケット 1 つの処理。d はパケットの中身（ID のあと）の先頭から読める。
// 受信の goroutine から 1 つずつ呼ばれる（同じ Client で同時に呼ばれることはない）。
// エラーを返すと接続を切る（Err にそのエラーが入る）。
type playHandler func(cl *Client, d *protocol.Decoder) error

// playHandlers はパケット ID → 処理の一覧（登録した順に呼ぶ）。
var playHandlers = map[int32][]playHandler{}

// onPlay は Play の段階のパケット id の処理を登録する。同じ id に複数登録してよく、
// それぞれに先頭から読める別の Decoder が渡される。
// client.go が自分で扱うパケット（Keep Alive, Login など）にも登録でき、client.go の処理のあとに呼ばれる。
func onPlay(id int32, h playHandler) {
	playHandlers[id] = append(playHandlers[id], h)
}

// dispatchPlay は id に登録された処理を順に呼ぶ。
func (cl *Client) dispatchPlay(p protocol.Packet) error {
	for _, h := range playHandlers[p.ID] {
		if err := h(cl, protocol.NewDecoder(p.Data)); err != nil {
			return err
		}
	}
	return nil
}

// featureMakers は機能の状態を作る関数の一覧（featureKey の idx が添字）。
var featureMakers []func(cl *Client) any

// featureKey は機能の状態を Client から取り出す鍵。newFeature で作る。
type featureKey[T any] struct{ idx int }

// newFeature は機能の状態を登録し、取り出す鍵を返す。パッケージ変数の初期化で呼ぶ。
// mk は Client を作るときに 1 度呼ばれる（このときはまだ接続していない。cl の設定だけ読める）。
func newFeature[T any](mk func(cl *Client) *T) featureKey[T] {
	featureMakers = append(featureMakers, func(cl *Client) any { return mk(cl) })
	return featureKey[T]{idx: len(featureMakers) - 1}
}

// of は cl の状態を返す。
func (k featureKey[T]) of(cl *Client) *T { return cl.features[k.idx].(*T) }

// initFeatures は登録された機能の状態を作る。Connect で、受信を始める前に呼ぶ。
func (cl *Client) initFeatures() {
	cl.features = make([]any, len(featureMakers))
	for i, mk := range featureMakers {
		cl.features[i] = mk(cl)
	}
}
