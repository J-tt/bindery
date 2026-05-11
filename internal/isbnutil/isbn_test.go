package isbnutil

import "testing"

func TestNormalize(t *testing.T) {
	for _, tt := range []struct {
		name string
		raw  string
		want string
	}{
		{name: "isbn10 lowercase x check digit", raw: "3-453-30523-x", want: "345330523X"},
		{name: "isbn13 hyphen separators", raw: "978-0-307-47472-8", want: "9780307474728"},
		{name: "isbn13 space separators", raw: "978 0 307 47472 8", want: "9780307474728"},
		{name: "interior x preserved", raw: "978X0307474728", want: "978X0307474728"},
		{name: "early x preserved", raw: "97X80307474728", want: "97X80307474728"},
		{name: "multiple x preserved", raw: "978X030747472X", want: "978X030747472X"},
		{name: "invalid letters preserved", raw: "ISBN 9780307474728", want: "ISBN9780307474728"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := Normalize(tt.raw); got != tt.want {
				t.Fatalf("Normalize(%q) = %q, want %q", tt.raw, got, tt.want)
			}
		})
	}
}
