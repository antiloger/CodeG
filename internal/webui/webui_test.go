package webui

import (
	"io/fs"
	"strings"
	"testing"
)

// The site always has an index.html: the built UI, or the placeholder that
// says how to build it.
func TestFSAlwaysHasAnIndex(t *testing.T) {
	data, err := fs.ReadFile(FS(), "index.html")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "codeg") {
		t.Errorf("index.html = %q", data)
	}
}

func TestPlaceholderExplainsHowToBuild(t *testing.T) {
	sub, err := fs.Sub(placeholder, "placeholder")
	if err != nil {
		t.Fatal(err)
	}
	data, err := fs.ReadFile(sub, "index.html")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "npm run build") {
		t.Errorf("placeholder does not say how to build the UI: %q", data)
	}
}
