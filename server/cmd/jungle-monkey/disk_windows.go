//go:build windows

package main

import "golang.org/x/sys/windows"

func freeDisk(path string) (uint64, error) {
	var free uint64
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return 0, err
	}
	err = windows.GetDiskFreeSpaceEx(p, &free, nil, nil)
	return free, err
}
