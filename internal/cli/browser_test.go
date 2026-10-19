package cli

import (
	"reflect"
	"testing"
)

// The command is only built here, never started, so no browser opens.
func TestBrowserCommand(t *testing.T) {
	const url = "http://127.0.0.1:8420/device?code=BDFG-HJKL"
	for goos, want := range map[string][]string{
		"windows": {"rundll32", "url.dll,FileProtocolHandler", url},
		"darwin":  {"open", url},
		"linux":   {"xdg-open", url},
		"freebsd": {"xdg-open", url},
	} {
		if got := browserCommand(goos, url).Args; !reflect.DeepEqual(got, want) {
			t.Errorf("%s: args = %v, want %v", goos, got, want)
		}
	}
}
