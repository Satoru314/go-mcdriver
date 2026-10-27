package mcdriver

// 世界の状態（時刻と天気）の受け取りと読み出し。
//
// 受け取るパケット: Update Time（時刻。サーバーは 20 ティックごとと /time のときに送る）、
// Game Event（雨の開始・終了、雨と雷の強さ）、Login (play) と Respawn（世界が変わったら天気を晴れに戻す。
// サーバーが続けて今の天気を送り直す）。

import (
	"fmt"
	"sync"
	"time"

	"github.com/Satoru314/go-mcdriver/data"
	"github.com/Satoru314/go-mcdriver/internal/packets"
	"github.com/Satoru314/go-mcdriver/internal/protocol"
)

// TicksPerDay は 1 日のティックの数。
const TicksPerDay = 24000

// maxTimeExtrapolation は時刻を補間する上限（ティック）。サーバーは 20 ティックごとに送ってくるので、
// これより長く届かないときは（サーバーが重い、止まっているなど）それ以上進めない。
const maxTimeExtrapolation = 100

// TimeInfo は時刻の詳しい値。
type TimeInfo struct {
	// Known は Update Time を一度でも受け取ったか。false ならほかの値は 0
	Known bool
	// WorldAge はワールドが動いたティックの数
	WorldAge int64
	// DayTime は日の数も含んだ時刻（ティック）。Day*24000 + TimeOfDay
	DayTime int64
	// Day は日の数（0 から）、TimeOfDay はその日の中の時刻（0〜23999。0 が朝 6 時、6000 が正午、18000 が真夜中）
	Day, TimeOfDay int64
	// Ticking は時刻が進んでいるか（gamerule doDaylightCycle）
	Ticking bool
}

// WeatherKind は天気の種類。
type WeatherKind int

const (
	// Clear は晴れ
	Clear WeatherKind = iota
	// Rain は雨（寒いところでは雪）
	Rain
	// Thunder は雷雨
	Thunder
)

// String は "clear"、"rain"、"thunder"（/weather の引数と同じ）を返す。
func (k WeatherKind) String() string {
	switch k {
	case Clear:
		return "clear"
	case Rain:
		return "rain"
	case Thunder:
		return "thunder"
	}
	return fmt.Sprintf("WeatherKind(%d)", int(k))
}

// Weather は天気。
//
// 雨や雷は少しずつ強く・弱くなる（1 ティックに 0.01 ずつ、サーバーが毎ティック強さを送る）。
// Kind は本家のクライアントと同じ決まりで強さから決める: 雷の強さ×雨の強さ > 0.9 なら Thunder、
// 雨の強さ > 0.2 なら Rain、それ以外は Clear。/weather を使ってから Kind が変わるまで数秒かかる。
type Weather struct {
	Kind WeatherKind
	// Raining はサーバーが雨にしているか（雨の開始で true、終了で false。強さより先に変わる）
	Raining bool
	// RainLevel は雨の強さ（0〜1）、ThunderLevel は雷の強さ（0〜1）
	RainLevel, ThunderLevel float32
}

// kind は強さから天気の種類を決める（本家の Level.isThundering / isRaining）。
func (w Weather) kind() WeatherKind {
	switch {
	case w.ThunderLevel*w.RainLevel > 0.9:
		return Thunder
	case w.RainLevel > 0.2:
		return Rain
	}
	return Clear
}

// timeState は時刻と天気。鍵 mu で守る。
type timeState struct {
	mu      sync.RWMutex
	t       packets.SetTime
	known   bool
	recv    time.Time // t を受け取った時刻（補間に使う）
	weather Weather
}

var timeKey = newFeature(func(*Client) *timeState { return &timeState{} })

func init() {
	onPlay(data.PlayClientboundSetTime, (*Client).timeOnSetTime)
	onPlay(data.PlayClientboundGameEvent, (*Client).timeOnGameEvent)
	onPlay(data.PlayClientboundLogin, (*Client).timeOnWorldChange)
	onPlay(data.PlayClientboundRespawn, (*Client).timeOnWorldChange)
}

// ---- 公開 API ----

// Time は今の日の数と、その日の中の時刻（0〜23999。0 が朝 6 時、6000 が正午、13000 ごろから夜）を返す。
// サーバーは 20 ティックごとにしか送ってこないので、時刻が進んでいるときは、受け取ってからの時間で補間する。
// まだ受け取っていなければ 0, 0。
func (cl *Client) Time() (day, timeOfDay int64) {
	ti := cl.TimeInfo()
	return ti.Day, ti.TimeOfDay
}

// TimeInfo は時刻の詳しい値を返す（Time と同じく補間する）。
func (cl *Client) TimeInfo() TimeInfo {
	ts := timeKey.of(cl)
	ts.mu.RLock()
	t, known, recv := ts.t, ts.known, ts.recv
	ts.mu.RUnlock()
	return timeInfoAt(t, known, recv, time.Now())
}

// timeInfoAt は受け取った値 t を、受け取った時刻 recv から at までの時間で進めた値を返す。
func timeInfoAt(t packets.SetTime, known bool, recv, at time.Time) TimeInfo {
	if !known {
		return TimeInfo{}
	}
	ticks := int64(at.Sub(recv) / TickInterval)
	ticks = min(max(ticks, 0), maxTimeExtrapolation)
	ti := TimeInfo{Known: true, WorldAge: t.WorldAge + ticks, DayTime: t.DayTime, Ticking: t.Ticking}
	if t.Ticking {
		ti.DayTime += ticks
	}
	ti.Day = floorDiv(ti.DayTime, TicksPerDay)
	ti.TimeOfDay = ti.DayTime - ti.Day*TicksPerDay
	return ti
}

func floorDiv(a, b int64) int64 {
	q := a / b
	if a%b != 0 && (a < 0) != (b < 0) {
		q--
	}
	return q
}

// Weather は今の天気を返す。
func (cl *Client) Weather() Weather {
	ts := timeKey.of(cl)
	ts.mu.RLock()
	defer ts.mu.RUnlock()
	return ts.weather
}

// ---- 受け取ったパケットの処理 ----

func (cl *Client) timeOnSetTime(d *protocol.Decoder) error {
	p, err := packets.ReadSetTime(d)
	if err != nil {
		return fmt.Errorf("mcdriver: Update Time: %w", err)
	}
	ts := timeKey.of(cl)
	ts.mu.Lock()
	ts.t, ts.known, ts.recv = p, true, time.Now()
	ts.mu.Unlock()
	// 1 秒ごとに届くので、HP が減らないまま残っているダメージの種類をここでも知らせる（events.go）
	cl.flushStaleDamage()
	return nil
}

func (cl *Client) timeOnGameEvent(d *protocol.Decoder) error {
	p, err := packets.ReadGameEvent(d)
	if err != nil {
		return nil // self.go が切断にする
	}
	ts := timeKey.of(cl)
	ts.mu.Lock()
	old := ts.weather.Kind
	w := &ts.weather
	switch p.Event {
	case packets.GameEventBeginRaining:
		// 本家のクライアントはここで雨の強さを 0 にするが、サーバーが続けて今の強さを送るので、
		// 強さは強さのパケットだけで変える（リスポーンで送り直されたときに、いったん晴れに見えないように）
		w.Raining = true
	case packets.GameEventEndRaining:
		w.Raining = false
	case packets.GameEventRainLevel:
		w.RainLevel = clamp01(p.Value)
	case packets.GameEventThunderLevel:
		w.ThunderLevel = clamp01(p.Value)
	default:
		ts.mu.Unlock()
		return nil
	}
	w.Kind = w.kind()
	cur := *w
	ts.mu.Unlock()
	if cur.Kind != old {
		cl.emit(WeatherEvent{eventBase: evNow(), Weather: cur})
	}
	return nil
}

func clamp01(v float32) float32 { return min(max(v, 0), 1) }

// timeOnWorldChange は世界が変わったら天気を晴れに戻す（本家のクライアントも世界を作り直す）。
// 雨ならサーバーが続けて雨の開始と強さを送り直すので、雨の中でリスポーンすると「晴れ」「雨」の知らせが続けて届く。
func (cl *Client) timeOnWorldChange(*protocol.Decoder) error {
	ts := timeKey.of(cl)
	ts.mu.Lock()
	old := ts.weather.Kind
	ts.weather = Weather{}
	ts.mu.Unlock()
	if old != Clear {
		cl.emit(WeatherEvent{eventBase: evNow(), Weather: Weather{}})
	}
	return nil
}
