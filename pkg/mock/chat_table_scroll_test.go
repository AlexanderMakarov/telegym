package mock

import (
	"strings"
	"testing"
)

func TestWrapTableLikeRegions_scrollsOnlyAlignedColumns(t *testing.T) {
	in := "Intro prose about nouns.\n\n" +
		"Singular                      Plural\n" +
		"դաս                            դասեր\n" +
		"\n" +
		"More prose after the table."
	out := wrapTableLikeRegions(in, true)
	if !strings.Contains(out, `<div class="msg-hscroll"><pre>`) {
		t.Fatalf("want hscroll pre around table: %s", out)
	}
	if !strings.Contains(out, "Singular                      Plural\nդաս                            դասեր") {
		t.Fatalf("table lines must stay in pre: %s", out)
	}
	if strings.Count(out, "msg-hscroll") != 1 {
		t.Fatalf("want exactly one hscroll region: %s", out)
	}
	if !strings.Contains(out, "Intro prose") || !strings.Contains(out, "More prose") {
		t.Fatalf("prose must remain outside pre: %s", out)
	}
	// Prose must not sit inside the only pre block.
	preStart := strings.Index(out, "<pre>")
	preEnd := strings.Index(out, "</pre>")
	pre := out[preStart:preEnd]
	if strings.Contains(pre, "Intro prose") || strings.Contains(pre, "More prose") {
		t.Fatalf("prose leaked into pre: %s", pre)
	}
}

func TestWrapTableLikeRegions_singleColumnLineNotWrapped(t *testing.T) {
	in := "Only one wide   line here"
	out := wrapTableLikeRegions(in, true)
	if strings.Contains(out, "msg-hscroll") {
		t.Fatalf("single line must not become hscroll: %s", out)
	}
}
