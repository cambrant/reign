package access

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"syscall"
)

// OpenEnvFile opens a service env file for reading after checking that it
// is safe for reign, running as root, to load into a service:
//
//   - the path is absolute and its last component is not a symlink, so it
//     cannot be redirected to another file after it was registered;
//   - it is a regular file (not a FIFO or device that could block or leak);
//   - it is owned by root, by the user reign runs as, or by an admin, so a
//     file another user swapped in is refused;
//   - nobody but its owner (and the root group) can write to it, since its
//     variables, LD_PRELOAD for one, end up in a process running as root.
//
// The returned file is the one that was checked; read from it rather than
// reopening the path.
func OpenEnvFile(path string, admins *Admins) (*os.File, error) {
	if !filepath.IsAbs(path) {
		return nil, fmt.Errorf("env file %q must be an absolute path", path)
	}
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		if isSymlinkErr(path, err) {
			return nil, fmt.Errorf("env file %q is a symlink; point env_file at the real file", path)
		}
		return nil, err
	}
	info, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, err
	}
	if err := checkEnvFileInfo(path, info, admins); err != nil {
		f.Close()
		return nil, err
	}
	return f, nil
}

// CheckEnvFile runs OpenEnvFile's checks and closes the file.
func CheckEnvFile(path string, admins *Admins) error {
	f, err := OpenEnvFile(path, admins)
	if err != nil {
		return err
	}
	return f.Close()
}

// CheckEnvFileReadableBy runs OpenEnvFile's checks and also requires that
// uid could read the file itself, so the API does not read files for a
// caller that the caller's own permissions keep it out of.
func CheckEnvFileReadableBy(path string, uid int, admins *Admins) error {
	f, err := OpenEnvFile(path, admins)
	if err != nil {
		return err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return err
	}
	if !canRead(info, uid, admins) {
		return fmt.Errorf("env file %q is not readable by uid %d", path, uid)
	}
	return nil
}

func checkEnvFileInfo(path string, info fs.FileInfo, admins *Admins) error {
	if !info.Mode().IsRegular() {
		return fmt.Errorf("env file %q is not a regular file", path)
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return fmt.Errorf("env file %q: cannot read its owner", path)
	}
	owner := int(st.Uid)
	if owner != 0 && owner != os.Geteuid() && !admins.IsAdmin(owner) {
		return fmt.Errorf("env file %q is owned by uid %d; it must be owned by root or a member of the admin group", path, owner)
	}
	perm := info.Mode().Perm()
	if perm&0o002 != 0 || (perm&0o020 != 0 && st.Gid != 0) {
		return fmt.Errorf("env file %q is writable by other users (mode %04o); run chmod go-w on it", path, perm)
	}
	return nil
}

// canRead reports whether uid may read a file with info, by its mode bits.
func canRead(info fs.FileInfo, uid int, admins *Admins) bool {
	if uid == 0 {
		return true
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return false
	}
	perm := info.Mode().Perm()
	switch {
	case int(st.Uid) == uid:
		return perm&0o400 != 0
	case slices.Contains(admins.userGroups(uid), strconv.FormatUint(uint64(st.Gid), 10)):
		return perm&0o040 != 0
	default:
		return perm&0o004 != 0
	}
}

// isSymlinkErr reports whether opening path failed because of O_NOFOLLOW.
func isSymlinkErr(path string, err error) bool {
	info, lerr := os.Lstat(path)
	return lerr == nil && info.Mode()&fs.ModeSymlink != 0 && err != nil
}
