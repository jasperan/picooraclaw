package tools

import (
	"math/rand"
	"regexp"
	"testing"
)

// The hand-written HTML helpers must match the regexps they replaced.
func TestHTMLHelpersMatchRegexp(t *testing.T) {
	scriptRe := regexp.MustCompile(`<script[\s\S]*?</script>`)
	tagRe := regexp.MustCompile(`<[^>]+>`)
	spaceRe := regexp.MustCompile(`\s+`)

	cases := []string{
		"", "plain", "<", ">", "<>", "<<>>", "a<>b", "<a<b>c", "x<y", "<p>hi</p>",
		"<script>a</script>b<script>c", "<script>x", "<scripts>y</script>z",
		"</script><script>", " \t\n\f\r\vx  y\u00a0 ", benchHTMLPage(),
	}
	alphabet := []string{"<", ">", "a", " ", "\n", "<script", "</script>", "\t", "é"}
	r := rand.New(rand.NewSource(1))
	for n := 0; n < 2000; n++ {
		var s string
		for k := r.Intn(20); k > 0; k-- {
			s += alphabet[r.Intn(len(alphabet))]
		}
		cases = append(cases, s)
	}

	for _, c := range cases {
		if got, want := removeBlocks(c, "<script", "</script>"), scriptRe.ReplaceAllLiteralString(c, ""); got != want {
			t.Fatalf("removeBlocks(%q) = %q, want %q", c, got, want)
		}
		if got, want := removeTags(c), tagRe.ReplaceAllLiteralString(c, ""); got != want {
			t.Fatalf("removeTags(%q) = %q, want %q", c, got, want)
		}
		if got, want := collapseSpace(c), spaceRe.ReplaceAllLiteralString(c, " "); got != want {
			t.Fatalf("collapseSpace(%q) = %q, want %q", c, got, want)
		}
	}
}
