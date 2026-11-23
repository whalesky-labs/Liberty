package credentials

import (
	"testing"
	"unsafe"
)

func TestWindowsCredentialNativeABI(t *testing.T) {
	size, blobOffset, usernameOffset := uintptr(80), uintptr(40), uintptr(72)
	if unsafe.Sizeof(credential{}) != size || unsafe.Offsetof(credential{}.Blob) != blobOffset || unsafe.Offsetof(credential{}.UserName) != usernameOffset {
		t.Fatal("CREDENTIALW layout differs from Windows ABI")
	}
}
