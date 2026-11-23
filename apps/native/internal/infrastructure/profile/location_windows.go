package profile

import "golang.org/x/sys/windows"

func localDataDirectory() (string, error) {
	return windows.KnownFolderPath(windows.FOLDERID_LocalAppData, windows.KF_FLAG_DEFAULT)
}
