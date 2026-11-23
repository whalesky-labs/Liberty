package credentials

import (
	"errors"
	"runtime"
	"unicode/utf8"
	"unsafe"

	"golang.org/x/sys/windows"
	"liberty.local/liberty/internal/domain"
)

var advapi = windows.NewLazySystemDLL("advapi32.dll")
var credRead = advapi.NewProc("CredReadW")
var credWrite = advapi.NewProc("CredWriteW")
var credDelete = advapi.NewProc("CredDeleteW")
var credFree = advapi.NewProc("CredFree")

// Preserve the native CREDENTIALW alignment on Windows x64.
type credential struct {
	Flags, Type             uint32
	TargetName, Comment     *uint16
	LastWritten             windows.Filetime
	BlobSize                uint32
	Blob                    *byte
	Persist, AttributeCount uint32
	Attributes              uintptr
	TargetAlias, UserName   *uint16
}

func (s *System) get(key string) (domain.Secret, error) {
	target, err := windows.UTF16PtrFromString(s.service + ":" + key)
	if err != nil {
		return domain.Secret{}, domain.ErrCredentialStore
	}
	var item *credential
	ok, _, err := credRead.Call(uintptr(unsafe.Pointer(target)), 1, 0, uintptr(unsafe.Pointer(&item)))
	runtime.KeepAlive(target)
	if ok == 0 {
		if errors.Is(err, windows.ERROR_NOT_FOUND) {
			return domain.Secret{}, nil
		}
		return domain.Secret{}, domain.ErrCredentialStore
	}
	if item == nil {
		return domain.Secret{}, domain.ErrCredentialStore
	}
	defer credFree.Call(uintptr(unsafe.Pointer(item)))
	if item.BlobSize > 2560 || (item.BlobSize > 0 && item.Blob == nil) {
		return domain.Secret{}, domain.ErrCredentialStore
	}
	bytes := unsafe.Slice(item.Blob, int(item.BlobSize))
	if !utf8.Valid(bytes) {
		return domain.Secret{}, domain.ErrCredentialStore
	}
	return domain.NewSecret(string(bytes)), nil
}
func (s *System) set(key string, value domain.Secret) error {
	target, err := windows.UTF16PtrFromString(s.service + ":" + key)
	if err != nil {
		return domain.ErrCredentialStore
	}
	username, _ := windows.UTF16PtrFromString("Liberty")
	bytes := []byte(value.Reveal())
	defer clear(bytes)
	item := credential{Type: 1, TargetName: target, BlobSize: uint32(len(bytes)), Blob: &bytes[0], Persist: 2, UserName: username}
	ok, _, _ := credWrite.Call(uintptr(unsafe.Pointer(&item)), 0)
	runtime.KeepAlive(item)
	runtime.KeepAlive(bytes)
	if ok == 0 {
		return domain.ErrCredentialStore
	}
	return nil
}
func (s *System) delete(key string) error {
	target, err := windows.UTF16PtrFromString(s.service + ":" + key)
	if err != nil {
		return domain.ErrCredentialStore
	}
	ok, _, err := credDelete.Call(uintptr(unsafe.Pointer(target)), 1, 0)
	runtime.KeepAlive(target)
	if ok == 0 && !errors.Is(err, windows.ERROR_NOT_FOUND) {
		return domain.ErrCredentialStore
	}
	return nil
}
