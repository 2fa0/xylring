//go:build p0bench && darwin

package main

import (
	"fmt"
	"os"
	"sync"
	"unsafe"

	"github.com/ebitengine/purego"
)

// Layout from the macOS SDK sys/resource.h, rusage_info_v0.
// Unlike vmmap's rounded display this preserves the kernel's byte counts.
type processUsage struct {
	UUID                                                            [16]byte
	UserTime, SystemTime, PackageWakeups, InterruptWakeups, Pageins uint64
	Wired, Resident, Footprint, Started, Exited                     uint64
}

var usageOnce sync.Once
var usageError error
var pidUsage func(int32, int32, unsafe.Pointer) int32

func nativeMemory() (processUsage, error) {
	usageOnce.Do(func() {
		lib, err := purego.Dlopen("/usr/lib/libproc.dylib", purego.RTLD_NOW|purego.RTLD_LOCAL)
		if err != nil {
			usageError = err
			return
		}
		purego.RegisterLibFunc(&pidUsage, lib, "proc_pid_rusage")
	})
	var usage processUsage
	if usageError != nil {
		return usage, usageError
	}
	if pidUsage(int32(os.Getpid()), 0, unsafe.Pointer(&usage)) != 0 {
		return usage, fmt.Errorf("proc_pid_rusage failed")
	}
	return usage, nil
}
