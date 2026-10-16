package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The tests worth having here are about the fence, not the plumbing: every one
// of these is a way out of the volume, and a file browser that has one is not a
// file browser.

func volumeAt(t *testing.T, dir, access string) volume {
	t.Helper()
	browsable, problem, root := probe(dir)
	if !browsable {
		t.Fatalf("probe(%q): %s", dir, problem)
	}
	return volume{Name: "data", Kind: kindVolume, Path: dir, Access: access, Browsable: true, root: root}
}

func TestPathsCannotLeaveTheVolume(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "secret.txt")
	if err := os.WriteFile(outside, []byte("not yours"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "inside.txt"), []byte("fine"), 0o600); err != nil {
		t.Fatal(err)
	}
	v := volumeAt(t, root, accessWrite)

	// Every one of these cleans to the root or below it; none may reach the
	// file next door.
	for _, p := range []string{"../secret.txt", "..%2Fsecret.txt", "/../../secret.txt", "a/../../secret.txt", "....//secret.txt"} {
		if _, _, err := openFile(v, p); err == nil {
			t.Errorf("openFile(%q) succeeded; it must not leave the volume", p)
		}
	}

	// A symlink is the interesting one: the path never contains "..", so only
	// resolving both ends catches it.
	if err := os.Symlink(outside, filepath.Join(root, "escape")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if _, _, err := openFile(v, "escape"); err == nil {
		t.Error("a symlink out of the volume was followed")
	}
	// Writing *through* a link is the other half: the parent resolves outside
	// the volume, so nothing lands there.
	if _, err := save(v, "escape", "planted.txt", strings.NewReader("x"), 1<<10, false); err == nil {
		t.Error("an upload through a symlink left the volume")
	}
	if _, err := list(v, "escape"); err == nil {
		t.Error("listing through a symlink left the volume")
	}
	// Deleting the link must delete the link, never what it points at.
	if err := remove(v, "escape", false); err != nil {
		t.Errorf("removing the link itself: %v", err)
	}
	if _, err := os.Stat(outside); err != nil {
		t.Errorf("deleting a symlink must leave its target alone: %v", err)
	}
}

func TestReadOnlyVolumeRefusesEveryWrite(t *testing.T) {
	root := t.TempDir()
	v := volumeAt(t, root, accessRead)

	if err := mkdir(v, "", "nope"); err != errReadOnly {
		t.Errorf("mkdir on a read-only volume: %v", err)
	}
	if _, err := save(v, "", "nope.txt", strings.NewReader("x"), 1<<20, false); err != errReadOnly {
		t.Errorf("save on a read-only volume: %v", err)
	}
	if err := remove(v, "anything", false); err != errReadOnly {
		t.Errorf("remove on a read-only volume: %v", err)
	}
	if err := rename(v, "a", "", "b"); err != errReadOnly {
		t.Errorf("rename on a read-only volume: %v", err)
	}
	if entries, err := list(v, ""); err != nil || len(entries) != 0 {
		t.Errorf("a read-only volume must still be readable: %v %v", entries, err)
	}
}

func TestTheOrdinaryOperations(t *testing.T) {
	root := t.TempDir()
	v := volumeAt(t, root, accessWrite)

	if err := mkdir(v, "", "reports"); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := mkdir(v, "", "reports"); err == nil {
		t.Error("mkdir twice must refuse the second")
	}
	n, err := save(v, "reports", "q3.csv", strings.NewReader("a,b\n1,2\n"), 1<<20, false)
	if err != nil || n != 8 {
		t.Fatalf("save: %d %v", n, err)
	}
	if _, err := save(v, "reports", "q3.csv", strings.NewReader("again"), 1<<20, false); err == nil {
		t.Error("an upload over an existing name must refuse without overwrite")
	}
	if _, err := save(v, "reports", "q3.csv", strings.NewReader("again"), 1<<20, true); err != nil {
		t.Errorf("overwrite: %v", err)
	}
	if err := rename(v, "reports/q3.csv", "reports", "q3-final.csv"); err != nil {
		t.Fatalf("rename: %v", err)
	}
	entries, err := list(v, "reports")
	if err != nil || len(entries) != 1 || entries[0].Name != "q3-final.csv" {
		t.Fatalf("list after rename: %+v %v", entries, err)
	}
	if err := remove(v, "reports", false); err == nil {
		t.Error("a non-empty folder must not delete without the recursive flag")
	}
	if err := remove(v, "reports", true); err != nil {
		t.Fatalf("recursive remove: %v", err)
	}
	if entries, _ := list(v, ""); len(entries) != 0 {
		t.Errorf("volume should be empty: %+v", entries)
	}
}

func TestFoldersSortFirst(t *testing.T) {
	root := t.TempDir()
	v := volumeAt(t, root, accessWrite)
	for _, name := range []string{"b.txt", "A.txt"} {
		if _, err := save(v, "", name, strings.NewReader("x"), 1<<10, false); err != nil {
			t.Fatal(err)
		}
	}
	if err := mkdir(v, "", "zeta"); err != nil {
		t.Fatal(err)
	}
	got, err := list(v, "")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"zeta", "A.txt", "b.txt"}
	for i, name := range want {
		if got[i].Name != name {
			t.Fatalf("order = %v, want %v", names(got), want)
		}
	}
}

func names(es []entry) []string {
	out := make([]string, len(es))
	for i, e := range es {
		out[i] = e.Name
	}
	return out
}

func TestUploadLimitLeavesNoPartialFile(t *testing.T) {
	root := t.TempDir()
	v := volumeAt(t, root, accessWrite)
	if _, err := save(v, "", "big.bin", strings.NewReader(strings.Repeat("x", 100)), 10, false); err == nil {
		t.Fatal("a file over the limit must be refused")
	}
	if entries, _ := list(v, ""); len(entries) != 0 {
		t.Errorf("a refused upload must leave nothing behind: %v", names(entries))
	}
}

func TestNamesAreCheckedNotPaths(t *testing.T) {
	root := t.TempDir()
	v := volumeAt(t, root, accessWrite)
	for _, name := range []string{"", ".", "..", "a/b", "a\\b", strings.Repeat("n", 300)} {
		if err := mkdir(v, "", name); err == nil {
			t.Errorf("mkdir accepted the name %q", name)
		}
	}
}

// Discovery: the platform's declaration is the only door, and what it says
// about a mount is not the same question as what the filesystem says.
func TestDiscoveryReadsThePlatformsDeclaration(t *testing.T) {
	dir := t.TempDir()
	raw, err := json.Marshal([]descriptor{
		{Name: "reports", Kind: kindVolume, Path: dir, Access: accessWrite},
		{Name: "gone", Kind: kindVolume, Path: filepath.Join(dir, "nothing-here"), Access: accessWrite},
		{Name: "archive", Kind: kindStore, Path: "/mnt/store", Access: accessRead},
	})
	if err != nil {
		t.Fatal(err)
	}
	set, err := discover(string(raw))
	if err != nil {
		t.Fatalf("discover: %v", err)
	}
	if set.browsableCount() != 1 {
		t.Errorf("browsable = %d, want 1", set.browsableCount())
	}
	missing, _ := set.get("gone")
	if missing.Browsable || missing.Problem == "" {
		t.Error("a declared mount that is not on the filesystem must be shown with a reason, not hidden")
	}
	store, _ := set.get("archive")
	if store.Browsable || !strings.Contains(store.Problem, "object store") {
		t.Errorf("a store must be listed and refused, got %+v", store)
	}
	if _, err := discover("{not json"); err == nil {
		t.Error("an unreadable contract must fail the boot, not present as no storage")
	}
	empty, err := discover("")
	if err != nil || len(empty.all()) != 0 {
		t.Errorf("absent storage is an empty set, not an error: %v %v", empty, err)
	}
}

func TestReadOnlySwitchOverridesAccess(t *testing.T) {
	dir := t.TempDir()
	raw, _ := json.Marshal([]descriptor{{Name: "data", Kind: kindVolume, Path: dir, Access: accessWrite}})
	set, err := discover(string(raw))
	if err != nil {
		t.Fatal(err)
	}
	set.forceReadOnly()
	v, _ := set.get("data")
	if v.Writable() {
		t.Error("READ_ONLY must make every volume read-only")
	}
}
