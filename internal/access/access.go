// Package access decides which local users may change what reign runs.
//
// reign runs services as root, so anyone who can create or start a service
// is effectively root. Only root and members of the admin group are allowed.
package access

import (
	"os/user"
	"slices"
	"strconv"
)

// DefaultAdminGroup is the group whose members may change services.
const DefaultAdminGroup = "reign"

// Admins answers whether a UID may change services.
type Admins struct {
	Group string // members of this group are admins, in addition to root

	// lookup returns the group IDs of a user and the ID of a group. Tests
	// replace it; nil uses os/user.
	lookup func(uid int, group string) (userGIDs []string, groupGID string, err error)
}

// NewAdmins returns the admin set for group; an empty group means root only.
func NewAdmins(group string) *Admins {
	return &Admins{Group: group}
}

// NewAdminsWithLookup is NewAdmins with a replacement for the user and group
// database, for tests.
func NewAdminsWithLookup(group string, lookup func(uid int, group string) ([]string, string, error)) *Admins {
	return &Admins{Group: group, lookup: lookup}
}

// IsAdmin reports whether uid is root or a member of the admin group.
// Lookup errors deny access.
func (a *Admins) IsAdmin(uid int) bool {
	if uid == 0 {
		return true
	}
	if a == nil || a.Group == "" {
		return false
	}
	lookup := a.lookup
	if lookup == nil {
		lookup = osLookup
	}
	gids, gid, err := lookup(uid, a.Group)
	if err != nil {
		return false
	}
	return slices.Contains(gids, gid)
}

// GroupExists reports whether the admin group exists on this host.
func (a *Admins) GroupExists() bool {
	if a == nil || a.Group == "" {
		return false
	}
	_, err := user.LookupGroup(a.Group)
	return err == nil
}

func osLookup(uid int, group string) ([]string, string, error) {
	u, err := user.LookupId(strconv.Itoa(uid))
	if err != nil {
		return nil, "", err
	}
	gids, err := u.GroupIds()
	if err != nil {
		// The primary group is still known when supplementary groups are not.
		gids = []string{u.Gid}
	}
	g, err := user.LookupGroup(group)
	if err != nil {
		return nil, "", err
	}
	return gids, g.Gid, nil
}
