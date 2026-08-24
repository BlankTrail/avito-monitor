//go:build windows

// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"path/filepath"

	"golang.org/x/sys/windows"
)

// diskFree reports the free and total bytes of the volume holding path.
//
// GetDiskFreeSpaceEx's first output is what is available to the calling user,
// which is the number that matters: a quota can leave a volume with terabytes
// free and this process with none.
func diskFree(path string) (free, total int64, err error) {
	p, err := windows.UTF16PtrFromString(filepath.Dir(path))
	if err != nil {
		return 0, 0, err
	}
	var freeToCaller, totalBytes, totalFree uint64
	if err := windows.GetDiskFreeSpaceEx(p, &freeToCaller, &totalBytes, &totalFree); err != nil {
		return 0, 0, err
	}
	return int64(freeToCaller), int64(totalBytes), nil
}
