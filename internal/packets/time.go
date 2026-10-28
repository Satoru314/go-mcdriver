package packets

// 世界の状態（単位 10）のパケット: Update Time。
// 天気は Game Event（self.go の GameEventBeginRaining など）で届く。

import "github.com/Satoru314/go-mcdriver/internal/protocol"

// SetTime は Update Time（1.21.4）。サーバーは 20 ティックごとと、/time を使ったときに送る。
type SetTime struct {
	// WorldAge はワールドが動いたティックの数（止まらない）
	WorldAge int64
	// DayTime は時刻（ティック）。24000 で 1 日。日の数も含んだ値（例 24000*3+6000 は 4 日目の正午）
	DayTime int64
	// Ticking は時刻が進んでいるか（gamerule doDaylightCycle）。1.21.2 から別の値として届く
	Ticking bool
}

// ReadSetTime は Update Time を読む。
func ReadSetTime(d *protocol.Decoder) (SetTime, error) {
	var p SetTime
	var err error
	if p.WorldAge, err = d.ReadLong(); err != nil {
		return p, err
	}
	if p.DayTime, err = d.ReadLong(); err != nil {
		return p, err
	}
	p.Ticking, err = d.ReadBool()
	return p, err
}

// WriteSetTime は Update Time を書く（テストの偽のサーバー用）。
func WriteSetTime(e *protocol.Encoder, p SetTime) {
	e.WriteLong(p.WorldAge)
	e.WriteLong(p.DayTime)
	e.WriteBool(p.Ticking)
}
