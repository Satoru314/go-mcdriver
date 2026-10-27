package conn

import (
	"testing"

	"github.com/Satoru314/go-mcdriver/internal/nbt"
	"github.com/Satoru314/go-mcdriver/internal/protocol"
)

func TestTextPlain(t *testing.T) {
	cases := []struct {
		name string
		v    any
		want string
	}{
		{"文字列だけ", "Kicked", "Kicked"},
		{"text と extra", map[string]any{"text": "a", "extra": []any{"b", map[string]any{"text": "c", "color": "red"}}}, "abc"},
		{"空のキーで包んだもの", map[string]any{"text": "", "extra": []any{map[string]any{"": "x"}, map[string]any{"": "y"}}}, "xy"},
		{"translate と with", map[string]any{"translate": "multiplayer.disconnect.kicked", "with": []any{"bot1", map[string]any{"text": "op"}}},
			"multiplayer.disconnect.kicked [bot1, op]"},
		{"translate だけ", map[string]any{"translate": "disconnect.timeout"}, "disconnect.timeout"},
	}
	for _, c := range cases {
		if got := TextPlain(c.v); got != c.want {
			t.Errorf("%s: %q, 期待 %q", c.name, got, c.want)
		}
	}
}

func TestTextFromJSON(t *testing.T) {
	cases := []struct{ in, want string }{
		{`{"text":"You are banned","extra":[{"text":"!"}]}`, "You are banned!"},
		{`"plain"`, "plain"},
		{`{"translate":"multiplayer.disconnect.server_full"}`, "multiplayer.disconnect.server_full"},
		{`not json`, "not json"},
	}
	for _, c := range cases {
		if got := TextFromJSON(c.in); got != c.want {
			t.Errorf("TextFromJSON(%q) = %q, 期待 %q", c.in, got, c.want)
		}
	}
}

func TestReadTextNBT(t *testing.T) {
	// ルートが TAG_String のもの（1.20.3 からは、ただの文字列はこの形で届く）
	e := protocol.NewEncoder()
	if err := nbt.WriteNetwork(e, "Server closed"); err != nil {
		t.Fatal(err)
	}
	// ルートが TAG_Compound のもの
	if err := nbt.WriteNetwork(e, map[string]any{"text": "Bye ", "extra": []any{"bot1"}}); err != nil {
		t.Fatal(err)
	}
	d := protocol.NewDecoder(e.Bytes())
	for _, want := range []string{"Server closed", "Bye bot1"} {
		got, err := ReadTextNBT(d)
		if err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Errorf("ReadTextNBT = %q, 期待 %q", got, want)
		}
	}
	if d.Remaining() != 0 {
		t.Errorf("読み残し %d バイト", d.Remaining())
	}
}
