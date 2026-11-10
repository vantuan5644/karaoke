// Package fold makes text compare the way people search it: lowercase, without accents.
package fold

import (
	"strings"
	"unicode"

	"golang.org/x/text/runes"
	"golang.org/x/text/transform"
	"golang.org/x/text/unicode/norm"
)

var letters = strings.NewReplacer("đ", "d", "Đ", "d", "ł", "l", "ø", "o", "ß", "ss")

// String lowercases s and takes the accents off ("Nắng Ấm" becomes "nang am"), so a title
// typed or uploaded without them still matches.
func String(s string) string {
	t := transform.Chain(norm.NFD, runes.Remove(runes.In(unicode.Mn)), norm.NFC)
	out, _, err := transform.String(t, s)
	if err != nil {
		out = s
	}
	return letters.Replace(strings.ToLower(out))
}
