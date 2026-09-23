package push

import (
	"net/url"
	"strings"
)

// webPushAllowedHosts is the allow-list of push-service origins a browser's
// PushSubscription.endpoint can legitimately point at. Anything else,
// including a lookalike suffix or an in-cluster hostname, is refused: a
// signed-in user could otherwise register an internal address here and have
// the push sender POST to it on their behalf, which is an SSRF from inside
// the auth boundary rather than outside it.
var webPushAllowedHosts = map[string]bool{
	"fcm.googleapis.com":                true,
	"updates.push.services.mozilla.com": true,
	"web.push.apple.com":                true,
}

// webPushAllowedSuffixes covers the per-tenant subdomains WNS and APNs web
// push use (e.g. "xyz.notify.windows.com", "xyz.push.apple.com"). The suffix
// includes the leading ".", so the bare parent domain ("notify.windows.com",
// "push.apple.com") does not match — it is not itself a push host.
var webPushAllowedSuffixes = []string{
	".notify.windows.com",
	".push.apple.com",
}

// ValidWebPushEndpoint reports whether raw is an https URL, with no
// credentials embedded, whose host (port stripped, case-insensitively) is
// one of the known push-service origins. The notifier calls it again as
// defence in depth before it ever dials an endpoint pulled back out of the
// database.
func ValidWebPushEndpoint(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	if u.Scheme != "https" {
		return false
	}
	if u.User != nil {
		return false
	}
	host := strings.ToLower(u.Hostname())
	if host == "" {
		return false
	}
	if webPushAllowedHosts[host] {
		return true
	}
	for _, suffix := range webPushAllowedSuffixes {
		if strings.HasSuffix(host, suffix) {
			return true
		}
	}
	return false
}
