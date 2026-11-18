//go:build windows

package trustpath

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Principals allowed to own or write protected paths.
var trustedSIDs = []string{
	"S-1-5-18",     // LocalSystem
	"S-1-5-32-544", // Administrators
	"S-1-5-80-956008885-3418522649-1831038044-1853292631-2271478464", // TrustedInstaller
	"S-1-3-0", // CREATOR OWNER (placeholder, resolved to a trusted owner)
}

var selfSID = sync.OnceValue(func() string {
	u, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return ""
	}
	return u.User.Sid.String()
})

func trusted(sid *windows.SID) bool {
	s := sid.String()
	return slices.Contains(trustedSIDs, s) || (selfSID() != "" && s == selfSID())
}

const (
	genericAll   = 0x10000000
	genericWrite = 0x40000000
	writeDAC     = 0x00040000
	writeOwner   = 0x00080000
	deleteRight  = 0x00010000
	fileWrite    = 0x2  // FILE_WRITE_DATA / FILE_ADD_FILE
	fileAppend   = 0x4  // FILE_APPEND_DATA / FILE_ADD_SUBDIRECTORY
	fileWriteEA  = 0x10 // FILE_WRITE_EA
	fileDelChild = 0x40 // FILE_DELETE_CHILD
)

// Rights that let a principal replace or redirect a protected leaf.
const leafMask = genericAll | genericWrite | writeDAC | writeOwner | deleteRight | fileWrite | fileAppend | fileWriteEA | fileDelChild

// Rights on an ancestor that let a principal rename/remove our component.
// Creating new siblings (e.g. Authenticated Users on C:\, Users on
// C:\ProgramData) does not.
const ancestorMask = genericAll | writeDAC | writeOwner | deleteRight | fileDelChild

func checkSD(p string, mask uint32) error {
	sd, err := windows.GetNamedSecurityInfo(p, windows.SE_FILE_OBJECT,
		windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return err
	}
	owner, _, err := sd.Owner()
	if err != nil {
		return err
	}
	if !trusted(owner) {
		return fmt.Errorf("%w: %s is owned by %s", ErrUntrusted, p, owner.String())
	}
	dacl, _, err := sd.DACL()
	if err != nil {
		return err
	}
	if dacl == nil {
		return fmt.Errorf("%w: %s has a NULL DACL", ErrUntrusted, p)
	}
	for i := uint32(0); i < uint32(dacl.AceCount); i++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(dacl, i, &ace); err != nil {
			return err
		}
		if ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE || ace.Header.AceFlags&windows.INHERIT_ONLY_ACE != 0 {
			continue
		}
		sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart)) //nolint:gosec // the SID is stored inline from SidStart (ACCESS_ALLOWED_ACE layout)
		if uint32(ace.Mask)&mask != 0 && !trusted(sid) {
			return fmt.Errorf("%w: %s grants write to %s", ErrUntrusted, p, sid.String())
		}
	}
	return nil
}

func checkChain(p string) error {
	for d := filepath.Dir(p); ; d = filepath.Dir(d) {
		if err := checkSD(d, ancestorMask); err != nil {
			return err
		}
		if d == filepath.Dir(d) {
			return nil
		}
	}
}

func resolve(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	if err := checkChain(abs); err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(abs)
}

func check(path string, wantDir bool) error {
	r, err := resolve(path)
	if err != nil {
		return err
	}
	if err := checkChain(r); err != nil {
		return err
	}
	fi, err := os.Lstat(r)
	if err != nil {
		return err
	}
	if fi.IsDir() != wantDir {
		return fmt.Errorf("%w: %s has unexpected type", ErrUntrusted, r)
	}
	return checkSD(r, leafMask)
}

// CheckFile verifies a binary, library or config file and its parents.
func CheckFile(path string) error { return check(path, false) }

// CheckDir verifies a directory and its parents.
func CheckDir(path string) error { return check(path, true) }

// EnsureDir creates path, refuses it if an untrusted principal owns it,
// then replaces its DACL with a protected one: SYSTEM and Administrators
// full control (inherited), ShareSIDs read and traverse on the directory
// only. Directories under C:\ProgramData would otherwise inherit "Users:
// create files" from ProgramData.
func EnsureDir(path string, o DirOptions) error {
	if err := os.MkdirAll(path, 0o700); err != nil {
		return err
	}
	r, err := resolve(path)
	if err != nil {
		return err
	}
	if err := checkChain(r); err != nil {
		return err
	}
	sd, err := windows.GetNamedSecurityInfo(r, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION)
	if err != nil {
		return err
	}
	owner, _, err := sd.Owner()
	if err != nil {
		return err
	}
	if !trusted(owner) {
		return fmt.Errorf("%w: %s is owned by %s; remove it as an administrator", ErrUntrusted, r, owner.String())
	}
	var b strings.Builder
	b.WriteString("D:P(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)")
	for _, s := range o.ShareSIDs {
		if _, err := windows.StringToSid(s); err != nil {
			return fmt.Errorf("trustpath: bad SID %q: %w", s, err)
		}
		b.WriteString("(A;;0x1200a9;;;" + s + ")") // read, execute/traverse
	}
	if err := SetDACL(r, b.String()); err != nil {
		return err
	}
	return checkSD(r, leafMask)
}

// SetDACL applies a protected DACL given in SDDL to path.
func SetDACL(path, sddl string) error {
	nsd, err := windows.SecurityDescriptorFromString(sddl)
	if err != nil {
		return err
	}
	dacl, _, err := nsd.DACL()
	if err != nil {
		return err
	}
	return windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, dacl, nil)
}
