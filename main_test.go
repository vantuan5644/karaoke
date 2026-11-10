package main

import (
	"reflect"
	"testing"
)

func TestSplitArgs(t *testing.T) {
	for _, c := range []struct {
		in   string
		want []string
	}{
		{"", nil},
		{"  --cookies-from-browser   firefox ", []string{"--cookies-from-browser", "firefox"}},
		{`--cookies "C:\Users\Jane Doe\c.txt" -v`, []string{"--cookies", `C:\Users\Jane Doe\c.txt`, "-v"}},
		{`--proxy 'socks5://a b' x""y`, []string{"--proxy", "socks5://a b", "xy"}},
		{`--empty ""`, []string{"--empty", ""}},
	} {
		if got, err := splitArgs(c.in); err != nil || !reflect.DeepEqual(got, c.want) {
			t.Errorf("splitArgs(%q) = %q, %v; want %q", c.in, got, err, c.want)
		}
	}
	if _, err := splitArgs(`--cookies "C:\no end`); err == nil {
		t.Error("an unclosed quote was accepted")
	}
}
