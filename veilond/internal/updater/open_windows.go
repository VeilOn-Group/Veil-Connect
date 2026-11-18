//go:build windows

package updater

import "os"

// Windows: the inbox ACL lets the veilond service account create files but
// not links (creating symlinks needs SeCreateSymbolicLinkPrivilege);
// openRegular has already refused anything but a regular file.
func openNoFollow(p string) (*os.File, error) { return os.Open(p) } //nolint:gosec // see above
