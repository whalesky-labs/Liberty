package credentials

import (
	"testing"
	"unsafe"
)

func TestWindowsCredentialNativeABI(t *testing.T) {
	size, blobOffset, usernameOffset := uintptr(52), uintptr(28), uintptr(48)
	if unsafe.Sizeof(uintptr(0)) == 8 {
		size, blobOffset, usernameOffset = 80, 40, 72
	}
	if unsafe.Sizeof(credential{}) != size || unsafe.Offsetof(credential{}.Blob) != blobOffset || unsafe.Offsetof(credential{}.UserName) != usernameOffset {
		t.Fatal("CREDENTIALW layout differs from Windows ABI")
	}
}
