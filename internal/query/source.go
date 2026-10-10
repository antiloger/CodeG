package query

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// SourceRequest asks for a range of lines of an indexed file.
type SourceRequest struct {
	File      string
	StartLine int // 1-based, inclusive
	EndLine   int // inclusive; 0 means StartLine
	Context   int // extra lines before and after
	// Highlight marks a range within the view; zero means StartLine..EndLine.
	Highlight *Location
}

type SourceLine struct {
	N         int    `json:"n"`
	Text      string `json:"text"`
	Highlight bool   `json:"highlight,omitempty"`
}

// SourceView is a plain snippet with the lines of interest marked.
type SourceView struct {
	Package   string       `json:"package"`
	File      string       `json:"file"`
	StartLine int          `json:"startLine"`
	EndLine   int          `json:"endLine"`
	Lines     []SourceLine `json:"lines"`
	Highlight *Location    `json:"highlight,omitempty"`
	// Stale means the file on disk no longer matches what was indexed: the line
	// numbers may be wrong. Re-index to refresh.
	Stale     bool `json:"stale"`
	Test      bool `json:"test,omitempty"`
	Generated bool `json:"generated,omitempty"`
	Truncated bool `json:"truncated,omitempty"`
}

const maxSourceLines = 400

// Source returns lines of an indexed file. Only files listed in the manifest
// are served: any other path, including one that escapes the module root, is
// reported as not found.
func (ix *Index) Source(req SourceRequest) (*SourceView, error) {
	file := path.Clean(strings.ReplaceAll(req.File, "\\", "/"))
	if file == "." || strings.HasPrefix(file, "/") || strings.HasPrefix(file, "../") || file == ".." {
		return nil, fmt.Errorf("%w: %s", ErrNotFound, req.File)
	}
	pkg, ok := ix.byFile[file]
	if !ok {
		return nil, fmt.Errorf("%w: %s is not an indexed file", ErrNotFound, req.File)
	}
	entry := ix.Manifest.Package(pkg)
	var indexed string
	for _, f := range entry.Files {
		if f.Path == file {
			indexed = f.Hash
		}
	}

	abs := filepath.Join(ix.Root, filepath.FromSlash(file))
	data, err := os.ReadFile(abs)
	if err != nil {
		return nil, fmt.Errorf("%w: %s can no longer be read (%v)", ErrNotFound, file, err)
	}
	sum := sha256.Sum256(data)
	v := &SourceView{Package: pkg, File: file, Stale: hex.EncodeToString(sum[:]) != indexed}

	if r, err := ix.Reader(pkg); err == nil {
		if files, err := r.Files(); err == nil {
			for _, f := range files {
				if f.Path == file {
					v.Test, v.Generated = f.Test, f.Generated
				}
			}
		}
	}

	lines := strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
	start, end := req.StartLine, req.EndLine
	if start < 1 {
		start = 1
	}
	if end < start {
		end = start
	}
	hlStart, hlEnd := start, end
	if req.Highlight != nil {
		hlStart, hlEnd = req.Highlight.Line, req.Highlight.EndLine
		if hlEnd < hlStart {
			hlEnd = hlStart
		}
		v.Highlight = req.Highlight
	}
	first, last := max(1, start-req.Context), min(len(lines), end+req.Context)
	if last-first+1 > maxSourceLines {
		last, v.Truncated = first+maxSourceLines-1, true
	}
	v.StartLine, v.EndLine = first, last
	for n := first; n <= last && n <= len(lines); n++ {
		v.Lines = append(v.Lines, SourceLine{N: n, Text: strings.TrimSuffix(lines[n-1], "\r"), Highlight: n >= hlStart && n <= hlEnd})
	}
	if v.Lines == nil {
		v.Lines = []SourceLine{}
	}
	return v, nil
}
