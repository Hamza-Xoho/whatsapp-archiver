//go:build !linux

package archiveread

import "os"

// The first bridge release targets Linux; this fallback keeps archiver builds portable.
func identityFields(info os.FileInfo) any { return info.Name() + info.ModTime().String() }
