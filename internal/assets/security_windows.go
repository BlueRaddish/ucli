package assets

import (
	"errors"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows"
)

// Match the existing artifact/state owner-only policy, only on paths this
// operation created. Existing ancestors or published entries are never chmodded.
func secureCreated(path string, directory bool) error {
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
		return errors.New("model cache path cannot use an alternate data stream")
	}
	for _, part := range strings.Split(strings.TrimPrefix(path, volume), `\`) {
		if strings.HasSuffix(part, ".") || strings.HasSuffix(part, " ") {
			return errors.New("model cache path cannot use a trailing dot or space alias")
		}
	}
	root, err := windows.UTF16PtrFromString(volume + `\`)
	if err != nil || windows.GetDriveType(root) == windows.DRIVE_REMOTE {
		return errors.New("model cache must use a local drive")
	}
	return nil
}

func isReparse(path string) bool {
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return true
	}
	attributes, err := windows.GetFileAttributes(name)
	return err != nil || attributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0
}
