package web

import (
	"bytes"
	"embed"
	"errors"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"runtime"
)

// Generate mock distributive files, if needed.
//go:generate go run generate_dist_stub.go

//go:embed dist
var content embed.FS

// Dist returns frontend distributive files. If live is true, it returns files from the dist directory, otherwise
// from the embedded content. Live might be useful for development purposes.
func Dist(live bool) fs.FS {
	const distDirName = "dist"

	if live {
		// get the current file path (to resolve the dist directory path later)
		_, filePath, _, ok := runtime.Caller(0)
		if !ok {
			return noFs("unable to get the current file path")
		}

		return os.DirFS(path.Join(filepath.Dir(filePath), distDirName))
	} else {
		data, err := fs.Sub(content, distDirName)
		if err != nil {
			return noFs("dist directory not found")
		}

		return data
	}
}

// stubMarker is written by generate_dist_stub.go. It lets the server warn instead of
// silently serving an empty page after a fresh clone - which otherwise reads as "the
// project is broken" to anyone who just ran the first command in the README.
const stubMarker = "前端尚未构建"

// Stubbed reports whether the distributive is the generated placeholder instead of a
// real build.
func Stubbed(fsys fs.FS) bool {
	data, err := fs.ReadFile(fsys, "index.html")
	if err != nil {
		return true
	}

	return bytes.Contains(data, []byte(stubMarker))
}

// noFs is a mock fs.FS implementation, which returns an error on Open.
type noFs string

var _ fs.FS = (*noFs)(nil) // verify that noFs implements fs.FS

func (fs noFs) Open(string) (fs.File, error) { return nil, errors.New("web/dist: " + string(fs)) }
