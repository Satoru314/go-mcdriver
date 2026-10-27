package conn

import (
	"errors"
	"fmt"
)

var (
	// ErrEncryption はサーバーが暗号化（正規アカウントの認証）を求めたときのエラー。
	// このドライバはオフラインモード専用で、暗号化は作らない。
	ErrEncryption = errors.New("conn: サーバーが暗号化を求めた（オンラインモードのサーバーには入れない）")
	// ErrTransfer はサーバーが別のサーバーへの移動（Transfer）を求めたときのエラー。対応しない。
	ErrTransfer = errors.New("conn: サーバーが別のサーバーへの移動を求めた（対応しない）")
)

// DisconnectError はサーバーから切断された（Disconnect を受け取った）ことを表す。
type DisconnectError struct {
	State  string // 切られた段階（data.StateLogin / StateConfiguration / StatePlay）
	Reason string // 理由（文字の部品を文字列にしたもの）
}

func (e *DisconnectError) Error() string {
	return fmt.Sprintf("サーバーから切断された（%s）: %s", e.State, e.Reason)
}
