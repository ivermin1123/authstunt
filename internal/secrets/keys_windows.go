//go:build windows

package secrets

import (
	"fmt"
	"io/fs"
	"os"
	"unsafe"

	"golang.org/x/sys/windows"
)

// restrictToOwner sets a protected DACL granting full access to the
// current user only. Mode bits are a no-op on Windows, so without this
// the key file would inherit whatever the parent directory allows.
func restrictToOwner(path string, _ *os.File) error {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return fmt.Errorf("token user: %w", err)
	}
	// D:P  protected DACL (no inherited entries)
	// A;;FA;;;<sid>  allow full access to the current user
	sddl := fmt.Sprintf("D:P(A;;FA;;;%s)", user.User.Sid.String())
	sd, err := windows.SecurityDescriptorFromString(sddl)
	if err != nil {
		return fmt.Errorf("security descriptor: %w", err)
	}
	dacl, _, err := sd.DACL()
	if err != nil {
		return fmt.Errorf("dacl: %w", err)
	}
	err = windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION,
		nil, nil, dacl, nil)
	if err != nil {
		return fmt.Errorf("set security info: %w", err)
	}
	return nil
}

// narrowDir is a no-op on Windows: os.Chmod only toggles the read-only
// attribute there, which grants nobody anything. The key file's own DACL
// is what the checks below rely on.
func narrowDir(string, fs.FileInfo) error { return nil }

// checkKeyFileAccess refuses a key file whose DACL grants access to
// anyone but the current user.
//
// The POSIX permission check is meaningless here - Go reports a synthetic
// 0666 for every file - so the Windows answer is the access control list
// itself. A file created by restrictToOwner carries exactly one allow ACE
// for the current user; anything else, including an inherited entry for
// Administrators, means the key was reachable by another principal. As on
// POSIX, this refuses rather than repairs: the exposure already happened.
func checkKeyFileAccess(path string, _ fs.FileInfo) error {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return fmt.Errorf("secrets: token user: %w", err)
	}
	sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return fmt.Errorf("secrets: read key file security info: %w", err)
	}
	dacl, _, err := sd.DACL()
	if err != nil {
		return fmt.Errorf("secrets: read key file dacl: %w", err)
	}
	if dacl == nil {
		// A NULL DACL grants everyone full access.
		return fmt.Errorf("%w: %s has no access control list", ErrUnsafeKeyFile, path)
	}
	for i := uint32(0); i < uint32(dacl.AceCount); i++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(dacl, i, &ace); err != nil {
			return fmt.Errorf("secrets: read key file ace %d: %w", i, err)
		}
		if ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE {
			// A deny entry only ever removes access, so it cannot make the
			// key reachable by someone else.
			continue
		}
		sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
		if !sid.Equals(user.User.Sid) {
			return fmt.Errorf("%w: %s grants access to %s: rotate it (delete the key and the data sealed under it) rather than relaxing this check",
				ErrUnsafeKeyFile, path, sid)
		}
	}
	return nil
}
