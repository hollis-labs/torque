package config

import (
	"fmt"
	"os"
	"syscall"
)

// DirIdentity is a directory's device and inode, recorded so that a later
// read by path can tell whether the path still names the same directory
// (CW-20261001-0141). A directory that was moved aside and recreated under
// the same path has a new inode.
type DirIdentity struct {
	Path     string
	dev, ino uint64
}

// RecordDirIdentity records path's current device and inode.
func RecordDirIdentity(path string) (DirIdentity, error) {
	dev, ino, err := statIdentity(path)
	if err != nil {
		return DirIdentity{}, err
	}
	return DirIdentity{Path: path, dev: dev, ino: ino}, nil
}

// Check reports an error when the path no longer names the recorded
// directory.
func (d DirIdentity) Check() error {
	dev, ino, err := statIdentity(d.Path)
	if err != nil {
		return fmt.Errorf("%s is no longer the directory recorded at startup: %w", d.Path, err)
	}
	if dev != d.dev || ino != d.ino {
		return fmt.Errorf("%s is no longer the directory recorded at startup (device/inode %d/%d, was %d/%d)", d.Path, dev, ino, d.dev, d.ino)
	}
	return nil
}

func statIdentity(path string) (dev, ino uint64, err error) {
	info, err := os.Stat(path)
	if err != nil {
		return 0, 0, err
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, 0, fmt.Errorf("%s: no device/inode on this platform", path)
	}
	// Dev is a uint64 on Linux and an int32 on macOS.
	return uint64(st.Dev), uint64(st.Ino), nil
}
