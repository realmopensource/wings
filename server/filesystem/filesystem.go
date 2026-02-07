package filesystem

import (
	"bufio"
	"io"
	"io/ioutil"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"emperror.dev/errors"
	"github.com/gabriel-vasile/mimetype"
	ignore "github.com/sabhiram/go-gitignore"

	"github.com/pterodactyl/wings/config"
	"github.com/pterodactyl/wings/system"
)

type Filesystem struct {
	mu                sync.RWMutex
	lastLookupTime    *usageLookupTime
	lookupInProgress  *system.AtomicBool
	diskUsed          int64
	diskCheckInterval time.Duration
	denylist          *ignore.GitIgnore

	// The maximum amount of disk space (in bytes) that this Filesystem instance can use.
	diskLimit int64

	// The root data directory path for this Filesystem instance.
	root     *os.Root
	rootPath string

	isTest bool
}

// New creates a new Filesystem instance for a given server.
func New(path string, size int64, denylist []string) (*Filesystem, error) {
	r, err := os.OpenRoot(path)
	if err != nil {
		return nil, errors.WithStack(err)
	}

	fs := &Filesystem{
		root:              r,
		rootPath:          path,
		diskLimit:         size,
		diskCheckInterval: time.Duration(config.Get().System.DiskCheckInterval),
		lastLookupTime:    &usageLookupTime{},
		lookupInProgress:  system.NewAtomicBool(false),
		denylist:          ignore.CompileIgnoreLines(denylist...),
	}

	return fs, nil
}

// Path returns the root path for the Filesystem instance.
func (fs *Filesystem) Path() string {
	return fs.rootPath
}

// File returns a reader for a file instance as well as the stat information.
func (fs *Filesystem) File(p string) (*os.File, Stat, error) {
	p = strings.TrimLeft(filepath.Clean(p), "/")
	st, err := fs.Stat(p)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, Stat{}, newFilesystemError(ErrNotExist, err)
		}
		return nil, Stat{}, errors.WithStackIf(err)
	}
	if st.IsDir() {
		return nil, Stat{}, newFilesystemError(ErrCodeIsDirectory, nil)
	}
	f, err := fs.root.Open(p)
	if err != nil {
		return nil, Stat{}, errors.WithStackIf(err)
	}
	return f, st, nil
}

// Touch acts by creating the given file and path on the disk if it is not present
// already. If  it is present, the file is opened using the defaults which will truncate
// the contents. The opened file is then returned to the caller.
func (fs *Filesystem) Touch(p string, flag int) (*os.File, error) {
	p = strings.TrimLeft(filepath.Clean(p), "/")
	f, err := fs.root.OpenFile(p, flag, 0o644)
	if err == nil {
		return f, nil
	}
	if f != nil {
		_ = f.Close()
	}
	// If the error is not because it doesn't exist then we just need to bail at this point.
	if !errors.Is(err, os.ErrNotExist) {
		return nil, errors.Wrap(err, "server/filesystem: touch: failed to open file handle")
	}
	// Only create and chown the directory if it doesn't exist.
	if _, err := fs.root.Stat(filepath.Dir(p)); errors.Is(err, os.ErrNotExist) {
		// Create the path leading up to the file we're trying to create, setting the final perms
		// on it as we go.
		if err := fs.root.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return nil, errors.Wrap(err, "server/filesystem: touch: failed to create directory tree")
		}
		if err := fs.Chown(filepath.Dir(p)); err != nil {
			return nil, errors.Wrap(err, "server/filesystem: touch: failed to chown directory tree")
		}
	}
	o := &fileOpener{root: fs.root}
	// Try to open the file now that we have created the pathing necessary for it, and then
	// Chown that file so that the permissions don't mess with things.
	f, err = o.open(p, flag, 0o644)
	if err != nil {
		return nil, errors.Wrap(err, "server/filesystem: touch: failed to open file with wait")
	}
	_ = fs.Chown(p)
	return f, nil
}

// Writefile writes a file to the system. If the file does not already exist one
// will be created. This will also properly recalculate the disk space used by
// the server when writing new files or modifying existing ones.
func (fs *Filesystem) Writefile(p string, r io.Reader) error {
	p = strings.TrimLeft(filepath.Clean(p), "/")
	var currentSize int64
	// If the file does not exist on the system already go ahead and create the pathway
	// to it and an empty file. We'll then write to it later on after this completes.
	stat, err := fs.root.Stat(p)
	if err != nil && !os.IsNotExist(err) {
		return errors.Wrap(err, "server/filesystem: writefile: failed to stat file")
	} else if err == nil {
		if stat.IsDir() {
			return errors.WithStack(&Error{code: ErrCodeIsDirectory, resolved: stat.Name()})
		}
		currentSize = stat.Size()
	}

	br := bufio.NewReader(r)
	// Check that the new size we're writing to the disk can fit. If there is currently
	// a file we'll subtract that current file size from the size of the buffer to determine
	// the amount of new data we're writing (or amount we're removing if smaller).
	if err := fs.HasSpaceFor(int64(br.Size()) - currentSize); err != nil {
		return err
	}

	// Touch the file and return the handle to it at this point. This will create the file,
	// any necessary directories, and set the proper owner of the file.
	file, err := fs.Touch(p, os.O_RDWR|os.O_CREATE|os.O_TRUNC)
	if err != nil {
		return err
	}
	defer file.Close()

	buf := make([]byte, 1024*4)
	sz, err := io.CopyBuffer(file, r, buf)

	// Adjust the disk usage to account for the old size and the new size of the file.
	fs.addDisk(sz - currentSize)

	return fs.Chown(p)
}

// CreateDirectory creates a new directory ("name") at a specified path ("p") for the server.
func (fs *Filesystem) CreateDirectory(name string, p string) error {
	p = strings.TrimLeft(filepath.Clean(p), "/")
	return fs.root.MkdirAll(path.Join(p, name), 0o755)
}

// Rename moves (or renames) a file or directory.
func (fs *Filesystem) Rename(from string, to string) error {
	to = strings.TrimLeft(filepath.Clean(to), "/")
	from = strings.TrimLeft(filepath.Clean(from), "/")

	if from == "" || to == "" {
		return errors.New("server/filesystem: attempting to rename into an invalid directory space")
	}

	// If the target file or directory already exists the rename function will
	// fail, so just bail out now.
	if _, err := fs.root.Stat(to); err == nil {
		return os.ErrExist
	}

	d := strings.TrimLeft(filepath.Dir(to), "/")
	// Ensure that the directory we're moving into exists correctly on the system. Only do this if
	// we're not at the root directory level.
	if d != "" {
		if err := fs.root.MkdirAll(d, 0o755); err != nil {
			return errors.Wrap(err, "server/filesystem: failed to create directory tree")
		}
	}

	return fs.root.Rename(from, to)
}

// Begin looping up to 50 times to try and create a unique copy file name. This will take
// an input of "file.txt" and generate "file copy.txt". If that name is already taken, it will
// then try to write "file copy 2.txt" and so on, until reaching 50 loops. At that point we
// won't waste anymore time, just use the current timestamp and make that copy.
//
// Could probably make this more efficient by checking if there are any files matching the copy
// pattern, and trying to find the highest number and then incrementing it by one rather than
// looping endlessly.
func (fs *Filesystem) findCopySuffix(dir string, name string, extension string) (string, error) {
	var i int
	suffix := " copy"

	for i = 0; i < 51; i++ {
		if i > 0 {
			suffix = " copy " + strconv.Itoa(i)
		}

		n := name + suffix + extension
		// If we stat the file and it does not exist that means we're good to create the copy. If it
		// does exist, we'll just continue to the next loop and try again.
		if _, err := fs.Stat(path.Join(dir, n)); err != nil {
			if !errors.Is(err, os.ErrNotExist) {
				return "", err
			}

			break
		}

		if i == 50 {
			suffix = "copy." + time.Now().Format(time.RFC3339)
		}
	}

	return name + suffix + extension, nil
}

// Copies a given file to the same location and appends a suffix to the file to indicate that
// it has been copied.
func (fs *Filesystem) Copy(p string) error {
	p = strings.TrimLeft(filepath.Clean(p), "/")
	s, err := fs.root.Stat(p)
	if err != nil {
		return err
	} else if s.IsDir() || !s.Mode().IsRegular() {
		// If this is a directory or not a regular file, just throw a not-exist error
		// since anything calling this function should understand what that means.
		return os.ErrNotExist
	}

	// Check that copying this file wouldn't put the server over its limit.
	if err := fs.HasSpaceFor(s.Size()); err != nil {
		return err
	}

	base := filepath.Base(p)
	relative := strings.TrimSuffix(strings.TrimPrefix(p, fs.Path()), base)
	extension := filepath.Ext(base)
	name := strings.TrimSuffix(base, extension)

	// Ensure that ".tar" is also counted as apart of the file extension.
	// There might be a better way to handle this for other double file extensions,
	// but this is a good workaround for now.
	if strings.HasSuffix(name, ".tar") {
		extension = ".tar" + extension
		name = strings.TrimSuffix(name, ".tar")
	}

	source, err := fs.root.Open(p)
	if err != nil {
		return err
	}
	defer source.Close()

	n, err := fs.findCopySuffix(relative, name, extension)
	if err != nil {
		return err
	}

	return fs.Writefile(path.Join(relative, n), source)
}

// TruncateRootDirectory removes _all_ files and directories from a server's
// data directory and resets the used disk space to zero.
func (fs *Filesystem) TruncateRootDirectory() error {
	return errors.New("server/filesystem: not implemented")
}

// Delete removes a file or folder from the system. Prevents the user from
// accidentally (or maliciously) removing their root server data directory.
func (fs *Filesystem) Delete(p string) error {
	p = strings.TrimLeft(filepath.Clean(p), "/")
	if p == "" {
		return errors.New("server/filesystem: delete: cannot delete root directory")
	}

	st, err := fs.root.Lstat(p)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return errors.Wrap(err, "server/filesystem: delete: failed to stat file")
	}

	if st.IsDir() {
		if s, err := fs.DirectorySize(p); err == nil {
			fs.addDisk(-s)
		}
	} else {
		fs.addDisk(-st.Size())
	}

	return fs.root.RemoveAll(p)
}

type fileOpener struct {
	busy uint
	root *os.Root
}

// Attempts to open a given file up to "attempts" number of times, using a backoff. If the file
// cannot be opened because of a "text file busy" error, we will attempt until the number of attempts
// has been exhaused, at which point we will abort with an error.
func (fo *fileOpener) open(path string, flags int, perm os.FileMode) (*os.File, error) {
	for {
		f, err := fo.root.OpenFile(path, flags, perm)

		// If there is an error because the text file is busy, go ahead and sleep for a few
		// hundred milliseconds and then try again up to three times before just returning the
		// error back to the caller.
		//
		// Based on code from: https://github.com/golang/go/issues/22220#issuecomment-336458122
		if err != nil && fo.busy < 3 && strings.Contains(err.Error(), "text file busy") {
			time.Sleep(100 * time.Millisecond << fo.busy)
			fo.busy++
			continue
		}

		return f, err
	}
}

// ListDirectory lists the contents of a given directory and returns stat
// information about each file and folder within it.
func (fs *Filesystem) ListDirectory(p string) ([]Stat, error) {
	cleaned, err := fs.SafePath(p)
	if err != nil {
		return nil, err
	}

	files, err := ioutil.ReadDir(cleaned)
	if err != nil {
		return nil, err
	}

	var wg sync.WaitGroup

	// You must initialize the output of this directory as a non-nil value otherwise
	// when it is marshaled into a JSON object you'll just get 'null' back, which will
	// break the panel badly.
	out := make([]Stat, len(files))

	// Iterate over all of the files and directories returned and perform an async process
	// to get the mime-type for them all.
	for i, file := range files {
		wg.Add(1)

		go func(idx int, f os.FileInfo) {
			defer wg.Done()

			var m *mimetype.MIME
			d := "inode/directory"
			if !f.IsDir() {
				cleanedp := filepath.Join(cleaned, f.Name())
				if f.Mode()&os.ModeSymlink != 0 {
					cleanedp, _ = fs.SafePath(filepath.Join(cleaned, f.Name()))
				}

				// Don't try to detect the type on a pipe — this will just hang the application and
				// you'll never get a response back.
				//
				// @see https://github.com/pterodactyl/panel/issues/4059
				if cleanedp != "" && f.Mode()&os.ModeNamedPipe == 0 {
					m, _ = mimetype.DetectFile(filepath.Join(cleaned, f.Name()))
				} else {
					// Just pass this for an unknown type because the file could not safely be resolved within
					// the server data path.
					d = "application/octet-stream"
				}
			}

			st := Stat{FileInfo: f, Mimetype: d}
			if m != nil {
				st.Mimetype = m.String()
			}
			out[idx] = st
		}(i, file)
	}

	wg.Wait()

	// Sort the output alphabetically to begin with since we've run the output
	// through an asynchronous process and the order is gonna be very random.
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Name() == out[j].Name() || out[i].Name() > out[j].Name() {
			return true
		}
		return false
	})

	// Then, sort it so that directories are listed first in the output. Everything
	// will continue to be alphabetized at this point.
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].IsDir()
	})

	return out, nil
}
