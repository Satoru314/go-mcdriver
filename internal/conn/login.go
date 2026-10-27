package conn

import (
	"fmt"

	"github.com/Satoru314/go-mcdriver/data"
	"github.com/Satoru314/go-mcdriver/internal/protocol"
)

// ハンドシェイクの次の状態。
const (
	intentStatus   = 1
	intentLogin    = 2
	intentTransfer = 3
)

// MaxUsernameLen はプレイヤー名の最大の長さ。
const MaxUsernameLen = 16

// Profile はログインで決まったプレイヤー。
type Profile struct {
	UUID       protocol.UUID
	Name       string
	Properties []Property // オフラインでは普通は空
}

// Property はプレイヤーの属性（スキンなど）。
type Property struct {
	Name      string
	Value     string
	Signature string // 無ければ空
}

// Login はハンドシェイクからログインの完了（Login Acknowledged を送る）までを行う。
// host と port はハンドシェイクに書くサーバーの名前とポート。
// 戻ったとき、接続は設定の段階（Configuration）にある。
// 途中で中止したいときは、呼ぶ前に c.WatchContext を使う。
func Login(c *Conn, host string, port uint16, username string) (Profile, error) {
	if username == "" || len(username) > MaxUsernameLen {
		return Profile{}, fmt.Errorf("conn: プレイヤー名の長さが正しくない: %q（1〜%d 文字）", username, MaxUsernameLen)
	}

	// Handshake（next state = login）
	if err := c.Send(data.HandshakeServerboundIntention, func(e *protocol.Encoder) error {
		e.WriteVarInt(data.ProtocolVersion)
		if err := e.WriteString(host, 255); err != nil {
			return err
		}
		e.WriteUShort(port)
		e.WriteVarInt(intentLogin)
		return nil
	}); err != nil {
		return Profile{}, fmt.Errorf("Handshake を送る: %w", err)
	}

	// Login Start（オフラインの UUID）
	if err := c.Send(data.LoginServerboundHello, func(e *protocol.Encoder) error {
		if err := e.WriteString(username, MaxUsernameLen); err != nil {
			return err
		}
		e.WriteUUID(OfflineUUID(username))
		return nil
	}); err != nil {
		return Profile{}, fmt.Errorf("Login Start を送る: %w", err)
	}

	for {
		p, err := c.ReadPacket()
		if err != nil {
			return Profile{}, fmt.Errorf("ログイン中に読む: %w", err)
		}
		d := protocol.NewDecoder(p.Data)
		switch p.ID {
		case data.LoginClientboundLoginDisconnect:
			s, err := d.ReadString(262144)
			if err != nil {
				return Profile{}, fmt.Errorf("Disconnect (login): %w", err)
			}
			return Profile{}, &DisconnectError{State: data.StateLogin, Reason: TextFromJSON(s)}

		case data.LoginClientboundHello:
			return Profile{}, ErrEncryption

		case data.LoginClientboundLoginCompression:
			th, err := d.ReadVarInt()
			if err != nil {
				return Profile{}, fmt.Errorf("Set Compression: %w", err)
			}
			c.SetThreshold(int(th))

		case data.LoginClientboundLoginFinished:
			prof, err := readProfile(d)
			if err != nil {
				return Profile{}, fmt.Errorf("Login Success: %w", err)
			}
			if err := c.Send(data.LoginServerboundLoginAcknowledged, nil); err != nil {
				return Profile{}, fmt.Errorf("Login Acknowledged を送る: %w", err)
			}
			return prof, nil

		case data.LoginClientboundCustomQuery:
			// Login Plugin Request: 何も分からないので「理解できない」と答える
			msgID, err := d.ReadVarInt()
			if err != nil {
				return Profile{}, fmt.Errorf("Login Plugin Request: %w", err)
			}
			if err := c.Send(data.LoginServerboundCustomQueryAnswer, func(e *protocol.Encoder) error {
				e.WriteVarInt(msgID)
				e.WriteBool(false) // データなし
				return nil
			}); err != nil {
				return Profile{}, fmt.Errorf("Login Plugin Response を送る: %w", err)
			}

		case data.LoginClientboundCookieRequest:
			if err := ReplyCookie(c, d, data.LoginServerboundCookieResponse); err != nil {
				return Profile{}, err
			}

		default:
			// 知らないパケットは読み飛ばす
		}
	}
}

// readProfile は Login Success の中身を読む（1.21.2 以降は Strict Error Handling がない）。
func readProfile(d *protocol.Decoder) (Profile, error) {
	var prof Profile
	var err error
	if prof.UUID, err = d.ReadUUID(); err != nil {
		return prof, err
	}
	if prof.Name, err = d.ReadString(MaxUsernameLen); err != nil {
		return prof, err
	}
	prof.Properties, err = protocol.ReadPrefixedArray(d, 16, func(d *protocol.Decoder) (Property, error) {
		var pr Property
		var err error
		if pr.Name, err = d.ReadString(64); err != nil {
			return pr, err
		}
		if pr.Value, err = d.ReadString(32767); err != nil {
			return pr, err
		}
		sig, ok, err := protocol.ReadPrefixedOptional(d, func(d *protocol.Decoder) (string, error) {
			return d.ReadString(1024)
		})
		if err != nil {
			return pr, err
		}
		if ok {
			pr.Signature = sig
		}
		return pr, nil
	})
	return prof, err
}

// ReplyCookie は Cookie Request に「持っていない」と答える。id は向きと段階に合った Cookie Response の ID。
func ReplyCookie(c *Conn, d *protocol.Decoder, id int32) error {
	key, err := d.ReadIdentifier()
	if err != nil {
		return fmt.Errorf("Cookie Request: %w", err)
	}
	if err := c.Send(id, func(e *protocol.Encoder) error {
		if err := e.WriteIdentifier(key); err != nil {
			return err
		}
		e.WriteBool(false) // 中身なし
		return nil
	}); err != nil {
		return fmt.Errorf("Cookie Response を送る: %w", err)
	}
	return nil
}
