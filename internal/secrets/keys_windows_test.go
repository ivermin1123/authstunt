//go:build windows

package secrets

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

// TestKeyFileDACLCurrentUserOnly is the Windows half of the key file
// permission proof. Mode bits are a no-op there, so the POSIX test skips
// and a green Windows runner on its own would prove nothing about who can
// read the key.
func TestKeyFileDACLCurrentUserOnly(t *testing.T) {
	dir := t.TempDir()
	if _, err := LoadOrCreateKey(dir, "p1"); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "p1.key")

	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		t.Fatal(err)
	}
	dacl, _, err := sd.DACL()
	if err != nil {
		t.Fatal(err)
	}
	if dacl == nil {
		t.Fatal("the key file has no DACL, which grants everyone full access")
	}
	if dacl.AceCount != 1 {
		t.Fatalf("the key file DACL holds %d entries, want exactly 1", dacl.AceCount)
	}
	var ace *windows.ACCESS_ALLOWED_ACE
	if err := windows.GetAce(dacl, 0, &ace); err != nil {
		t.Fatal(err)
	}
	if ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE {
		t.Fatalf("the only DACL entry has type %d, want an allow entry", ace.Header.AceType)
	}
	sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
	if !sid.Equals(user.User.Sid) {
		t.Fatalf("the key file grants access to %s, want only %s", sid, user.User.Sid)
	}
}

// TestLoadOrCreateKeyRejectsSharedDACL is the Windows counterpart of the
// POSIX unsafe-permissions test: a key file another principal can reach is
// refused, not repaired.
func TestLoadOrCreateKeyRejectsSharedDACL(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "p1.key")
	if err := os.WriteFile(path, make([]byte, keySize), 0o600); err != nil {
		t.Fatal(err)
	}
	// The grant is spelled out rather than left to whatever the temp
	// directory happens to inherit, so the test asserts the check and not
	// the runner's profile layout. WD is Everyone.
	sd, err := windows.SecurityDescriptorFromString("D:P(A;;FA;;;WD)")
	if err != nil {
		t.Fatal(err)
	}
	dacl, _, err := sd.DACL()
	if err != nil {
		t.Fatal(err)
	}
	err = windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION,
		nil, nil, dacl, nil)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := LoadOrCreateKey(dir, "p1"); !errors.Is(err, ErrUnsafeKeyFile) {
		t.Fatalf("a key file readable by Everyone returned %v, want ErrUnsafeKeyFile", err)
	}
}
