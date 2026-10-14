//go:build windows

package health

import "fmt"

func DiskFree(string) (uint64, error) { return 0, fmt.Errorf("disk free unknown on Windows") }
