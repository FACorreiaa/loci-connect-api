# Push: finished searches to browsers and iPhones

A run that reaches COMPLETE or ERROR is announced once (`runs.ClaimNotification`)
to every device the account registered, if `notification_settings.search_finished`
is on. `internal/domain/push` holds the notifier and one sender per platform.

| Platform | Registration (`UserService.RegisterPushDevice`) | Sender | Keys |
|---|---|---|---|
| `web_push` | endpoint URL on an allow-listed push service, `p256dh`, `auth` | `WebPushSender` (VAPID, `webpush-go`) | `VAPID_PUBLIC_KEY`, `VAPID_PRIVATE_KEY`, `VAPID_SUBJECT` |
| `apns` | 64-hex device token, `apns_topic` (one of our bundle ids), `apns_environment` (`production` or `sandbox`) | `APNSSender` (HTTP/2 + ES256 provider JWT, standard library only) | `APNS_KEY_ID`, `APNS_TEAM_ID`, `APNS_KEY_P8` (base64 of the `.p8`, or raw PEM) or `APNS_KEY_PATH` (a file holding it), `APNS_TOPIC_PROD`, `APNS_TOPIC_BETA` |

Each platform is on only when all its keys are set; the startup log says which
("apns enabled" / "apns disabled: …"). With both off, runs, the cap and in-app
toasts still work.

## APNs specifics

- **Environment.** App Store and TestFlight builds hold production tokens;
  a build signed with a development profile holds a sandbox token. Apple drops
  a token sent to the wrong host without an error, so the app says which it
  has when it registers, and the sender picks `api.push.apple.com` or
  `api.sandbox.push.apple.com` per device.
- **Topic.** The bundle id. Registration refuses anything that is not
  `APNS_TOPIC_PROD` or `APNS_TOPIC_BETA`, so a client cannot aim the sender at
  another app.
- **Payload.** The alert under `aps`; `sessionId`, `cityName`, `domain`,
  `status`, `url` at the top level, the same keys the web payload carries and
  the ones the app routes on (`SessionLink(userInfo:)`).
- **Gone tokens.** `BadDeviceToken`, `Unregistered`, `DeviceTokenNotForTopic`
  or HTTP 410 remove the row, like a web endpoint answering 410.
- **Provider token.** One ES256 JWT per 50 minutes, minted with the team's
  push key; `ExpiredProviderToken` mints a fresh one and retries once.

## Standing tasks (proactive messages)

When a standing task (`internal/domain/watch`) posts its "Standing task"
message into a thread, `watch.PushNotifier` hands it to
`Notifier.NotifyProactive`, which pushes to the owner's **APNs** devices only
(a browser with the thread open already shows the message). No notification
setting gates it: the user asked for the task. Gone tokens are pruned the same
way. With APNs unconfigured it does nothing and the message is only in the
thread.

```json
{
  "aps": {
    "alert": {"title": "<watch title>", "body": "<first line of the message, <=180 chars>"},
    "sound": "default",
    "thread-id": "session-<sessionId>",
    "category": "loci_chat"
  },
  "sessionId": "<sessionId>",
  "cityName": "<thread city, may be empty>",
  "domain": "itinerary",
  "url": "https://lociai.fyi/itinerary?sessionId=<sessionId>&cityName=<city>&domain=itinerary",
  "messageId": "<messageId>",
  "origin": "proactive",
  "sourceLabel": "Standing task"
}
```

`sessionId` / `cityName` / `domain` are what `SessionLink(userInfo:)` already
routes on; `url` is a Universal Link (`applinks:lociai.fyi`).

Metrics: `loci_push_sent_total{platform, result}`.
