package htmldoc

import (
	"errors"
	"testing"
)

func TestValidate(t *testing.T) {
	tests := map[string]struct {
		in   string
		want error
	}{
		"full document": {"<!doctype html><html><body><p>hi</p></body></html>", nil},
		"fragment":      {"<h1>Report</h1>", nil},
		"empty":         {"", ErrEmpty},
		"plain text":    {"just some text", ErrNotHTML},
		"nul byte":      {"<p>a\x00b</p>", ErrEncoding},
		"invalid utf8":  {"<p>\xff\xfe</p>", ErrEncoding},
		"too large":     {"<p>" + string(make([]byte, 100)) + "</p>", ErrTooLarge},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			if err := Validate([]byte(tc.in), 64); !errors.Is(err, tc.want) {
				t.Fatalf("Validate = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestTitle(t *testing.T) {
	tests := map[string]string{
		"<html><head><title>  Q3   Sales\n Report </title></head></html>": "Q3 Sales Report",
		"<title>Tom &amp; Jerry</title>":                                  "Tom & Jerry",
		"<h1>No title</h1>":                                               "",
		"<title></title>":                                                 "",
	}
	for in, want := range tests {
		if got := Title([]byte(in)); got != want {
			t.Errorf("Title(%q) = %q, want %q", in, got, want)
		}
	}
}
