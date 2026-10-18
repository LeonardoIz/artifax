package server

import "testing"

func TestSafeNext(t *testing.T) {
	tests := map[string]string{
		"":                     "/",
		"/admin":               "/admin",
		"/d/abc?x=1":           "/d/abc?x=1",
		"//evil.example":       "/",
		`/\evil.example`:       "/",
		"https://evil.example": "/",
		"admin":                "/",
	}
	for in, want := range tests {
		if got := safeNext(in); got != want {
			t.Errorf("safeNext(%q) = %q, want %q", in, got, want)
		}
	}
}
