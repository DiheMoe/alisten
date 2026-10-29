package api

import "testing"

func TestBookTitle(t *testing.T) {
	for title, want := range map[string]string{
		"晴天": "《晴天》",
		"日语填词《5:20AM》（完整版）": "日语填词《5:20AM》（完整版）",
	} {
		if got := bookTitle(title); got != want {
			t.Errorf("bookTitle(%q) = %q，期望 %q", title, got, want)
		}
	}
}
