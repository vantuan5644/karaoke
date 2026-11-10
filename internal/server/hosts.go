package server

import (
	"fmt"
	"net"
	"net/http"
	"strings"
)

// Names a home network or a VPN gives machines. A page on some other site that points its
// own domain at this server (DNS rebinding) arrives with that domain in Host, which is none of
// these, so it is turned away before it can use the API as if it were this site.
var homeSuffixes = []string{".local", ".lan", ".home", ".home.arpa", ".internal", ".localdomain", ".localhost", ".ts.net"}

func hostName(hostport string) string {
	host := hostport
	if h, _, err := net.SplitHostPort(hostport); err == nil {
		host = h
	}
	return strings.TrimSuffix(strings.ToLower(strings.Trim(host, "[]")), ".")
}

// homeHost says whether host can only be this machine as someone at home reached it: an
// address, localhost, a bare machine name, a home or VPN name, or one the owner allowed.
func homeHost(host string, allowed map[string]bool) bool {
	if host == "" || allowed[host] || host == "localhost" || net.ParseIP(host) != nil {
		return host != ""
	}
	if !strings.Contains(host, ".") {
		return true
	}
	for _, suffix := range homeSuffixes {
		if strings.HasSuffix(host, suffix) {
			return true
		}
	}
	return false
}

func knownHosts(allow []string, next http.Handler) http.Handler {
	allowed := map[string]bool{}
	for _, h := range allow {
		if h = hostName(strings.TrimSpace(h)); h != "" {
			allowed[h] = true
		}
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if host := hostName(r.Host); !homeHost(host, allowed) {
			writeError(w, http.StatusMisdirectedRequest,
				fmt.Sprintf("this server doesn't answer to %q; start it with -allow-host %s to use that name", host, host))
			return
		}
		next.ServeHTTP(w, r)
	})
}
