package managed

import (
	"os"

	"golang.org/x/sys/windows"
)

// Windows FileInfo.Sys exposes attributes but no link count. Query the open
// handle so aliases cannot pass the immutable runtime/input ownership checks.
func singleLinkFile(file *os.File) bool {
	var info windows.ByHandleFileInformation
	return windows.GetFileInformationByHandle(windows.Handle(file.Fd()), &info) == nil &&
		info.NumberOfLinks == 1 && info.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT == 0
}
