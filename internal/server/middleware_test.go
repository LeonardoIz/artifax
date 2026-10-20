package server

import (
	"net/url"
	"testing"
)

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

func TestPageLinks(t *testing.T) {
	tests := []struct {
		query, next string
		want        pager
	}{
		{"", "", pager{}},
		{"", "c2", pager{Older: "/admin?cursor=c2"}},
		{"q=log&cursor=c1", "c2", pager{Newest: "/admin?q=log", Older: "/admin?cursor=c2&q=log"}},
		{"cursor=c1", "", pager{Newest: "/admin"}},
	}
	for _, tt := range tests {
		q, _ := url.ParseQuery(tt.query)
		if got := pageLinks("/admin", q, tt.next); got != tt.want {
			t.Errorf("pageLinks(%q, %q) = %+v, want %+v", tt.query, tt.next, got, tt.want)
		}
	}
}
