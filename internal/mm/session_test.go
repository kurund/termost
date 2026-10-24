package mm

import "testing"

func TestWebsocketURL(t *testing.T) {
	cases := map[string]string{
		"https://chat.example.com":  "wss://chat.example.com",
		"http://localhost:8065":     "ws://localhost:8065",
		"https://example.com/mm":    "wss://example.com/mm",
		"wss://already.example.com": "wss://already.example.com",
	}
	for in, want := range cases {
		if got := websocketURL(in); got != want {
			t.Errorf("websocketURL(%q) = %q, want %q", in, got, want)
		}
	}
}
