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

// Lookup is the user and group database. Tests replace it.
type Lookup struct {
	UserGroups func(uid int) ([]string, error)   // GIDs the user belongs to, primary included
	GroupID    func(name string) (string, error) // GID of a group name
}

// OSLookup reads the host's user and group database.
var OSLookup = Lookup{
	UserGroups: func(uid int) ([]string, error) {
		u, err := user.LookupId(strconv.Itoa(uid))
		if err != nil {
			return nil, err
		}
		gids, err := u.GroupIds()
		if err != nil {
			// The primary group is still known when supplementary groups are not.
			return []string{u.Gid}, nil
		}
		return gids, nil
	},
	GroupID: func(name string) (string, error) {
		g, err := user.LookupGroup(name)
		if err != nil {
			return "", err
		}
		return g.Gid, nil
	},
}

// Admins answers whether a UID may change services.
type Admins struct {
	Group  string // members of this group are admins, in addition to root
	lookup Lookup
}

// NewAdmins returns the admin set for group; an empty group means root only.
func NewAdmins(group string) *Admins {
	return &Admins{Group: group, lookup: OSLookup}
}

// NewAdminsWithLookup is NewAdmins with a replacement user and group
// database, for tests.
func NewAdminsWithLookup(group string, lookup Lookup) *Admins {
	return &Admins{Group: group, lookup: lookup}
}

// IsAdmin reports whether uid is root or a member of the admin group.
// Lookup errors deny access. A nil Admins knows only root.
func (a *Admins) IsAdmin(uid int) bool {
	if uid == 0 {
		return true
	}
	if a == nil || a.Group == "" {
		return false
	}
	gid, err := a.lookup.GroupID(a.Group)
	if err != nil {
		return false
	}
	gids, err := a.lookup.UserGroups(uid)
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
	_, err := a.lookup.GroupID(a.Group)
	return err == nil
}

// userGroups returns the GIDs of uid, or nil if they cannot be looked up.
func (a *Admins) userGroups(uid int) []string {
	lookup := OSLookup
	if a != nil && a.lookup.UserGroups != nil {
		lookup = a.lookup
	}
	gids, err := lookup.UserGroups(uid)
	if err != nil {
		return nil
	}
	return gids
}
