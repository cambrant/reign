package handlers

import (
	"encoding/hex"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/user"
	"strconv"
	"strings"
)

// RequesterHeader carries the CLI's self-reported identity, e.g. "tim@kraut pts/0 ppid=4242".
const RequesterHeader = "X-Reign-Requester"

// maxRequesterLen bounds the self-reported part so a client can't flood the event log.
const maxRequesterLen = 200

// requesterFromRequest describes who sent r for the event log. It combines the
// client's self-reported X-Reign-Requester header with what the server can
// verify itself: the remote address and, for loopback TCP connections on
// Linux, the UID that owns the client socket (from /proc/net/tcp). The header
// alone can be forged; the UID cannot.
func requesterFromRequest(r *http.Request) string {
	claimed := sanitizeRequester(r.Header.Get(RequesterHeader))
	if claimed == "" {
		claimed = "unknown client"
	}

	var verified []string
	if local, ok := r.Context().Value(http.LocalAddrContextKey).(net.Addr); ok {
		if uid, ok := loopbackPeerUID(r.RemoteAddr, local.String()); ok {
			verified = append(verified, "uid="+uidName(uid))
		}
	}
	verified = append(verified, r.RemoteAddr)

	return fmt.Sprintf("%s [%s]", claimed, strings.Join(verified, " "))
}

// sanitizeRequester reduces a client-supplied requester string to an
// allowlist of printable ASCII and caps its length. Brackets are not allowed,
// so a client cannot fake the server-verified "[uid=... addr]" suffix, and
// control, bidi and other non-printing characters cannot reach the log.
func sanitizeRequester(s string) string {
	s = strings.Map(func(r rune) rune {
		if allowedRequesterRune(r) {
			return r
		}
		return -1
	}, s)
	s = strings.TrimSpace(s)
	if len(s) > maxRequesterLen {
		s = s[:maxRequesterLen] // ASCII only, so this cuts on a character boundary
	}
	return s
}

// allowedRequesterRune reports whether r may appear in a self-reported requester.
func allowedRequesterRune(r rune) bool {
	switch {
	case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		return true
	}
	return strings.ContainsRune("@._:/ =()+-", r)
}

// uidName renders a UID as "1000(tim)", or just the number if it has no user.
func uidName(uid int) string {
	if u, err := user.LookupId(strconv.Itoa(uid)); err == nil {
		return fmt.Sprintf("%d(%s)", uid, u.Username)
	}
	return strconv.Itoa(uid)
}

// loopbackPeerUID finds the UID owning the client end of a loopback TCP
// connection. It only works on Linux and only for loopback peers, where the
// client socket is visible in this host's /proc/net/tcp{,6}.
func loopbackPeerUID(remoteAddr, localAddr string) (int, bool) {
	remote, err := net.ResolveTCPAddr("tcp", remoteAddr)
	if err != nil || !remote.IP.IsLoopback() {
		return 0, false
	}
	local, err := net.ResolveTCPAddr("tcp", localAddr)
	if err != nil {
		return 0, false
	}
	for _, f := range []string{"/proc/net/tcp", "/proc/net/tcp6"} {
		data, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		if uid, ok := findSocketUID(string(data), remote, local); ok {
			return uid, true
		}
	}
	return 0, false
}

// findSocketUID scans /proc/net/tcp-format content for the socket whose local
// address is the client (remote) and whose peer is the server (local).
func findSocketUID(table string, client, server *net.TCPAddr) (int, bool) {
	for _, line := range strings.Split(table, "\n")[1:] {
		fields := strings.Fields(line)
		if len(fields) < 8 {
			continue
		}
		l, ok1 := parseProcAddr(fields[1])
		r, ok2 := parseProcAddr(fields[2])
		if !ok1 || !ok2 {
			continue
		}
		if sameAddr(l, client) && sameAddr(r, server) {
			uid, err := strconv.Atoi(fields[7])
			if err != nil {
				return 0, false
			}
			return uid, true
		}
	}
	return 0, false
}

func sameAddr(a, b *net.TCPAddr) bool {
	return a.Port == b.Port && a.IP.Equal(b.IP)
}

// parseProcAddr parses "0100007F:1ED2" as found in /proc/net/tcp. The address
// is stored as 32-bit words in host (little-endian) byte order.
func parseProcAddr(s string) (*net.TCPAddr, bool) {
	host, port, ok := strings.Cut(s, ":")
	if !ok {
		return nil, false
	}
	raw, err := hex.DecodeString(host)
	if err != nil || (len(raw) != 4 && len(raw) != 16) {
		return nil, false
	}
	ip := make(net.IP, len(raw))
	for i := 0; i < len(raw); i += 4 {
		ip[i], ip[i+1], ip[i+2], ip[i+3] = raw[i+3], raw[i+2], raw[i+1], raw[i]
	}
	p, err := strconv.ParseUint(port, 16, 16)
	if err != nil {
		return nil, false
	}
	return &net.TCPAddr{IP: ip, Port: int(p)}, true
}
