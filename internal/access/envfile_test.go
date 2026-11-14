package access

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func writeEnv(t *testing.T, mode os.FileMode) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), ".env")
	if err := os.WriteFile(path, []byte("A=1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, mode); err != nil {
		t.Fatal(err)
	}
	return path
}

// noAdmins knows no users, so only root and reign's own user may own env files.
var noAdmins = NewAdminsWithLookup("reign", Lookup{
	UserGroups: func(int) ([]string, error) { return nil, errors.New("no users") },
	GroupID:    func(string) (string, error) { return "", errors.New("no group") },
})

func TestCheckEnvFile(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("ownership checks behave differently as root")
	}
	fifo := filepath.Join(t.TempDir(), "fifo")
	if err := mkfifo(fifo); err != nil {
		t.Skip("mkfifo:", err)
	}
	good := writeEnv(t, 0o600)
	link := filepath.Join(t.TempDir(), "link.env")
	if err := os.Symlink(good, link); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name    string
		path    string
		wantErr string
	}{
		{"own file 0600", good, ""},
		{"own file 0644", writeEnv(t, 0o644), ""},
		{"relative path", ".env", "absolute"},
		{"symlink", link, "symlink"},
		{"fifo", fifo, "not a regular file"},
		{"world writable", writeEnv(t, 0o606), "writable by other users"},
		{"group writable", writeEnv(t, 0o660), "writable by other users"},
		{"missing", filepath.Join(t.TempDir(), "nope"), "no such file"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := CheckEnvFile(tt.path, noAdmins)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("CheckEnvFile: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("CheckEnvFile err = %v, want %q", err, tt.wantErr)
			}
		})
	}
}

func TestCheckEnvFileReadableBy(t *testing.T) {
	me := os.Geteuid()
	if me == 0 {
		t.Skip("ownership checks behave differently as root")
	}
	myGID := strconv.Itoa(os.Getegid())
	admins := NewAdminsWithLookup("reign", Lookup{
		UserGroups: func(uid int) ([]string, error) {
			if uid == 4242 {
				return []string{myGID}, nil // shares the file's group
			}
			return []string{"4343"}, nil
		},
		GroupID: func(string) (string, error) { return "990", nil },
	})
	private := writeEnv(t, 0o600)
	groupRead := writeEnv(t, 0o640)
	worldRead := writeEnv(t, 0o644)

	tests := []struct {
		name string
		path string
		uid  int
		ok   bool
	}{
		{"owner reads private file", private, me, true},
		{"root reads private file", private, 0, true},
		{"stranger cannot read private file", private, 4343, false},
		{"group member reads group file", groupRead, 4242, true},
		{"stranger cannot read group file", groupRead, 4343, false},
		{"stranger reads world file", worldRead, 4343, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := CheckEnvFileReadableBy(tt.path, tt.uid, admins)
			if (err == nil) != tt.ok {
				t.Fatalf("CheckEnvFileReadableBy(uid %d) err = %v, want ok=%v", tt.uid, err, tt.ok)
			}
		})
	}
}

func TestIsAdmin(t *testing.T) {
	admins := NewAdminsWithLookup("reign", Lookup{
		UserGroups: func(uid int) ([]string, error) {
			if uid == 1000 {
				return []string{"1000", "990"}, nil
			}
			return []string{strconv.Itoa(uid)}, nil
		},
		GroupID: func(string) (string, error) { return "990", nil },
	})
	for uid, want := range map[int]bool{0: true, 1000: true, 1001: false} {
		if got := admins.IsAdmin(uid); got != want {
			t.Errorf("IsAdmin(%d) = %v, want %v", uid, got, want)
		}
	}
	var none *Admins
	if !none.IsAdmin(0) || none.IsAdmin(1000) {
		t.Error("nil Admins must know only root")
	}
}
