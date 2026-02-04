package filesystem

import "os"

// todo: vulnerable to race condition with symlinks
// see: https://pkg.go.dev/os#Root
func (fs *Filesystem) Chmod(path string, mode os.FileMode) error {
	cleaned, err := fs.SafePath(path)
	if err != nil {
		return err
	}

	if fs.isTest {
		return nil
	}

	if err := fs.root.Chmod(cleaned, mode); err != nil {
		return err
	}

	return nil
}
