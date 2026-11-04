package slugify

import (
	"regexp"
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

var nonAlnum = regexp.MustCompile(`[^a-z0-9]+`)

func Slugify(input string) string {
	var b strings.Builder
	for _, r := range norm.NFD.String(input) {
		if r >= 0x300 && r <= 0x36f {
			continue
		}
		b.WriteRune(unicode.ToLower(r))
	}
	return strings.Trim(nonAlnum.ReplaceAllString(b.String(), "-"), "-")
}
