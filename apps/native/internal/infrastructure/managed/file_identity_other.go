//go:build !windows

package managed

import "os"

func singleLinkFile(file *os.File) bool {
	info, err := file.Stat()
	return err == nil && info.Mode().IsRegular() && !linkedFile(info)
}
