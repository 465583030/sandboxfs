// Copyright 2017 Google Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License"); you may not
// use this file except in compliance with the License.  You may obtain a copy
// of the License at:
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS, WITHOUT
// WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.  See the
// License for the specific language governing permissions and limitations
// under the License.

package integration

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"syscall"
	"testing"
	"time"

	"github.com/bazelbuild/sandboxfs/integration/utils"
	"github.com/bazelbuild/sandboxfs/internal/sandbox"
)

// The tests in this file verify the read/write mapping.  In principle, they should ensure that the
// mapping is fully-functional, including in its read-only operations.  However, as we know that
// read/write mappings are implemented in the same way as read-only mappings, we "cheat" and only
// test here for the write-specific behaviors.

func TestReadWrite_CreateFile(t *testing.T) {
	state := utils.MountSetup(t, "static", "-read_write_mapping=/:%ROOT%")
	defer state.TearDown(t)

	utils.MustWriteFile(t, state.RootPath("file"), 0644, "original content")
	utils.MustMkdirAll(t, state.RootPath("subdir"), 0755)
	utils.MustWriteFile(t, state.MountPath("subdir/file"), 0644, "new content")

	if err := utils.FileEquals(state.MountPath("file"), "original content"); err != nil {
		t.Error(err)
	}
	if err := utils.FileEquals(state.MountPath("subdir/file"), "new content"); err != nil {
		t.Error(err)
	}
}

func TestReadWrite_RewriteFile(t *testing.T) {
	state := utils.MountSetup(t, "static", "-read_write_mapping=/:%ROOT%")
	defer state.TearDown(t)

	utils.MustWriteFile(t, state.RootPath("file"), 0644, "original content")
	if err := utils.FileEquals(state.MountPath("file"), "original content"); err != nil {
		t.Error(err)
	}

	utils.MustWriteFile(t, state.MountPath("file"), 0644, "rewritten content")
	if err := utils.FileEquals(state.MountPath("file"), "rewritten content"); err != nil {
		t.Error(err)
	}
}

func TestReadWrite_RewriteFileWithShorterContent(t *testing.T) {
	state := utils.MountSetup(t, "static", "-read_write_mapping=/:%ROOT%")
	defer state.TearDown(t)

	utils.MustWriteFile(t, state.MountPath("file"), 0644, "very long contents")
	utils.MustWriteFile(t, state.MountPath("file"), 0644, "short")
	if err := utils.FileEquals(state.MountPath("file"), "short"); err != nil {
		t.Error(err)
	}
}

func TestReadWrite_Truncate(t *testing.T) {
	state := utils.MountSetup(t, "static", "-read_write_mapping=/:%ROOT%")
	defer state.TearDown(t)

	utils.MustWriteFile(t, state.MountPath("file"), 0644, "very long contents")

	wantContent := "very"
	if err := os.Truncate(state.MountPath("file"), int64(len(wantContent))); err != nil {
		t.Fatalf("Truncate failed: %v", err)
	}

	if err := utils.FileEquals(state.MountPath("file"), wantContent); err != nil {
		t.Error(err)
	}
}

// equivalentStats compares two os.FileInfo objects and returns nil if they represent the same
// file; otherwise returns a descriptive error including the differences between the two.
// This equivalency is to be used during file move tess, to check if a file was actually moved
// instead of recreated.
func equivalentStats(stat1 os.FileInfo, stat2 os.FileInfo) error {
	ino1 := stat1.Sys().(*syscall.Stat_t).Ino
	ino2 := stat2.Sys().(*syscall.Stat_t).Ino

	if stat1.Mode() != stat2.Mode() || stat1.ModTime() != stat2.ModTime() || ino1 != ino2 {
		return fmt.Errorf("got mode=%v, mtime=%v, inode=%v; want mode=%v, mtime=%v, inode=%v", stat1.Mode(), stat1.ModTime(), ino1, stat2.Mode(), stat2.ModTime(), ino2)
	}
	return nil
}

// doRenameTest is a helper function for the tests that verify the file system-level rename
// operation.  This takes the path of a file to be moved (the "old outer path"), the path of the
// rename target (the "new outer path"), and the corresponding paths within the mount point.
//
// Tests calling this function should only start a sandboxfs instance with the desired configuration
// and then immediately call this function.
func doRenameTest(t *testing.T, oldOuterPath, newOuterPath, oldInnerPath, newInnerPath string) {
	utils.MustMkdirAll(t, filepath.Dir(oldOuterPath), 0755)
	utils.MustMkdirAll(t, filepath.Dir(newOuterPath), 0755)
	utils.MustMkdirAll(t, filepath.Dir(oldInnerPath), 0755)
	utils.MustMkdirAll(t, filepath.Dir(newInnerPath), 0755)
	utils.MustWriteFile(t, oldOuterPath, 0644, "some content")

	lstatOrFatal := func(path string) os.FileInfo {
		stat, err := os.Lstat(path)
		if err != nil {
			t.Fatalf("Failed to lstat %s: %v", path, err)
		}
		return stat
	}
	oldOuterStat := lstatOrFatal(oldOuterPath)
	oldInnerStat := lstatOrFatal(oldInnerPath)
	if err := os.Rename(oldInnerPath, newInnerPath); err != nil {
		t.Fatalf("Failed to rename %s to %s: %v", oldInnerPath, newInnerPath, err)
	}
	newOuterStat := lstatOrFatal(newOuterPath)
	newInnerStat := lstatOrFatal(newInnerPath)

	if _, err := os.Lstat(oldOuterPath); os.IsExist(err) {
		t.Fatalf("Old file name in root still present but should have disappeared: %s", oldOuterPath)
	}
	if _, err := os.Lstat(oldInnerPath); os.IsExist(err) {
		t.Fatalf("Old file name in mount point still present but should have disappeared: %s", oldInnerPath)
	}
	if err := utils.FileEquals(newOuterPath, "some content"); err != nil {
		t.Fatalf("New file name in root missing or with bad contents: %s: %v", newOuterPath, err)
	}
	if err := utils.FileEquals(newInnerPath, "some content"); err != nil {
		t.Fatalf("New file name in mount point missing or with bad contents: %s: %v", newInnerPath, err)
	}

	if err := equivalentStats(oldOuterStat, newOuterStat); err != nil {
		t.Errorf("Stats for %s and %s differ: %v", oldOuterPath, newOuterPath, err)
	}
	if err := equivalentStats(oldInnerStat, newInnerStat); err != nil {
		t.Errorf("Stats for %s and %s differ: %v", oldInnerPath, newInnerPath, err)
	}
}

func TestReadWrite_RenameFile(t *testing.T) {
	state := utils.MountSetup(t, "static", "-read_write_mapping=/:%ROOT%")
	defer state.TearDown(t)

	oldOuterPath := state.RootPath("old-name")
	newOuterPath := state.RootPath("new-name")
	oldInnerPath := state.MountPath("old-name")
	newInnerPath := state.MountPath("new-name")
	doRenameTest(t, oldOuterPath, newOuterPath, oldInnerPath, newInnerPath)
}

func TestReadWrite_MoveFile(t *testing.T) {
	state := utils.MountSetup(t, "static", "-read_write_mapping=/:%ROOT%")
	defer state.TearDown(t)

	oldOuterPath := state.RootPath("dir1/dir2/old-name")
	newOuterPath := state.RootPath("dir2/dir3/dir4/new-name")
	oldInnerPath := state.MountPath("dir1/dir2/old-name")
	newInnerPath := state.MountPath("dir2/dir3/dir4/new-name")
	doRenameTest(t, oldOuterPath, newOuterPath, oldInnerPath, newInnerPath)
}

func TestReadWrite_Mknod(t *testing.T) {
	utils.RequireRoot(t, "Requires root privileges to create arbitrary nodes")

	state := utils.MountSetup(t, "static", "-read_write_mapping=/:%ROOT%")
	defer state.TearDown(t)

	// checkNode ensures that a given file is of the specified type and, if the type indicates
	// that the file is a device, that the device number matches.  This check is done on both
	// the underlying file system and within the mount point.
	checkNode := func(relPath string, wantMode os.FileMode, wantDev uint64) error {
		for _, path := range []string{state.RootPath(relPath), state.MountPath(relPath)} {
			fileInfo, err := os.Lstat(path)
			if err != nil {
				return fmt.Errorf("failed to stat %s: %v", path, err)
			}
			stat := fileInfo.Sys().(*syscall.Stat_t)

			if fileInfo.Mode() != wantMode {
				return fmt.Errorf("got mode %v for %s, want %v", fileInfo.Mode(), path, wantMode)
			}
			if (wantMode&os.ModeType)&os.ModeDevice != 0 {
				if uint64(stat.Rdev) != wantDev { // stat.Rdev size and sign are platform-specific.
					return fmt.Errorf("got dev %v for %s, want %v", stat.Rdev, path, wantDev)
				}
			}
		}
		return nil
	}

	// findOS checks if the current OS appears in a list of acceptable OSes.
	findOS := func(oses []string) bool {
		for _, os := range oses {
			if os == runtime.GOOS {
				return true
			}
		}
		return false
	}

	allOSes := []string{"darwin", "linux"}
	if !findOS(allOSes) {
		t.Fatalf("Don't know how this test behaves in this platform")
	}

	data := []struct {
		name string

		filename  string
		perm      uint32
		mknodType uint32
		dev       int
		statType  os.FileMode

		// The behavior of mknod(2) is operating-system specific.  On Linux, we can create
		// regular files with this call, and attempting to create a directory results in the
		// wrong node being created.  On macOS, attempting to create either of these fails.
		//
		// Instead of ignoring these cases as invalid, test specifically for the behavior we
		// know should happen by "whitelisting" the systems on which each test is valid.
		// This way, we verify that sandboxfs is properly delegating these calls to the
		// underlying system.
		wantOS []string
	}{
		{"RegularFile", "file", 0644, syscall.S_IFREG, 0, 0, []string{"linux"}},
		{"Directory", "dir", 0755, syscall.S_IFDIR, 0, os.ModeDir, []string{}},
		{"BlockDevice", "blkdev", 0400, syscall.S_IFBLK, 1234, os.ModeDevice, allOSes},
		{"CharDevice", "chrdev", 0400, syscall.S_IFCHR, 5678, os.ModeDevice | os.ModeCharDevice, allOSes},
		{"NamedPipe", "fifo", 0640, syscall.S_IFIFO, 0, os.ModeNamedPipe, allOSes},
	}
	for _, d := range data {
		t.Run(d.name, func(t *testing.T) {
			path := state.MountPath(d.filename)

			shouldHaveFailed := false

			err := syscall.Mknod(path, d.perm|d.mknodType, d.dev)
			if findOS(d.wantOS) {
				if err != nil {
					t.Fatalf("Failed to mknod %s: %v", path, err)
				}
			} else {
				if err == nil {
					shouldHaveFailed = true
				}
			}

			err = checkNode(d.filename, (os.FileMode(d.perm)&os.ModePerm)|d.statType, uint64(d.dev))
			if findOS(d.wantOS) {
				if err != nil {
					t.Error(err)
				}
			} else {
				if err == nil {
					shouldHaveFailed = true
				}
			}

			if shouldHaveFailed {
				t.Fatalf("Test was expected to fail on this platform due to behavioral differences in mknod(2) but succeeded")
			}
		})
	}
}

func TestReadWrite_Chmod(t *testing.T) {
	state := utils.MountSetup(t, "static", "-read_write_mapping=/:%ROOT%")
	defer state.TearDown(t)

	// checkPerm ensures that the given file has the given permissions on the underlying file
	// system and within the mount point.
	checkPerm := func(relPath string, wantPerm os.FileMode) error {
		for _, path := range []string{state.RootPath(relPath), state.MountPath(relPath)} {
			fileInfo, err := os.Lstat(path)
			if err != nil {
				return fmt.Errorf("failed to stat %s: %v", path, err)
			}
			perm := fileInfo.Mode() & os.ModePerm
			if perm != wantPerm {
				return fmt.Errorf("got permissions %v for %s, want %v", perm, path, wantPerm)
			}
		}
		return nil
	}

	t.Run("Dir", func(t *testing.T) {
		utils.MustMkdirAll(t, state.RootPath("dir"), 0755)

		path := state.MountPath("dir")
		if err := os.Chmod(path, 0500); err != nil {
			t.Fatalf("Failed to chmod %s: %v", path, err)
		}
		if err := checkPerm("dir", 0500); err != nil {
			t.Error(err)
		}
	})

	t.Run("File", func(t *testing.T) {
		utils.MustWriteFile(t, state.RootPath("file"), 0644, "new content")

		path := state.MountPath("file")
		if err := os.Chmod(path, 0440); err != nil {
			t.Fatalf("Failed to chmod %s: %v", path, err)
		}
		if err := checkPerm("file", 0440); err != nil {
			t.Error(err)
		}
	})

	t.Run("DanglingSymlink", func(t *testing.T) {
		utils.MustSymlink(t, "missing", state.RootPath("dangling-symlink"))

		path := state.MountPath("dangling-symlink")
		if err := os.Chmod(path, 0555); err == nil {
			t.Errorf("Want chmod to fail on dangling link, got success")
		}
	})

	t.Run("GoodSymlink", func(t *testing.T) {
		utils.MustWriteFile(t, state.RootPath("target"), 0644, "")
		utils.MustSymlink(t, "target", state.RootPath("good-symlink"))

		path := state.MountPath("good-symlink")
		linkFileInfo, err := os.Lstat(path)
		if err != nil {
			t.Fatalf("Failed to stat %s: %v", path, err)
		}

		if err := os.Chmod(path, 0200); err != nil {
			t.Fatalf("Failed to chmod %s: %v", path, err)
		}

		if err := checkPerm("good-symlink", linkFileInfo.Mode()&os.ModePerm); err != nil {
			t.Error(err)
		}
		if err := checkPerm("target", 0200); err != nil {
			t.Error(err)
		}
	})
}

func TestReadWrite_Chown(t *testing.T) {
	utils.RequireRoot(t, "Requires root privileges to change test file ownership")

	state := utils.MountSetup(t, "static", "-read_write_mapping=/:%ROOT%")
	defer state.TearDown(t)

	// checkOwners ensures that the given file is owned by the given user and group on the
	// underlying file system and within the mount point.
	checkOwners := func(relPath string, wantUID uint32, wantGID uint32) error {
		for _, path := range []string{state.RootPath(relPath), state.MountPath(relPath)} {
			fileInfo, err := os.Lstat(path)
			if err != nil {
				return fmt.Errorf("failed to stat %s: %v", path, err)
			}
			stat := fileInfo.Sys().(*syscall.Stat_t)

			if stat.Uid != wantUID {
				return fmt.Errorf("got uid %v for %s, want %v", stat.Uid, path, wantUID)
			}
			if stat.Gid != wantGID {
				return fmt.Errorf("got gid %v for %s, want %v", stat.Gid, path, wantGID)
			}
		}
		return nil
	}

	utils.MustMkdirAll(t, state.RootPath("dir"), 0755)
	utils.MustWriteFile(t, state.RootPath("file"), 0644, "new content")
	utils.MustSymlink(t, "missing", state.RootPath("dangling-symlink"))
	utils.MustWriteFile(t, state.RootPath("target"), 0644, "")
	utils.MustSymlink(t, "target", state.RootPath("good-symlink"))

	targetFileInfo, err := os.Lstat(state.RootPath("target"))
	if err != nil {
		t.Fatalf("Failed to stat %s: %v", state.RootPath("target"), err)
	}
	targetStat := targetFileInfo.Sys().(*syscall.Stat_t)

	data := []struct {
		name string

		filename string
		wantUID  int
		wantGID  int
	}{
		{"Dir", "dir", 1, 2},
		{"File", "file", 3, 4},
		{"DanglingSymlink", "dangling-symlink", 5, 6},
		{"GoodSymlink", "good-symlink", 7, 8},
	}
	for _, d := range data {
		t.Run(d.name, func(t *testing.T) {
			path := state.MountPath(d.filename)
			if err := os.Lchown(path, d.wantUID, d.wantGID); err != nil {
				t.Fatalf("Failed to chown %s: %v", path, err)
			}
			if err := checkOwners(d.filename, uint32(d.wantUID), uint32(d.wantGID)); err != nil {
				t.Error(err)
			}
		})
	}

	if err := checkOwners("target", targetStat.Uid, targetStat.Gid); err != nil {
		t.Errorf("Ownership of symlink target was modified but shouldn't have been: %v", err)
	}
}

func TestReadWrite_Chtimes(t *testing.T) {
	state := utils.MountSetup(t, "static", "-read_write_mapping=/:%ROOT%")
	defer state.TearDown(t)

	// checkTimes ensures that the given file has the desired timing information on the
	// underlying file system and within the mount point.
	//
	// wantAtime may be zero if the atime check should be skipped.  wantMtime is always checked
	// for equality.  wantMinCtime indicates the minimum ctime that the file should have, as
	// that's the most we can check for (because ctime cannot be explicitly set).
	checkTimes := func(relPath string, wantAtime time.Time, wantMtime time.Time, wantMinCtime time.Time) error {
		for _, path := range []string{state.RootPath(relPath), state.MountPath(relPath)} {
			fileInfo, err := os.Lstat(path)
			if err != nil {
				return fmt.Errorf("failed to stat %s: %v", path, err)
			}
			stat := fileInfo.Sys().(*syscall.Stat_t)

			if !fileInfo.ModTime().Equal(wantMtime) {
				return fmt.Errorf("got mtime %v for %s, want %v", fileInfo.ModTime(), path, wantMtime)
			}
			if !wantAtime.Equal(time.Unix(0, 0)) && !sandbox.Atime(stat).Equal(wantAtime) {
				return fmt.Errorf("got atime %v for %s, want %v", sandbox.Atime(stat), path, wantAtime)
			}
			if sandbox.Ctime(stat).Before(wantMinCtime) {
				return fmt.Errorf("got ctime %v for %s, want <= %v", sandbox.Ctime(stat), path, wantMinCtime)
			}
		}
		return nil
	}

	// chtimes is a wrapper over os.Chtimes that updates the given file with the desired atime
	// and mtime, but also computes a lower bound for the ctime of the touched file.  This lower
	// bound is returned and can later be fed to checkTimes.
	chtimes := func(path string, atime time.Time, mtime time.Time) (time.Time, error) {
		// We have no control on ctime updates so let some time pass before we modify our
		// test file.  This way, we can ensure that the ctime was set to, at least, the
		// current updated time.  All file systems should have a minimum of second-level
		// granularity (I'm looking at you HFS+), so sleeping for a whole second should be
		// sufficient to get this right.  (Sleeps can pause for longer than specified, but
		// that's perfectly fine.)
		minCtime := time.Now()
		time.Sleep(1 * time.Second)

		if err := os.Chtimes(path, atime, mtime); err != nil {
			return time.Unix(0, 0), fmt.Errorf("failed to chtimes %s: %v", path, err)
		}
		return minCtime, nil
	}

	someAtime := time.Date(2009, 5, 25, 9, 0, 0, 0, time.UTC)
	someMtime := time.Date(1984, 8, 10, 19, 15, 0, 0, time.UTC)

	t.Run("Dir", func(t *testing.T) {
		utils.MustMkdirAll(t, state.RootPath("dir"), 0755)

		wantMinCtime, err := chtimes(state.MountPath("dir"), someAtime, someMtime)
		if err != nil {
			t.Fatal(err)
		}
		if err := checkTimes("dir", someAtime, someMtime, wantMinCtime); err != nil {
			t.Error(err)
		}
	})

	t.Run("File", func(t *testing.T) {
		utils.MustWriteFile(t, state.RootPath("file"), 0644, "new content")

		wantMinCtime, err := chtimes(state.MountPath("file"), someAtime, someMtime)
		if err != nil {
			t.Fatal(err)
		}
		if err := checkTimes("file", someAtime, someMtime, wantMinCtime); err != nil {
			t.Error(err)
		}
	})

	t.Run("DanglingSymlink", func(t *testing.T) {
		utils.MustSymlink(t, "missing", state.RootPath("dangling-symlink"))

		if _, err := chtimes("dangling-symlink", time.Unix(0, 0), time.Unix(0, 0)); err == nil {
			t.Errorf("Want chtimes to fail on dangling link, got success")
		}
	})

	t.Run("GoodSymlink", func(t *testing.T) {
		utils.MustWriteFile(t, state.RootPath("target"), 0644, "")
		utils.MustSymlink(t, "target", state.RootPath("good-symlink"))
		path := state.MountPath("good-symlink")

		linkFileInfo, err := os.Lstat(path)
		if err != nil {
			t.Fatalf("Failed to stat %s: %v", path, err)
		}
		linkStat := linkFileInfo.Sys().(*syscall.Stat_t)

		wantMinCtime, err := chtimes(path, someAtime, someMtime)
		if err != nil {
			t.Fatal(err)
		}

		if err := checkTimes("good-symlink", time.Unix(0, 0), linkFileInfo.ModTime(), sandbox.Ctime(linkStat)); err != nil {
			t.Error(err)
		}
		if err := checkTimes("target", someAtime, someMtime, wantMinCtime); err != nil {
			t.Error(err)
		}
	})
}

// TODO(jmmv): Implement and test hard links.
