//go:build windows

package workspace

import (
	"errors"
	"fmt"
	"golang.org/x/sys/windows"
)

func physicalIdentity(root string) (_ string, err error) {
	p, err := windows.UTF16PtrFromString(root)
	if err != nil {
		return "", err
	}
	h, err := windows.CreateFile(p, 0, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil, windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS, 0)
	if err != nil {
		return "", err
	}
	defer func() { err = errors.Join(err, windows.CloseHandle(h)) }()
	var info windows.ByHandleFileInformation
	if err = windows.GetFileInformationByHandle(h, &info); err != nil {
		return "", err
	}
	return fmt.Sprintf("win:%d:%d:%d", info.VolumeSerialNumber, info.FileIndexHigh, info.FileIndexLow), nil
}
