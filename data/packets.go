package data

// 接続の状態（packets.json の分け方）。
const (
	StateHandshake     = "handshake"
	StateStatus        = "status"
	StateLogin         = "login"
	StateConfiguration = "configuration"
	StatePlay          = "play"
)

// パケットの向き。
const (
	Clientbound = "clientbound" // サーバー → クライアント
	Serverbound = "serverbound" // クライアント → サーバー
)

// Packet は1つのパケットの種類。
type Packet struct {
	State     string // StateHandshake など
	Direction string // Clientbound か Serverbound
	Name      string // 公式の名前（"minecraft:" を外したもの。例 "keep_alive"）
	ID        int32
}

type packetKey struct{ state, dir, name string }

var packetByName = func() map[packetKey]Packet {
	m := make(map[packetKey]Packet, len(packets))
	for _, p := range packets {
		m[packetKey{p.State, p.Direction, p.Name}] = p
	}
	return m
}()

// Packets は全パケットを返す（状態, 向き, ID の順）。
func Packets() []Packet { return packets }

// PacketID は状態・向き・名前からパケット ID を引く。
func PacketID(state, direction, name string) (int32, bool) {
	p, ok := packetByName[packetKey{state, direction, trimNS(name)}]
	return p.ID, ok
}

// PacketByID は状態・向き・ID からパケットを引く。
func PacketByID(state, direction string, id int32) (Packet, bool) {
	for _, p := range packets {
		if p.State == state && p.Direction == direction && p.ID == id {
			return p, true
		}
	}
	return Packet{}, false
}
