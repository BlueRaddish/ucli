package artifact

import (
	"golang.org/x/sys/windows"
	"path/filepath"
	"strings"
)

// Match state-store owner-only ACLs without changing permissions of foreign or
// preexisting paths. x/sys is already a pinned application dependency.
func secureCreatedPath(path string, directory bool) error {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return err
	}
	inherit := ""
	if directory {
		inherit = "OICI"
	}
	descriptor, err := windows.SecurityDescriptorFromString("D:P(A;" + inherit + ";FA;;;" + user.User.Sid.String() + ")")
	if err != nil {
		return err
	}
	dacl, _, err := descriptor.DACL()
	if err != nil {
		return err
	}
	return windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, dacl, nil)
}
func localPath(path string) error {
	volume := filepath.VolumeName(path)
	if strings.Contains(strings.TrimPrefix(path, volume), ":") {
		return problem(InvalidRequest, "Artifact cache paths cannot use alternate data streams.")
	}
	root, err := windows.UTF16PtrFromString(volume + `\`)
	if err != nil || windows.GetDriveType(root) == windows.DRIVE_REMOTE {
		return problem(InvalidRequest, "Artifact cache must use a local drive.")
	}
	return nil
}
func isReparse(path string) bool {
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return true
	}
	attrs, err := windows.GetFileAttributes(name)
	return err != nil || attrs&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0
}
