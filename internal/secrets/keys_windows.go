//go:build windows

package secrets

import (
	"fmt"
	"os"

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
