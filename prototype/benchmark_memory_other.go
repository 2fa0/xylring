//go:build p0bench && !darwin

package main

import "fmt"

type processUsage struct{ Resident, Footprint, UserTime, SystemTime uint64 }

func nativeMemory() (processUsage, error) {
	return processUsage{}, fmt.Errorf("physical footprint requires macOS")
}
