//go:build !windows

// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"path/filepath"

	"golang.org/x/sys/unix"
)

// diskFree reports the free and total bytes of the filesystem holding path.
//
// Bavail rather than Bfree: the difference is the blocks reserved for root,
// which this process cannot have, and reporting them as free is how a guard
// passes on a disk that is already full for everyone who is not root.
func diskFree(path string) (free, total int64, err error) {
	var st unix.Statfs_t
	if err := unix.Statfs(filepath.Dir(path), &st); err != nil {
		return 0, 0, err
	}
	return int64(st.Bavail) * int64(st.Bsize), int64(st.Blocks) * int64(st.Bsize), nil
}
