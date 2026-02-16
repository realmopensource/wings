package filesystem

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"emperror.dev/errors"
)

// CompressFiles compresses all the files matching the given paths in the
// specified directory. This function also supports passing nested paths to only
// compress certain files and folders when working in a larger directory. This
// effectively creates a local backup, but rather than ignoring specific files
// and folders, it takes an allowlist of files and folders.
//
// All paths are relative to the dir that is passed in as the first argument,
// and the compressed file will be placed at that location named
// `archive-{date}.tar.gz`.
func (fs *Filesystem) CompressFiles(ctx context.Context, dir string, paths []string) (os.FileInfo, error) {
	r, err := fs.root.OpenRoot(normalize(dir))
	if err != nil {
		return nil, errors.Wrap(err, "server/filesystem: compress: failed to open root directory")
	}
	a, err := NewArchive(r, nil, WithMatching(paths))
	if err != nil {
		_ = r.Close()
		return nil, errors.WrapIf(err, "server/filesystem: compress: failed to create archive instance")
	}
	defer a.Close()

	n := fmt.Sprintf("archive-%s.tar.gz", strings.ReplaceAll(time.Now().Format(time.RFC3339), ":", ""))
	f, err := r.OpenFile(n, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return nil, errors.Wrap(err, "server/filesystem: compress: failed to open file for writing")
	}
	defer f.Close()

	if err := a.Create(ctx, f); err != nil {
		return nil, errors.Wrap(err, "server/filesystem: compress: failed to write to disk")
	}

	// todo: disk space
	return f.Stat()
}

// SpaceAvailableForDecompression looks through a given archive and determines
// if decompressing it would put the server over its allocated disk space limit.
func (fs *Filesystem) SpaceAvailableForDecompression(ctx context.Context, dir string, file string) error {
	return nil
}

// DecompressFile will decompress a file in a given directory by using the
// archiver tool to infer the file type and go from there. This will walk over
// all the files within the given archive and ensure that there is not a
// zip-slip attack being attempted by validating that the final path is within
// the server data directory.
func (fs *Filesystem) DecompressFile(ctx context.Context, dir string, file string) error {
	return errors.New("server/fs: not implemented")
}

// ExtractStreamUnsafe .
func (fs *Filesystem) ExtractStreamUnsafe(ctx context.Context, dir string, r io.Reader) error {
	return errors.New("server/fs: not implemented")
}
