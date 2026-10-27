package packets

import (
	"testing"

	"github.com/Satoru314/go-mcdriver/internal/protocol"
)

// Update Time のバイト列: World Age (Long)、Time of day (Long)、Time of day increasing (Boolean)。
func TestSetTime(t *testing.T) {
	b := []byte{
		0, 0, 0, 0, 0, 0, 0x30, 0x39, // 12345
		0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xfe, // -2
		1,
	}
	p, err := ReadSetTime(protocol.NewDecoder(b))
	if err != nil {
		t.Fatal(err)
	}
	if p != (SetTime{WorldAge: 12345, DayTime: -2, Ticking: true}) {
		t.Fatalf("%+v", p)
	}
	e := protocol.NewEncoder()
	WriteSetTime(e, p)
	if string(e.Bytes()) != string(b) {
		t.Fatalf("書いたバイト列 %x、期待 %x", e.Bytes(), b)
	}
	if _, err := ReadSetTime(protocol.NewDecoder(b[:16])); err == nil {
		t.Fatal("短いのにエラーにならない")
	}
}
