package ui

import (
	"strings"
	"testing"
)

// Anything a remote host, a profile or an error message can put in front of the
// terminal passes through MessageText, so it must never carry control sequences.
func FuzzMessageText(f *testing.F) {
	for _, seed := range []string{
		"", "plain", "\x1b[2J", "\x1b]52;c;secret\x07", "\x1b]0;title\x1b\\", "a\x00b", "\r\n", "\x9b31m", "\x1bP1$r\x1b\\", "\xff", "\x1b[", "line\ttab\nnext",
	} {
		f.Add(seed, "red")
	}
	f.Fuzz(func(t *testing.T, text, color string) {
		for _, colored := range []bool{false, true} {
			got := MessageText(text, color, colored)
			body := got
			if colored && strings.HasPrefix(got, "\x1b[") && strings.HasSuffix(got, "\x1b[0m") {
				// The only escapes allowed are the trusted palette's own wrapper.
				open := strings.Index(got, "m")
				body = got[open+1 : len(got)-len("\x1b[0m")]
				if strings.Contains(got[:open+1], ";") || len(got[:open+1]) > 6 {
					t.Fatalf("untrusted color produced %q", got[:open+1])
				}
			}
			for _, r := range body {
				if r != '\n' && r != '\t' && (r < 0x20 || r == 0x7f || (r >= 0x80 && r < 0xa0)) {
					t.Fatalf("MessageText(%q, %q, %t) left control %U in %q", text, color, colored, r, got)
				}
			}
		}
	})
}
