package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// The filesystem, with one rule enforced in one place.
//
// Every request names a volume and a path *relative to that volume's root*.
// The root is never on the wire, and no other part of this app is allowed to
// build a path — resolve is the only function that turns a request into
// something os.* is called with, and it refuses anything that leaves the
// volume. That includes symlinks: the root is compared after resolution on both
// sides, so a link inside an EFS access point pointing at /etc is a refusal
// rather than a browse.
//
// The access point is already a jail below the pod, so this is the second fence
// rather than the only one. It exists anyway: an app that mounts two volumes
// must not let one of them address the other, and the kernel has no opinion
// about that.

var (
	errOutside  = errors.New("that path is outside the volume")
	errNotFound = errors.New("no such file or folder")
	errReadOnly = errors.New("this volume is mounted read-only")
)

// entry is one row of a listing.
type entry struct {
	Name     string    `json:"name"`
	Dir      bool      `json:"dir"`
	Size     int64     `json:"size"`
	Modified time.Time `json:"modified"`
	// Symlink entries are shown and never followed for their metadata. An
	// operator should be able to see that a link is there; whether it can be
	// opened is decided by resolve, at the moment it is asked for.
	Symlink bool `json:"symlink"`
	// Other is a socket, fifo or device node — things EFS can hold and this app
	// will not pretend to serve.
	Other bool `json:"other"`
}

// cleanRel normalizes a caller-supplied path to a slash-relative one.
//
// It is deliberately total: anything that cleans to an escape lands at the
// volume root instead of erroring, because "" , "/", "." and ".." all mean the
// same harmless thing to a person clicking a breadcrumb.
func cleanRel(p string) string {
	p = strings.TrimSpace(p)
	p = strings.ReplaceAll(p, "\\", "/")
	p = path.Clean("/" + p)
	return strings.TrimPrefix(p, "/")
}

// resolve turns (volume, relative path) into an absolute path inside the
// volume, or refuses.
//
// Two modes, and the difference is the final component:
//
//   - follow — what reading needs. The whole path is resolved, symlinks and
//     all, and the *result* must be inside the volume. A link pointing at
//     /etc/shadow is refused here rather than served.
//   - entry — what creating, deleting and renaming need. Only the parent is
//     resolved; the last segment is taken literally. Deleting a symlink must
//     delete the link, not whatever it points at, and creating a name cannot
//     resolve something that is not there yet.
//
// Either way the containment check is on a path with every symlink already
// resolved, which is the only version of it that means anything.
func resolve(v volume, rel string, follow bool) (string, error) {
	if !v.Browsable {
		return "", errors.New(v.Problem)
	}
	rel = cleanRel(rel)
	full := filepath.Join(v.root, filepath.FromSlash(rel))

	if follow {
		resolved, err := filepath.EvalSymlinks(full)
		if err != nil {
			if os.IsNotExist(err) {
				return "", errNotFound
			}
			return "", scrub(v, err)
		}
		if !within(v.root, resolved) {
			return "", errOutside
		}
		return resolved, nil
	}

	parent, err := filepath.EvalSymlinks(filepath.Dir(full))
	if err != nil {
		if os.IsNotExist(err) {
			return "", errNotFound
		}
		return "", scrub(v, err)
	}
	if !within(v.root, parent) {
		return "", errOutside
	}
	// cleanRel already removed every "..", so the last segment is one name and
	// cannot climb out of the parent that was just checked.
	return filepath.Join(parent, filepath.Base(full)), nil
}

// scrub keeps the volume's root off the wire. The path is not a secret — it is
// on the page — but an error is written for the person reading it, and
// "/mnt/lpv-reports-abc123/reports/q3.csv: permission denied" is not.
func scrub(v volume, err error) error {
	if err == nil {
		return nil
	}
	msg := err.Error()
	for _, prefix := range []string{v.root, v.Path} {
		if prefix != "" {
			msg = strings.ReplaceAll(msg, prefix+string(os.PathSeparator), "")
			msg = strings.ReplaceAll(msg, prefix, v.Name)
		}
	}
	return errors.New(msg)
}

func within(root, p string) bool {
	if p == root {
		return true
	}
	return strings.HasPrefix(p, root+string(os.PathSeparator))
}

// list reads one directory.
func list(v volume, rel string) ([]entry, error) {
	dir, err := resolve(v, rel, true)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(dir)
	if err != nil {
		return nil, scrub(v, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("%s is a file, not a folder", path.Base(cleanRel(rel)))
	}

	des, err := os.ReadDir(dir)
	if err != nil {
		return nil, scrub(v, err)
	}
	out := make([]entry, 0, len(des))
	for _, de := range des {
		e := entry{Name: de.Name()}
		switch {
		case de.Type()&os.ModeSymlink != 0:
			e.Symlink = true
			// A link's own target decides how it is shown, but never whether it
			// may be opened — that is resolve's call, later.
			if st, err := os.Stat(filepath.Join(dir, de.Name())); err == nil {
				e.Dir, e.Size, e.Modified = st.IsDir(), st.Size(), st.ModTime()
			}
		case de.IsDir():
			e.Dir = true
			if fi, err := de.Info(); err == nil {
				e.Modified = fi.ModTime()
			}
		case de.Type().IsRegular():
			if fi, err := de.Info(); err == nil {
				e.Size, e.Modified = fi.Size(), fi.ModTime()
			}
		default:
			e.Other = true
		}
		out = append(out, e)
	}

	// Folders first, then case-insensitive by name: the order a person expects,
	// and stable enough that a page refresh does not reshuffle the rows.
	sort.Slice(out, func(i, j int) bool {
		if out[i].Dir != out[j].Dir {
			return out[i].Dir
		}
		a, b := strings.ToLower(out[i].Name), strings.ToLower(out[j].Name)
		if a != b {
			return a < b
		}
		return out[i].Name < out[j].Name
	})
	return out, nil
}

// openFile returns a readable file and its metadata, refusing anything that is
// not a regular file — a fifo would hang the request forever, and a directory
// is not a download.
func openFile(v volume, rel string) (*os.File, os.FileInfo, error) {
	full, err := resolve(v, rel, true)
	if err != nil {
		return nil, nil, err
	}
	info, err := os.Stat(full)
	if err != nil {
		return nil, nil, scrub(v, err)
	}
	if info.IsDir() {
		return nil, nil, errors.New("that is a folder, not a file")
	}
	if !info.Mode().IsRegular() {
		return nil, nil, errors.New("that is not a regular file")
	}
	f, err := os.Open(full)
	if err != nil {
		return nil, nil, scrub(v, err)
	}
	return f, info, nil
}

// validName is the check every created or renamed name goes through. It is
// about the name, not the path: a separator here is a caller trying to move
// something while claiming to name it.
func validName(name string) error {
	switch {
	case name == "":
		return errors.New("a name is required")
	case name == "." || name == "..":
		return errors.New("that name is reserved")
	case strings.ContainsAny(name, "/\\"):
		return errors.New("a name cannot contain a slash")
	case strings.ContainsRune(name, 0):
		return errors.New("a name cannot contain a null byte")
	case len(name) > 255:
		return errors.New("that name is too long for the filesystem (255 bytes)")
	}
	return nil
}

// mkdir creates one folder inside dir.
func mkdir(v volume, dir, name string) error {
	if !v.Writable() {
		return errReadOnly
	}
	if err := validName(name); err != nil {
		return err
	}
	full, err := resolve(v, path.Join(cleanRel(dir), name), false)
	if err != nil {
		return err
	}
	if _, err := os.Lstat(full); err == nil {
		return errors.New(name + " already exists")
	}
	return scrub(v, os.Mkdir(full, 0o775))
}

// save writes an uploaded stream to dir/name.
//
// It streams: an EFS volume is sized in terabytes and the pod is not, so the
// body never lands in memory. overwrite is explicit because a browser upload of
// a name that already exists is the one destructive act a person does by
// accident.
func save(v volume, dir, name string, src io.Reader, limit int64, overwrite bool) (int64, error) {
	if !v.Writable() {
		return 0, errReadOnly
	}
	if err := validName(name); err != nil {
		return 0, err
	}
	full, err := resolve(v, path.Join(cleanRel(dir), name), false)
	if err != nil {
		return 0, err
	}
	flags := os.O_WRONLY | os.O_CREATE | os.O_EXCL
	if overwrite {
		flags = os.O_WRONLY | os.O_CREATE | os.O_TRUNC
	}
	f, err := os.OpenFile(full, flags, 0o664)
	if err != nil {
		if os.IsExist(err) {
			return 0, errors.New(name + " already exists")
		}
		return 0, scrub(v, err)
	}

	// One byte past the limit is enough to know: read it, and if it arrives the
	// file is removed rather than left truncated at the cap.
	n, copyErr := io.Copy(f, io.LimitReader(src, limit+1))
	closeErr := f.Close()
	switch {
	case copyErr != nil:
		_ = os.Remove(full)
		return 0, scrub(v, copyErr)
	case n > limit:
		_ = os.Remove(full)
		return 0, fmt.Errorf("%s is larger than the %s upload limit", name, humanBytes(limit))
	case closeErr != nil:
		_ = os.Remove(full)
		return 0, scrub(v, closeErr)
	}
	return n, nil
}

// remove deletes one file or folder. A non-empty folder needs recursive, which
// the UI asks for in a second sentence rather than a second click.
func remove(v volume, rel string, recursive bool) error {
	if !v.Writable() {
		return errReadOnly
	}
	if cleanRel(rel) == "" {
		return errors.New("the volume root cannot be deleted")
	}
	full, err := resolve(v, rel, false)
	if err != nil {
		return err
	}
	info, err := os.Lstat(full)
	if err != nil {
		if os.IsNotExist(err) {
			return errNotFound
		}
		return scrub(v, err)
	}
	if info.IsDir() && recursive {
		return scrub(v, os.RemoveAll(full))
	}
	if err := os.Remove(full); err != nil {
		if info.IsDir() {
			return errors.New("that folder is not empty")
		}
		return scrub(v, err)
	}
	return nil
}

// rename moves or renames within one volume. Both ends go through resolve, so
// neither can point out of it, and EFS gives us a real atomic rename — which is
// exactly what an object store could not, and why stores are not browsable
// here.
func rename(v volume, from, toDir, toName string) error {
	if !v.Writable() {
		return errReadOnly
	}
	if cleanRel(from) == "" {
		return errors.New("the volume root cannot be renamed")
	}
	if err := validName(toName); err != nil {
		return err
	}
	src, err := resolve(v, from, false)
	if err != nil {
		return err
	}
	if _, err := os.Lstat(src); err != nil {
		if os.IsNotExist(err) {
			return errNotFound
		}
		return scrub(v, err)
	}
	dst, err := resolve(v, path.Join(cleanRel(toDir), toName), false)
	if err != nil {
		return err
	}
	if src == dst {
		return nil
	}
	if _, err := os.Lstat(dst); err == nil {
		return errors.New(toName + " already exists")
	}
	return scrub(v, os.Rename(src, dst))
}

func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}
