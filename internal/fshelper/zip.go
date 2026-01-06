// Package fshelper provides filesystem utilities for reading ZIP files.
// This is a simplified version inspired by github.com/simulot/immich-go/internal/fshelper
package fshelper

import (
	"archive/zip"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// ZipFS wraps a zip.Reader to implement fs.FS.
type ZipFS struct {
	*zip.Reader
	file *os.File
	name string
}

// OpenZip opens a ZIP file and returns it as an fs.FS.
func OpenZip(path string) (*ZipFS, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}

	stat, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, err
	}

	r, err := zip.NewReader(f, stat.Size())
	if err != nil {
		f.Close()
		return nil, err
	}

	name := filepath.Base(path)
	name = strings.TrimSuffix(name, filepath.Ext(name))

	return &ZipFS{
		Reader: r,
		file:   f,
		name:   name,
	}, nil
}

// Close closes the underlying file.
func (z *ZipFS) Close() error {
	return z.file.Close()
}

// Name returns the base name of the ZIP file (without extension).
func (z *ZipFS) Name() string {
	return z.name
}

// Open implements fs.FS.
func (z *ZipFS) Open(name string) (fs.File, error) {
	return z.Reader.Open(name)
}

// ParsePaths parses a list of paths and returns fs.FS instances.
// Supports ZIP files and glob patterns.
func ParsePaths(paths []string) ([]fs.FS, error) {
	var result []fs.FS

	for _, p := range paths {
		// Expand glob patterns
		matches, err := filepath.Glob(p)
		if err != nil {
			return nil, err
		}
		if len(matches) == 0 {
			// No glob match, treat as literal path
			matches = []string{p}
		}

		for _, match := range matches {
			lower := strings.ToLower(match)
			if strings.HasSuffix(lower, ".zip") {
				zfs, err := OpenZip(match)
				if err != nil {
					return nil, err
				}
				result = append(result, zfs)
			} else {
				// For directories, use os.DirFS
				result = append(result, os.DirFS(match))
			}
		}
	}

	return result, nil
}

// CloseFSs closes all fs.FS instances that have a Close method.
func CloseFSs(fsyss []fs.FS) error {
	var lastErr error
	for _, fsys := range fsyss {
		if closer, ok := fsys.(interface{ Close() error }); ok {
			if err := closer.Close(); err != nil {
				lastErr = err
			}
		}
	}
	return lastErr
}
