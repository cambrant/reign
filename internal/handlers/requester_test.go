package handlers

import (
	"net"
	"net/http/httptest"
	"strings"
	"testing"
)

// procTCP is /proc/net/tcp with both ends of a CLI connection to reign on
// 127.0.0.1:7890 (0x1ED2) from port 54321 (0xD431): the client socket owned by
// uid 1000 and the accepted server socket owned by root.
const procTCP = `  sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode
   0: 0100007F:1ED2 00000000:0000 0A 00000000:00000000 00:00000000 00000000     0        0 1 1 0000000000000000 100 0 0 10 0
   1: 0100007F:D431 0100007F:1ED2 01 00000000:00000000 00:00000000 00000000  1000        0 2 1 0000000000000000 20 4 30 10 -1
   2: 0100007F:1ED2 0100007F:D431 01 00000000:00000000 00:00000000 00000000     0        0 3 1 0000000000000000 20 4 30 10 -1
`

func TestFindSocketUID(t *testing.T) {
	server := &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 7890}
	tests := []struct {
		name    string
		client  *net.TCPAddr
		wantUID int
		wantOK  bool
	}{
		{"client socket found", &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 54321}, 1000, true},
		{"unknown port", &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 1}, 0, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			uid, ok := findSocketUID(procTCP, tt.client, server)
			if ok != tt.wantOK || uid != tt.wantUID {
				t.Errorf("findSocketUID = (%d, %v), want (%d, %v)", uid, ok, tt.wantUID, tt.wantOK)
			}
		})
	}
}

// TestFindSocketUIDIgnoresDeadRows checks that rows that are not a live
// socket (TIME_WAIT, inode 0, both reporting uid 0) never identify a caller
// as root, and that duplicate live rows identify nobody.
func TestFindSocketUIDIgnoresDeadRows(t *testing.T) {
	server := &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 7890}
	client := &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 54321}
	header := "  sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode\n"
	timeWait := "   1: 0100007F:D431 0100007F:1ED2 06 00000000:00000000 03:00000000 00000000     0        0 0 3 0000000000000000\n"
	noInode := "   1: 0100007F:D431 0100007F:1ED2 01 00000000:00000000 00:00000000 00000000     0        0 0 1 0000000000000000 20 4 30 10 -1\n"
	live := "   2: 0100007F:D431 0100007F:1ED2 01 00000000:00000000 00:00000000 00000000  1000        0 2 1 0000000000000000 20 4 30 10 -1\n"
	tests := []struct {
		name    string
		table   string
		wantUID int
		wantOK  bool
	}{
		{"time_wait only", header + timeWait, 0, false},
		{"inode 0 only", header + noInode, 0, false},
		{"time_wait next to live socket", header + timeWait + live, 1000, true},
		{"two live rows", header + live + live, 0, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			uid, ok := findSocketUID(tt.table, client, server)
			if ok != tt.wantOK || uid != tt.wantUID {
				t.Errorf("findSocketUID = (%d, %v), want (%d, %v)", uid, ok, tt.wantUID, tt.wantOK)
			}
		})
	}
}

func TestParseProcAddr(t *testing.T) {
	tests := []struct {
		in     string
		want   string
		wantOK bool
	}{
		{"0100007F:1ED2", "127.0.0.1:7890", true},
		{"00000000000000000000000001000000:1ED2", "[::1]:7890", true},
		{"zz:1ED2", "", false},
		{"0100007F", "", false},
	}
	for _, tt := range tests {
		got, ok := parseProcAddr(tt.in)
		if ok != tt.wantOK {
			t.Errorf("parseProcAddr(%q) ok = %v, want %v", tt.in, ok, tt.wantOK)
			continue
		}
		if ok && got.String() != tt.want {
			t.Errorf("parseProcAddr(%q) = %s, want %s", tt.in, got, tt.want)
		}
	}
}

func TestRequesterFromRequest(t *testing.T) {
	tests := []struct {
		name   string
		header string
		want   []string
		reject []string
	}{
		{"cli header", "tim@kraut pts/0 ppid=42", []string{"tim@kraut pts/0 ppid=42", "192.0.2.1:1234"}, nil},
		{"no header", "", []string{"unknown client", "192.0.2.1:1234"}, nil},
		{"control characters stripped", "evil\nINJECTED\x1b[31m", []string{"evilINJECTED31m"}, []string{"\n", "\x1b"}},
		{"forged verified suffix", "tim [uid=0(root) 127.0.0.1:1]", []string{"tim uid=0(root) 127.0.0.1:1 [192.0.2.1:1234]"}, []string{"tim ["}},
		{"bidi override stripped", "tim\u202eroot", []string{"timroot"}, []string{"\u202e"}},
		{"long header truncated", strings.Repeat("a", 500), []string{strings.Repeat("a", maxRequesterLen) + " ["}, []string{strings.Repeat("a", maxRequesterLen+1)}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequest("POST", "/services/app/restart", nil)
			r.RemoteAddr = "192.0.2.1:1234"
			if tt.header != "" {
				r.Header.Set(RequesterHeader, tt.header)
			}
			got := requesterFromRequest(r)
			for _, w := range tt.want {
				if !strings.Contains(got, w) {
					t.Errorf("requester %q does not contain %q", got, w)
				}
			}
			for _, w := range tt.reject {
				if strings.Contains(got, w) {
					t.Errorf("requester %q contains %q", got, w)
				}
			}
		})
	}
}
