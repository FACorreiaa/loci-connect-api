package poi

import "testing"

func TestCategoryWordsPattern(t *testing.T) {
	if got := categoryWordsPattern(nil); got != "" {
		t.Fatalf("no words must not narrow, got %q", got)
	}
	if got := categoryWordsPattern([]string{" ", ""}); got != "" {
		t.Fatalf("blank words must not narrow, got %q", got)
	}
	got := categoryWordsPattern([]string{"Bar", "cafe", "bar", " c.d "})
	want := `\m(bar|cafe|c\.d)s?\M`
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}
