package cli

import (
	"fmt"
	"os"
	"os/user"
	"strings"
)

// requesterHeader is the header the server reads the CLI's identity from.
const requesterHeader = "X-Reign-Requester"

// requesterIdentity describes the person and terminal running the CLI, e.g.
// "tim@kraut pts/0 sudo ppid=4242". It is self-reported: the server adds what
// it can verify itself (remote address, socket owner UID).
func requesterIdentity() string {
	return formatRequester(currentUser(), os.Getenv("SUDO_USER"), hostname(), terminal(), os.Getppid())
}

// formatRequester builds the identity string from its parts. With sudo, the
// invoking user (SUDO_USER) is reported, followed by "sudo".
func formatRequester(userName, sudoUser, host, tty string, ppid int) string {
	who := userName
	if sudoUser != "" {
		who = sudoUser
	}
	if who == "" {
		who = "unknown"
	}
	parts := []string{who}
	if host != "" {
		parts[0] += "@" + host
	}
	if tty != "" {
		parts = append(parts, tty)
	}
	if sudoUser != "" {
		parts = append(parts, "sudo")
	}
	parts = append(parts, fmt.Sprintf("ppid=%d", ppid))
	return strings.Join(parts, " ")
}

func currentUser() string {
	if u := os.Getenv("USER"); u != "" {
		return u
	}
	if u, err := user.Current(); err == nil {
		return u.Username
	}
	return ""
}

func hostname() string {
	h, _ := os.Hostname()
	if i := strings.IndexByte(h, '.'); i > 0 {
		h = h[:i]
	}
	return h
}

// terminal returns the controlling terminal of stdin, e.g. "pts/0", or "" if
// stdin is not a terminal or it cannot be determined (non-Linux).
func terminal() string {
	target, err := os.Readlink("/proc/self/fd/0")
	if err != nil || !strings.HasPrefix(target, "/dev/") {
		return ""
	}
	tty := strings.TrimPrefix(target, "/dev/")
	if strings.HasPrefix(tty, "pts/") || strings.HasPrefix(tty, "tty") {
		return tty
	}
	return ""
}
