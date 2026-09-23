package localcontext

import (
	"errors"
	"net/url"
	"strings"
)

// redactErr renders err for a log line with the query string cut from any
// request URL it quotes. Transport errors embed the full URL, and for the
// providers here that URL carries the caller's position.
func redactErr(err error) string {
	if err == nil {
		return ""
	}
	msg := err.Error()
	var ue *url.Error
	if errors.As(err, &ue) {
		if u, perr := url.Parse(ue.URL); perr == nil {
			u.RawQuery = ""
			msg = strings.ReplaceAll(msg, ue.URL, u.String())
		}
	}
	return msg
}
