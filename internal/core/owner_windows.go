//go:build windows

package core

import "golang.org/x/sys/windows"

func ownedByCurrentUser(p string) bool {
	user, e := windows.GetCurrentProcessToken().GetTokenUser()
	if e != nil {
		return false
	}
	sd, e := windows.GetNamedSecurityInfo(p, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION)
	if e != nil {
		return false
	}
	owner, _, e := sd.Owner()
	return e == nil && owner.Equals(user.User.Sid)
}

func secureDirectory(p string) error {
	user, e := windows.GetCurrentProcessToken().GetTokenUser()
	if e != nil {
		return e
	}
	sd, e := windows.SecurityDescriptorFromString("D:P(A;OICI;FA;;;" + user.User.Sid.String() + ")")
	if e != nil {
		return e
	}
	acl, _, e := sd.DACL()
	if e != nil {
		return e
	}
	return windows.SetNamedSecurityInfo(p, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, acl, nil)
}
