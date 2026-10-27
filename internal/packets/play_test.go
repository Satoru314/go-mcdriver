package packets

import (
	"reflect"
	"testing"

	"github.com/Satoru314/go-mcdriver/internal/protocol"
)

// Login (play) を書いて読むと元に戻ること（死んだ場所あり）。
func TestLoginPlayRoundTrip(t *testing.T) {
	want := LoginPlay{
		EntityID: 7, Hardcore: true, DimensionNames: []string{"minecraft:overworld"},
		MaxPlayers: 20, ViewDistance: 12, SimulationDistance: 8, EnableRespawnScreen: true,
		DimensionType: 2, DimensionName: "minecraft:overworld", HashedSeed: -1, GameMode: 0,
		PreviousGameMode: -1, HasDeathLocation: true, DeathDimension: "minecraft:the_nether",
		DeathLocation: protocol.Position{X: -10, Y: 70, Z: 33}, PortalCooldown: 300, SeaLevel: 63,
		EnforcesSecureChat: true,
	}
	e := protocol.NewEncoder()
	if err := WriteLoginPlay(e, want); err != nil {
		t.Fatal(err)
	}
	d := protocol.NewDecoder(e.Bytes())
	got, err := ReadLoginPlay(d)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) || d.Remaining() != 0 {
		t.Errorf("got %+v（残り %d）", got, d.Remaining())
	}
}

// Synchronize Player Position のバイト数（VarInt 1 + Double 6 + Float 2 + Int 1 = 1+48+8+4）と相対の当てはめ。
func TestPlayerPosition(t *testing.T) {
	pp := PlayerPosition{TeleportID: 3, X: 1, Y: 2, Z: 3, Yaw: 10, Pitch: 80, Flags: RelX | RelZ | RelPitch}
	e := protocol.NewEncoder()
	WritePlayerPosition(e, pp)
	if e.Len() != 61 {
		t.Errorf("%d バイト、期待 61", e.Len())
	}
	got, err := ReadPlayerPosition(protocol.NewDecoder(e.Bytes()))
	if err != nil || got != pp {
		t.Fatalf("got %+v %v", got, err)
	}
	x, y, z, yaw, pitch := pp.Apply(100, 200, 300, 45, 30)
	if x != 101 || y != 2 || z != 303 || yaw != 10 || pitch != 90 {
		t.Errorf("Apply = %v %v %v %v %v", x, y, z, yaw, pitch)
	}
}

// Set Player Position and Rotation は 8*3 + 4*2 + 1 = 33 バイト。
func TestMovePlayerPosRot(t *testing.T) {
	m := MovePlayerPosRot{X: 1.5, Y: 64, Z: -2.5, Yaw: 90, Pitch: -10, Flags: MoveOnGround}
	e := protocol.NewEncoder()
	WriteMovePlayerPosRot(e, m)
	if e.Len() != 33 {
		t.Errorf("%d バイト、期待 33", e.Len())
	}
	got, err := ReadMovePlayerPosRot(protocol.NewDecoder(e.Bytes()))
	if err != nil || got != m {
		t.Errorf("got %+v %v", got, err)
	}
}
