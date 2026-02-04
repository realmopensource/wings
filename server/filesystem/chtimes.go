package filesystem

import "time"

// todo: vulnerable to race condition with symlinks
// see: https://pkg.go.dev/os#Root
func (fs *Filesystem) Chtimes(path string, atime, mtime time.Time) error {
	cleaned, err := fs.SafePath(path)
	if err != nil {
		return err
	}

	if fs.isTest {
		return nil
	}

	if err := fs.root.Chtimes(cleaned, atime, mtime); err != nil {
		return err
	}

	return nil
}
