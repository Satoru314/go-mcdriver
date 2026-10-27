package conn

import "testing"

// オフラインの UUID が、Java の UUID.nameUUIDFromBytes と同じ値になること。
func TestOfflineUUID(t *testing.T) {
	cases := []struct{ name, want string }{
		// 既知の値（Notch のオフライン UUID）
		{"Notch", "b50ad385-829d-3141-a216-7e7d7539ba7f"},
	}
	for _, c := range cases {
		got := OfflineUUID(c.name).String()
		if got != c.want {
			t.Errorf("OfflineUUID(%q) = %s, 期待 %s", c.name, got, c.want)
		}
	}
	// version 3 と variant のビット
	u := OfflineUUID("bot1")
	if u[6]>>4 != 3 {
		t.Errorf("version = %d, 期待 3", u[6]>>4)
	}
	if u[8]>>6 != 2 {
		t.Errorf("variant = %b, 期待 10", u[8]>>6)
	}
}
