package handlers

import (
	"mime"
	"net"
	"net/http"
	"os"
	"strings"

	"github.com/reign/internal/access"
	"github.com/reign/internal/logger"
)

// Guard protects the API in front of the route handlers.
//
// reign runs services as root and the API has no credentials of its own, so
// the guard does two things:
//
//   - It rejects anything a web browser could send. A page on any site can
//     make the browser POST to a loopback port, and DNS rebinding lets a page
//     read the responses too. Browsers always add an Origin or Sec-Fetch-Site
//     header to such requests, a rebound request carries the attacker's domain
//     in Host, and a "simple" cross-site POST cannot be labelled
//     application/json. The CLI sends none of these.
//   - It lets only root and members of the admin group change anything
//     (every method except GET and HEAD). The caller is identified by the UID
//     that owns the client socket, which only works for loopback connections
//     on Linux; any other caller is refused.
type Guard struct {
	next   http.Handler
	admins *access.Admins
	open   bool            // skip the UID check (apiAuth "none")
	hosts  map[string]bool // accepted Host names besides IP literals and localhost

	// peerUID finds the UID owning the client socket; tests replace it.
	peerUID func(r *http.Request) (int, bool)
}

// GuardOptions configures a Guard.
type GuardOptions struct {
	Admins       *access.Admins
	Open         bool     // let any caller change services
	ListenAddr   string   // its host is accepted in the Host header
	AllowedHosts []string // further accepted Host names
}

// NewGuard wraps next.
func NewGuard(next http.Handler, opts GuardOptions) *Guard {
	hosts := map[string]bool{"localhost": true}
	if host, _, err := net.SplitHostPort(opts.ListenAddr); err == nil && host != "" {
		hosts[strings.ToLower(host)] = true
	}
	if name, err := os.Hostname(); err == nil && name != "" {
		name = strings.ToLower(name)
		hosts[name] = true
		if short, _, ok := strings.Cut(name, "."); ok {
			hosts[short] = true
		}
	}
	for _, h := range opts.AllowedHosts {
		hosts[strings.ToLower(h)] = true
	}
	return &Guard{
		next:    next,
		admins:  opts.Admins,
		open:    opts.Open,
		hosts:   hosts,
		peerUID: requestPeerUID,
	}
}

// ServeHTTP checks the request and passes it on.
func (g *Guard) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if reason := g.browserReason(r); reason != "" {
		logger.Warn("Rejected API request %s %s from %s: %s", r.Method, r.URL.Path, r.RemoteAddr, reason)
		writeError(w, http.StatusForbidden, "request rejected: "+reason)
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead && !g.open {
		uid, ok := g.peerUID(r)
		if !ok || !g.admins.IsAdmin(uid) {
			logger.Warn("Refused %s %s from %s: not root or in the admin group", r.Method, r.URL.Path, requesterFromRequest(r))
			writeError(w, http.StatusForbidden, g.denyMessage(ok))
			return
		}
	}
	g.next.ServeHTTP(w, r)
}

// browserReason returns why r looks like it came from a web browser or a
// rebound DNS name, or "" if it does not.
func (g *Guard) browserReason(r *http.Request) string {
	if r.Header.Get("Origin") != "" {
		return "Origin header not allowed"
	}
	if site := r.Header.Get("Sec-Fetch-Site"); site != "" && site != "none" {
		return "browser requests are not allowed"
	}
	if !g.allowedHost(r.Host) {
		return "unexpected Host header"
	}
	if r.ContentLength != 0 {
		mt, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if err != nil || mt != "application/json" {
			return "Content-Type must be application/json"
		}
	}
	return ""
}

// allowedHost accepts IP literals (a rebound DNS name is never one),
// localhost, the listen host, this machine's hostname and configured names.
func (g *Guard) allowedHost(hostport string) bool {
	host := hostport
	if h, _, err := net.SplitHostPort(hostport); err == nil {
		host = h
	}
	host = strings.TrimSuffix(strings.Trim(host, "[]"), ".")
	if host == "" {
		return false
	}
	if net.ParseIP(host) != nil {
		return true
	}
	return g.hosts[strings.ToLower(host)]
}

func (g *Guard) denyMessage(uidKnown bool) string {
	group := "the admin group"
	if g.admins != nil && g.admins.Group != "" {
		group = "the " + g.admins.Group + " group"
	}
	if !uidKnown {
		return "changes are only accepted from local root or members of " + group + " (could not identify the calling user)"
	}
	return "changes require root or membership of " + group
}

// requestPeerUID returns the UID owning the client end of r's connection.
func requestPeerUID(r *http.Request) (int, bool) {
	local, ok := r.Context().Value(http.LocalAddrContextKey).(net.Addr)
	if !ok {
		return 0, false
	}
	return loopbackPeerUID(r.RemoteAddr, local.String())
}
