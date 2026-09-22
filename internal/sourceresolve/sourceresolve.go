// Package sourceresolve reads the metadata of a grounding source: the address that answered,
// the redirect chain, the status, and the dates. It never returns a response body, so no
// retrieved text reaches a model through this path (SDD §14.3).
package sourceresolve

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"syscall"
	"time"

	"github.com/alternayte/speccy/internal/engine/sourcepolicy"
)

// MaxHops is the number of redirects Speccy follows before it gives up.
const MaxHops = 5

// Timeout is the budget for one source, over every hop.
const Timeout = 15 * time.Second

// Resolver reads source metadata under a policy. The policy is checked before every request,
// including every redirect hop, so a hop to a forbidden host stops before the request.
type Resolver struct {
	HTTP *http.Client
	// Now is the clock, for tests.
	Now func() time.Time
}

// New returns a resolver whose transport refuses a private or loopback address, checks the
// address it is about to connect to rather than the name, and follows no redirect on its own.
func New() *Resolver {
	tr := &http.Transport{
		DialContext: (&net.Dialer{
			Timeout:   5 * time.Second,
			KeepAlive: 5 * time.Second,
			Control:   controlAddr,
		}).DialContext,
		TLSHandshakeTimeout:   5 * time.Second,
		ResponseHeaderTimeout: 10 * time.Second,
		DisableKeepAlives:     true,
		MaxIdleConns:          4,
	}
	return &Resolver{HTTP: &http.Client{
		Transport:     tr,
		Timeout:       Timeout,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}}
}

// ErrBlocked is the cause when the policy or the address rules stop a request.
var ErrBlocked = errors.New("blocked")

// Resolve reads the metadata of one source. It returns the source with its chain, its final
// URL, its status and its dates. A source the policy refuses comes back dropped, with the
// reason, and Speccy made no request to the refused host.
func (r *Resolver) Resolve(ctx context.Context, p sourcepolicy.Policy, raw string) sourcepolicy.Source {
	s := sourcepolicy.Source{URL: raw}
	now := time.Now
	if r.Now != nil {
		now = r.Now
	}
	ctx, cancel := context.WithTimeout(ctx, Timeout)
	defer cancel()

	next := raw
	for hop := 0; hop <= MaxHops; hop++ {
		u, err := url.Parse(next)
		if err != nil {
			return drop(s, "The source is not a URL.")
		}
		if u.Scheme != "http" && u.Scheme != "https" {
			return drop(s, fmt.Sprintf("Speccy reads http and https only, and the source uses %s.", u.Scheme))
		}
		if port := u.Port(); port != "" && port != "80" && port != "443" {
			return drop(s, fmt.Sprintf("Speccy reads port 80 and 443 only, and the source uses port %s.", port))
		}
		host := strings.ToLower(strings.TrimSuffix(u.Hostname(), "."))
		if state, why := p.CheckHost(host); state != sourcepolicy.HostOK {
			return drop(s, why)
		}
		s.Chain = append(s.Chain, next)

		resp, err := r.request(ctx, next)
		if err != nil {
			if errors.Is(err, ErrBlocked) {
				return drop(s, fmt.Sprintf("Speccy does not read %s, because it resolves to an address that is not public.", host))
			}
			return drop(s, "Speccy could not read the source: "+err.Error())
		}
		loc := resp.Header.Get("Location")
		status := resp.StatusCode
		modified := parseDate(resp.Header.Get("Last-Modified"))
		date := parseDate(resp.Header.Get("Date"))
		_ = resp.Body.Close() // the body never leaves this function

		if status >= 300 && status < 400 && loc != "" {
			ref, err := u.Parse(loc)
			if err != nil {
				return drop(s, "The source redirects to an address Speccy cannot read.")
			}
			next = ref.String()
			continue
		}
		at := now().UTC()
		s.FinalURL = next
		s.Status = status
		s.RetrievedAt = &at
		if modified != nil {
			s.Modified = modified
		} else if date != nil {
			s.Modified = date
		}
		if status >= 400 {
			return drop(s, fmt.Sprintf("The source answered %d.", status))
		}
		return s
	}
	return drop(s, fmt.Sprintf("The source redirects more than %d times.", MaxHops))
}

// request sends a HEAD, and falls back to a GET that asks for one byte when the server
// refuses a HEAD. Neither reads the body.
func (r *Resolver) request(ctx context.Context, target string) (*http.Response, error) {
	resp, err := r.do(ctx, http.MethodHead, target, nil)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode == http.StatusMethodNotAllowed || resp.StatusCode == http.StatusNotImplemented {
		_ = resp.Body.Close()
		return r.do(ctx, http.MethodGet, target, map[string]string{"Range": "bytes=0-0"})
	}
	return resp, nil
}

func (r *Resolver) do(ctx context.Context, method, target string, hdr map[string]string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, target, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "speccy-source-resolver")
	req.Header.Set("Accept", "*/*")
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	return r.HTTP.Do(req)
}

func drop(s sourcepolicy.Source, reason string) sourcepolicy.Source {
	s.Dropped, s.Reason = true, reason
	return s
}

// controlAddr refuses a connection to an address that is not public. It runs after the name
// resolves and before the connection opens, so a name that resolves to a private address, or
// a name that changes its answer between checks, is refused here.
func controlAddr(_, address string, _ syscall.RawConn) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return fmt.Errorf("%w: %s", ErrBlocked, address)
	}
	ip := net.ParseIP(host)
	if ip == nil || !public(ip) {
		return fmt.Errorf("%w: %s", ErrBlocked, host)
	}
	return nil
}

// public reports whether an address is one Speccy may reach.
func public(ip net.IP) bool {
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsUnspecified() ||
		ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsInterfaceLocalMulticast() || ip.IsMulticast() {
		return false
	}
	if v4 := ip.To4(); v4 != nil {
		switch {
		case v4[0] == 100 && v4[1]&0xc0 == 64: // 100.64.0.0/10, carrier-grade NAT
			return false
		case v4[0] == 192 && v4[1] == 0 && v4[2] == 0: // 192.0.0.0/24
			return false
		case v4[0] == 192 && v4[1] == 0 && v4[2] == 2: // TEST-NET-1
			return false
		case v4[0] == 198 && v4[1]&0xfe == 18: // 198.18.0.0/15, benchmarking
			return false
		case v4[0] == 198 && v4[1] == 51 && v4[2] == 100: // TEST-NET-2
			return false
		case v4[0] == 203 && v4[1] == 0 && v4[2] == 113: // TEST-NET-3
			return false
		case v4[0] >= 240: // reserved, and the broadcast address
			return false
		}
		return true
	}
	if len(ip) == net.IPv6len {
		if ip[0] == 0xfc || ip[0] == 0xfd { // unique local
			return false
		}
		if ip[0] == 0x20 && ip[1] == 0x01 && ip[2] == 0x00 && ip[3]&0xf0 == 0x00 { // 2001::/23, IETF
			return false
		}
	}
	return true
}

var dateFormats = []string{http.TimeFormat, time.RFC1123, time.RFC1123Z, time.RFC850, time.ANSIC}

func parseDate(v string) *time.Time {
	v = strings.TrimSpace(v)
	if v == "" {
		return nil
	}
	for _, f := range dateFormats {
		if t, err := time.Parse(f, v); err == nil {
			u := t.UTC()
			return &u
		}
	}
	return nil
}
