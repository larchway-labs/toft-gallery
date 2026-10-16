package main

import (
	"fmt"
	"net"
	"net/url"
	"strings"
)

// A target address is something somebody typed, so it is server-side input and
// gets the treatment that deserves: https only, no credentials in the URL, and
// no private, loopback or link-local resolved address.
//
// With one deliberate exception. An on-premises estate's other installs are
// frequently on RFC1918, so a blanket refusal would make the feature unusable
// in exactly the environment it is for. The console therefore takes an
// operator-set allow-list of private ranges rather than pretending the rule can
// be absolute.
//
// This is the console's own decision and is stated as such. It is **not** the
// platform's `safefetch` and must not be described as it.
func validateTargetURL(raw string, allowedRanges []string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", fmt.Errorf("an address is required")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("that is not a URL: %v", err)
	}
	if u.Scheme != "https" && u.Scheme != "http" {
		return "", fmt.Errorf("the address must start with https:// (http:// is accepted only for a host inside an allowed private range)")
	}
	if u.User != nil {
		return "", fmt.Errorf("take the credentials out of the URL — the key field below is where the credential goes")
	}
	if u.Host == "" {
		return "", fmt.Errorf("the address has no host")
	}
	if u.Path != "" && u.Path != "/" {
		return "", fmt.Errorf("give the install's address only, with no path after it")
	}

	host := u.Hostname()
	ips, err := net.LookupIP(host)
	if err != nil || len(ips) == 0 {
		return "", fmt.Errorf("%s does not resolve from this install", host)
	}
	nets := parseCIDRs(allowedRanges)
	for _, ip := range ips {
		if !isPrivate(ip) {
			continue
		}
		if !inAny(ip, nets) {
			return "", fmt.Errorf("%s resolves to %s, which is a private address this console has not been told to allow — add the range on Settings, or use a public name",
				host, ip)
		}
		// A private address that *is* allowed may be plain http: an estate on
		// RFC1918 frequently terminates TLS somewhere else, and refusing it
		// would be a rule that only ever refuses the intended case.
		return strings.TrimRight(u.Scheme+"://"+u.Host, "/"), nil
	}
	if u.Scheme != "https" {
		return "", fmt.Errorf("a public address must be https")
	}
	return strings.TrimRight(u.Scheme+"://"+u.Host, "/"), nil
}

func parseCIDRs(ranges []string) []*net.IPNet {
	var out []*net.IPNet
	for _, r := range ranges {
		r = strings.TrimSpace(r)
		if r == "" {
			continue
		}
		if _, n, err := net.ParseCIDR(r); err == nil {
			out = append(out, n)
		}
	}
	return out
}

func inAny(ip net.IP, nets []*net.IPNet) bool {
	for _, n := range nets {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}

func isPrivate(ip net.IP) bool {
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() || ip.IsUnspecified()
}
