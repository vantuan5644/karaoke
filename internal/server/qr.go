package server

import (
	"fmt"
	"net"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"rsc.io/qr"
)

// qr draws ?text= as an SVG QR code, for the TV's "scan to add songs".
func (s *Server) qr(w http.ResponseWriter, r *http.Request) {
	text := r.URL.Query().Get("text")
	if text == "" || len(text) > 512 {
		writeError(w, http.StatusBadRequest, "text: 1 to 512 bytes")
		return
	}
	svg, err := qrSVG(text)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	w.Header().Set("Content-Type", "image/svg+xml")
	w.Header().Set("Cache-Control", "public, max-age=86400")
	w.Write([]byte(svg))
}

func qrSVG(text string) (string, error) {
	code, err := qr.Encode(text, qr.M)
	if err != nil {
		return "", err
	}
	const quiet = 4 // the blank margin scanners need, in modules
	size := code.Size + 2*quiet
	var path strings.Builder
	for y := 0; y < code.Size; y++ {
		for x := 0; x < code.Size; x++ {
			if code.Black(x, y) {
				fmt.Fprintf(&path, "M%d %dh1v1h-1z", x+quiet, y+quiet)
			}
		}
	}
	return fmt.Sprintf(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 %d %d" shape-rendering="crispEdges">`+
		`<rect width="100%%" height="100%%" fill="#fff"/><path fill="#000" d="%s"/></svg>`, size, size, path.String()), nil
}

// LocalURLs lists the addresses other devices on the network could reach port at: private
// LAN addresses first, then Tailscale's, then anything else that isn't loopback.
func LocalURLs(port int, secure bool) []string {
	scheme := "http"
	if secure {
		scheme = "https"
	}
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return nil
	}
	type candidate struct {
		ip   net.IP
		rank int
	}
	var found []candidate
	for _, a := range addrs {
		ipnet, ok := a.(*net.IPNet)
		if !ok {
			continue
		}
		ip := ipnet.IP.To4()
		if ip == nil || ip.IsLoopback() || ip.IsLinkLocalUnicast() {
			continue
		}
		found = append(found, candidate{ip, addressRank(ip)})
	}
	sort.SliceStable(found, func(i, j int) bool { return found[i].rank < found[j].rank })
	urls := make([]string, len(found))
	for i, c := range found {
		urls[i] = scheme + "://" + net.JoinHostPort(c.ip.String(), strconv.Itoa(port))
	}
	return urls
}

func addressRank(ip net.IP) int {
	switch {
	case ip[0] == 192 && ip[1] == 168:
		return 0
	case ip[0] == 10:
		return 1
	case ip[0] == 172 && ip[1]&0xf0 == 16:
		// Docker's bridges live here too, so they come after a real LAN.
		return 2
	case ip[0] == 100 && ip[1]&0xc0 == 64: // Tailscale's 100.64.0.0/10
		return 3
	default:
		return 4
	}
}
