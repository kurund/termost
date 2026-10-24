package clip

import "testing"

func TestPickImageType(t *testing.T) {
	cases := map[string][]string{
		"image/png":  {"text/plain", "image/jpeg", "image/png"},
		"image/jpeg": {"TARGETS", "image/jpeg ", "image/x-weird"},
		"":           {"text/plain", "UTF8_STRING"},
	}
	for want, types := range cases {
		if got := PickImageType(types); got != want {
			t.Errorf("PickImageType(%v) = %q, want %q", types, got, want)
		}
	}
}
