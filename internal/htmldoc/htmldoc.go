// Package htmldoc validates uploaded HTML documents and extracts metadata.
package htmldoc

import (
	"bytes"
	"errors"
	"strings"
	"unicode/utf8"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

var (
	ErrEmpty    = errors.New("document is empty")
	ErrTooLarge = errors.New("document exceeds the upload limit")
	ErrEncoding = errors.New("document must be valid UTF-8 without NUL bytes")
	ErrNotHTML  = errors.New("document does not contain any HTML element")
)

// Validate checks that b looks like a usable HTML document of at most max bytes.
func Validate(b []byte, max int64) error {
	switch {
	case len(b) == 0:
		return ErrEmpty
	case int64(len(b)) > max:
		return ErrTooLarge
	case !utf8.Valid(b) || bytes.IndexByte(b, 0) >= 0:
		return ErrEncoding
	}
	z := html.NewTokenizer(bytes.NewReader(b))
	for {
		switch z.Next() {
		case html.ErrorToken:
			return ErrNotHTML
		case html.StartTagToken, html.SelfClosingTagToken:
			return nil
		}
	}
}

// Title returns the text of the first <title> element, or "" if there is none.
func Title(b []byte) string {
	z := html.NewTokenizer(bytes.NewReader(b))
	for {
		switch z.Next() {
		case html.ErrorToken:
			return ""
		case html.StartTagToken:
			if z.Token().DataAtom != atom.Title {
				continue
			}
			if z.Next() != html.TextToken {
				return ""
			}
			return strings.Join(strings.Fields(z.Token().Data), " ")
		}
	}
}
