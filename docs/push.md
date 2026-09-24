# Push: finished searches to browsers and iPhones

A run that reaches COMPLETE or ERROR is announced once (`runs.ClaimNotification`)
to every device the account registered, if `notification_settings.search_finished`
is on. `internal/domain/push` holds the notifier and one sender per platform.

| Platform | Registration (`UserService.RegisterPushDevice`) | Sender | Keys |
|---|---|---|---|
| `web_push` | endpoint URL on an allow-listed push service, `p256dh`, `auth` | `WebPushSender` (VAPID, `webpush-go`) | `VAPID_PUBLIC_KEY`, `VAPID_PRIVATE_KEY`, `VAPID_SUBJECT` |
| `apns` | 64-hex device token, `apns_topic` (one of our bundle ids), `apns_environment` (`production` or `sandbox`) | `APNSSender` (HTTP/2 + ES256 provider JWT, standard library only) | `APNS_KEY_ID`, `APNS_TEAM_ID`, `APNS_KEY_P8` (base64 of the `.p8`), `APNS_TOPIC_PROD`, `APNS_TOPIC_BETA` |

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

Metrics: `loci_push_sent_total{platform, result}`.
