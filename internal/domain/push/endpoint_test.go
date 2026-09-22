package push

import "testing"

// A signed-in user controls the endpoint field. Without this allow-list,
// registering an internal cluster address here would turn Task 10's sender
// into an SSRF from inside the auth boundary.
func TestValidWebPushEndpoint(t *testing.T) {
	cases := []struct {
		name string
		url  string
		want bool
	}{
		{"fcm", "https://fcm.googleapis.com/fcm/send/abc123", true},
		{"mozilla", "https://updates.push.services.mozilla.com/wpush/v2/abc123", true},
		{"apple web push", "https://web.push.apple.com/v1/abc123", true},
		{"wns subdomain suffix", "https://xyz123.notify.windows.com/w/abc", true},
		{"apns subdomain suffix", "https://xyz123.push.apple.com/3/device/abc", true},
		{"port on an allowed host is still allowed", "https://fcm.googleapis.com:443/fcm/send/abc123", true},

		{"http scheme is rejected even on an allowed host", "http://fcm.googleapis.com/fcm/send/abc123", false},
		{"internal cluster host", "https://whisper.horus.svc.cluster.local:8000/v1/audio/transcriptions", false},
		{"ip literal", "https://10.0.0.5/fcm/send/abc123", false},
		{"lookalike suffix", "https://fcm.googleapis.com.evil.test/fcm/send/abc123", false},
		{"userinfo embedded", "https://user:pass@fcm.googleapis.com/fcm/send/abc123", false},
		{"not a url at all", "not a url", false},
		{"empty", "", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ValidWebPushEndpoint(tc.url); got != tc.want {
				t.Errorf("ValidWebPushEndpoint(%q) = %v, want %v", tc.url, got, tc.want)
			}
		})
	}
}
