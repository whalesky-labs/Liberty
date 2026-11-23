package schema

import "golang.org/x/sys/windows"

func publishRestoreCredentialJournal(temporary, final string) error {
	source, err := windows.UTF16PtrFromString(temporary)
	if err != nil {
		return err
	}
	destination, err := windows.UTF16PtrFromString(final)
	if err != nil {
		return err
	}
	return windows.MoveFileEx(source, destination, windows.MOVEFILE_WRITE_THROUGH)
}
