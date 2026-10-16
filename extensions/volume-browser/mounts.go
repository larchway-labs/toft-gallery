package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

// Discovery.
//
// Toft declares what is attached in TOFT_STORAGE: a JSON array of
// {name, kind, path, access}, written by the same code that built the pod's
// volume mounts, so the description and the filesystem cannot disagree. That is
// the only door this app uses. It never guesses a path, never scans /mnt, and
// never treats a directory it happens to find as storage — a mount an
// administrator did not attach is not this app's to show.
//
// Two kinds can appear. A "volume" is an EFS access point: full POSIX, which is
// what makes a file browser meaningful. A "store" is an S3 prefix through
// Mountpoint, where there is no rename, no append and no directory — the
// operations this app offers would half-work, and half-working file management
// is worse than none. So stores are listed as present and explicitly not
// browsable, rather than silently dropped.

const envStorage = "TOFT_STORAGE"

const (
	kindVolume = "volume"
	kindStore  = "store"

	accessRead  = "read"
	accessWrite = "write"
)

// descriptor is one entry of TOFT_STORAGE, exactly as the platform writes
// it.
type descriptor struct {
	Name   string `json:"name"`
	Kind   string `json:"kind"`
	Path   string `json:"path"`
	Access string `json:"access"`
}

// volume is one attached EFS folder, as this app needs it: the declaration plus
// what the filesystem says about it right now.
type volume struct {
	Name      string `json:"name"`
	Kind      string `json:"kind"`
	Path      string `json:"path"`
	Access    string `json:"access"`
	Browsable bool   `json:"browsable"`
	// Problem is empty when the mount is usable. When it is not, it says why in
	// a sentence an operator can act on — a declared mount that is missing from
	// the filesystem is a misconfiguration to surface, never a volume to hide.
	Problem string `json:"problem,omitempty"`

	root string // Path with symlinks resolved; the containment boundary.
}

// Writable reports whether this app may modify the volume. Access is the
// platform's word on it and is checked first, so a refusal reads as "mounted
// read-only" rather than as an EROFS from the kernel three layers down.
func (v volume) Writable() bool { return v.Browsable && v.Access != accessRead && v.Access != "" }

// mountSet is the discovered set, indexed by name. Names are the resource
// slugs, and the platform holds them unique.
type mountSet struct {
	mu     sync.RWMutex
	list   []volume
	byName map[string]volume
}

// discover reads TOFT_STORAGE and stats every declared path.
//
// An unparseable value is an error rather than an empty set: an app that cannot
// read its own contract should say so at boot, not present itself as an install
// with no storage attached.
func discover(raw string) (*mountSet, error) {
	set := &mountSet{byName: map[string]volume{}}
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return set, nil
	}

	var descs []descriptor
	if err := json.Unmarshal([]byte(raw), &descs); err != nil {
		return nil, errors.New(envStorage + " is not the JSON array the platform writes: " + err.Error())
	}

	for _, d := range descs {
		v := volume{Name: d.Name, Kind: d.Kind, Path: d.Path, Access: d.Access}
		if v.Access == "" {
			v.Access = accessRead
		}
		switch {
		case d.Kind != kindVolume:
			v.Problem = "This is an object store (S3 through Mountpoint), not an EFS volume. " +
				"Mountpoint has no rename, no append and no real directories, so browsing it here " +
				"would only half work. Use the platform's own store browser instead."
		default:
			v.Browsable, v.Problem, v.root = probe(d.Path)
		}
		set.list = append(set.list, v)
		set.byName[v.Name] = v
	}

	// Usable mounts first, then by name. A store or a missing mount is worth
	// showing — it is how an operator learns the mapping did not land — but it
	// is not what someone came here to click, and the first browsable volume is
	// the one the page opens on.
	sort.Slice(set.list, func(i, j int) bool {
		if set.list[i].Browsable != set.list[j].Browsable {
			return set.list[i].Browsable
		}
		return set.list[i].Name < set.list[j].Name
	})
	return set, nil
}

// probe answers what the filesystem says about a declared mount path.
func probe(path string) (browsable bool, problem string, root string) {
	if path == "" {
		return false, "The platform declared this mount with no path.", ""
	}
	info, err := os.Stat(path)
	switch {
	case os.IsNotExist(err):
		return false, "Declared at " + path + ", but nothing is there. The mapping was probably " +
			"added after this app last started — a mapping change takes effect on the next deploy.", ""
	case err != nil:
		return false, "Cannot read " + path + ": " + err.Error(), ""
	case !info.IsDir():
		return false, path + " is not a directory.", ""
	}
	// The jail is the resolved path, so a symlink inside the volume cannot be
	// followed out of it by comparing against a prefix that is itself a link.
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return false, "Cannot resolve " + path + ": " + err.Error(), ""
	}
	return true, "", resolved
}

func (s *mountSet) all() []volume {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]volume, len(s.list))
	copy(out, s.list)
	return out
}

func (s *mountSet) get(name string) (volume, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	v, ok := s.byName[name]
	return v, ok
}

// refresh re-stats every declared mount without re-reading the environment.
//
// The declaration cannot change while the process runs — a mapping change
// takes effect on the next deployment, which is a new process — but what is
// *behind* it can: an EFS mount that was not ready at boot becomes ready, and
// an operator watching this page should see that without a restart.
func (s *mountSet) refresh() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, v := range s.list {
		if v.Kind != kindVolume {
			continue
		}
		v.Browsable, v.Problem, v.root = probe(v.Path)
		s.list[i] = v
		s.byName[v.Name] = v
	}
}

// browsableCount is what the empty state needs to phrase itself: no mounts at
// all and mounts that are all unusable are different problems.
func (s *mountSet) browsableCount() int {
	n := 0
	for _, v := range s.all() {
		if v.Browsable {
			n++
		}
	}
	return n
}

// forceReadOnly downgrades every mount's access to read.
//
// It is the install's switch, not the platform's: an administrator who mounts a
// volume read-write for the app that owns the data may still want this browser
// beside it to look only. Applied at discovery so there is one place writes are
// decided — Writable — rather than a second check every handler could forget.
func (s *mountSet) forceReadOnly() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, v := range s.list {
		v.Access = accessRead
		s.list[i] = v
		s.byName[v.Name] = v
	}
}
