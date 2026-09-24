package push

import (
	"context"
	"encoding/json"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/FACorreiaa/loci-connect-api/internal/domain/runs"
)

// WebOrigin is where a chat thread lives on the web. The iOS app claims it
// as a Universal Link (applinks:lociai.fyi), so the same URL opens the
// thread in the app or, without it, in a browser.
const WebOrigin = "https://lociai.fyi"

// ProactiveCategory is the notification category a proactive chat message
// carries; the app registers its actions under this name.
const ProactiveCategory = "loci_chat"

// maxProactiveBodyRunes keeps the banner to a line or two: the message
// itself is in the thread the notification opens.
const maxProactiveBodyRunes = 180

// ProactiveMessage is a message the agent posted into a thread on its own
// (a standing task's update), to be announced on the owner's phones.
type ProactiveMessage struct {
	UserID      uuid.UUID
	SessionID   uuid.UUID
	MessageID   uuid.UUID
	Title       string // the standing task's title
	Content     string // the full message; the banner shows its first line
	SourceLabel string // "Standing task"
	CityName    string // the thread's city, for the deep link; may be empty
}

// BuildProactivePayload shapes a proactive message for the APNs sender.
func BuildProactivePayload(m ProactiveMessage) Payload {
	path, routeType, _ := runs.ResultPath("", m.SessionID, m.CityName, uuid.Nil)
	return Payload{
		SessionID:   m.SessionID.String(),
		CityName:    m.CityName,
		Domain:      routeType,
		Title:       strings.TrimSpace(m.Title),
		Body:        FirstLine(m.Content, maxProactiveBodyRunes),
		URL:         WebOrigin + path,
		MessageID:   m.MessageID.String(),
		Origin:      "proactive",
		SourceLabel: m.SourceLabel,
		Category:    ProactiveCategory,
		ThreadID:    "session-" + m.SessionID.String(),
	}
}

// FirstLine is the first non-blank line of text with leading Markdown
// heading and list markers removed, cut to at most maxRunes runes (an
// ellipsis included) on a rune boundary.
func FirstLine(text string, maxRunes int) string {
	var line string
	for _, l := range strings.Split(text, "\n") {
		l = strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(l), "#*->• "))
		if l != "" {
			line = l
			break
		}
	}
	if maxRunes <= 0 || utf8.RuneCountInString(line) <= maxRunes {
		return line
	}
	runes := []rune(line)
	return strings.TrimSpace(string(runes[:maxRunes-1])) + "…"
}

// NotifyProactive announces m on every iPhone its owner registered. Like
// OnRunFinished it returns at once and never fails the caller: the message
// is already in the thread, and the push is only a pointer to it. Web push
// is not sent: a browser with the thread open already shows the message.
func (n *Notifier) NotifyProactive(_ context.Context, m ProactiveMessage) {
	sender, ok := n.senders[PlatformAPNS]
	if !ok || m.SessionID == uuid.Nil || m.UserID == uuid.Nil {
		return
	}
	n.wg.Add(1)
	go func() {
		defer n.wg.Done()
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		n.deliverProactive(ctx, sender, m)
	}()
}

func (n *Notifier) deliverProactive(ctx context.Context, sender Sender, m ProactiveMessage) {
	devices, err := n.devices.ForUser(ctx, m.UserID, PlatformAPNS)
	if err != nil {
		n.logger.Warn("list push devices", "session_id", m.SessionID, "platform", PlatformAPNS, "error", err)
		return
	}
	if len(devices) == 0 {
		return
	}
	body, err := json.Marshal(BuildProactivePayload(m))
	if err != nil {
		return
	}
	for _, d := range devices {
		n.sendOne(ctx, PlatformAPNS, sender, d, body, "session_id", m.SessionID)
	}
}
