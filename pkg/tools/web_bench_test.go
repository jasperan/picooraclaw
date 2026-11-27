package tools

import (
	"fmt"
	"strings"
	"testing"
)

func benchHTMLPage() string {
	var sb strings.Builder
	sb.WriteString("<html><head><title>Bench</title><style>body{color:red}</style>")
	sb.WriteString("<script>var x = 1; function f(){ return x; }</script></head><body>")
	for i := 0; i < 50; i++ {
		fmt.Fprintf(&sb, "<div class=\"p\"><h2>Section %d</h2><p>Some <b>bold</b> and <i>italic</i> text with a <a href=\"/l/%d\">link</a>.</p></div>\n", i, i)
	}
	sb.WriteString("</body></html>")
	return sb.String()
}

func benchDDGPage() string {
	var sb strings.Builder
	sb.WriteString("<html><body>")
	for i := 0; i < 10; i++ {
		fmt.Fprintf(&sb, "<div class=\"result\"><a rel=\"nofollow\" class=\"result__a\" href=\"//duckduckgo.com/l/?uddg=https%%3A%%2F%%2Fexample.com%%2F%d\">Result <b>%d</b></a>\n", i, i)
		fmt.Fprintf(&sb, "<a class=\"result__snippet\" href=\"x\">Snippet <b>number</b> %d</a></div>\n", i)
	}
	sb.WriteString("</body></html>")
	return sb.String()
}

func BenchmarkWebFetchExtractText(b *testing.B) {
	tool := &WebFetchTool{}
	page := benchHTMLPage()
	b.ReportAllocs()
	for b.Loop() {
		_ = tool.extractText(page)
	}
}

func BenchmarkDuckDuckGoExtractResults(b *testing.B) {
	p := &DuckDuckGoSearchProvider{}
	page := benchDDGPage()
	b.ReportAllocs()
	for b.Loop() {
		if _, err := p.extractResults(page, 5, "bench"); err != nil {
			b.Fatal(err)
		}
	}
}
