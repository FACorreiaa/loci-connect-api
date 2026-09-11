# Setting up Telegram

Telegram is optional. Without `TELEGRAM_BOT_TOKEN` the bridge is not built, no
poller starts, no route is mounted, and the Telegram card in Settings says the
server has no bot configured. Everything else works exactly as before.

Setup is in two halves: **once per deployment** (someone creates the bot) and
**once per person** (each person links their own chat).

The code is in `internal/domain/messaging` (link codes, routing, commands),
`internal/domain/messaging/telegram` (the Bot API adapter, the poller and the
webhook) and `internal/domain/messaging/chatbridge` (what turns a message into
an itinerary — the same chat session the web app uses).

## Part 1 — create the bot (once, by whoever runs Loci)

### 1. Create it with @BotFather

Open Telegram and message [@BotFather](https://t.me/BotFather):

```
/newbot
```

It asks two questions:

- **Name** — what people see at the top of the chat. Anything: `Loci`.
- **Username** — must be unique across all of Telegram and must end in `bot`.
  For example `loci_travel_bot`.

It replies with a token that looks like this:

```
8154392017:AAH9k2LmQ7xVbN3pR8sTuW1yZ4cE6gI0jKm
```

**That token is a credential.** Anyone holding it controls the bot completely.
Treat it like a password: never commit it, never paste it into an issue. It
travels in the Bot API's URL path, which is why nothing in the adapter wraps an
error that could carry it into a log.

### 2. Turn off group access

A link connects one chat to one account. A group has one chat id shared by
everybody in it, so a linked group would let every member plan as the owner and
read what they asked. Turn it off at the source so the bot is never added to
one. Still in @BotFather:

```
/setjoingroups
```

Pick your bot, then **Disable**.

### 3. Seal the settings

Loci reads three variables:

| Variable | What it is |
|---|---|
| `TELEGRAM_BOT_TOKEN` | The token from step 1. Required for the bridge to exist. |
| `TELEGRAM_BOT_HANDLE` | The bot's `@username`. Only so Settings can tell people which bot to open; nothing authenticates against it. |
| `TELEGRAM_WEBHOOK_SECRET` | Selects the delivery mode; see step 4. |

In production these live in the sealed secret `secrets/loci/loci-env.yaml` in
the infra repository. Locally they go in `.env`:

```bash
TELEGRAM_BOT_TOKEN=8154392017:AAH9k2LmQ7xVbN3pR8sTuW1yZ4cE6gI0jKm
TELEGRAM_BOT_HANDLE=@loci_travel_bot
```

### 4. Choose how updates arrive

Two modes, and **which one runs follows from whether you set a secret** — there
is no third variable and no combination that leaves an endpoint unprotected.
`config.MessagingConfig.UsesWebhook` is the whole rule.

**Long polling — leave `TELEGRAM_WEBHOOK_SECRET` empty.**

Loci asks Telegram for updates over an outbound connection. Nothing is exposed,
no public URL is needed, and it works on `localhost` and on a tailnet. This is
the right choice for development.

```bash
TELEGRAM_WEBHOOK_SECRET=
```

Nothing else to do. Start the server and the poller starts with it:

```
INFO telegram bot receiving updates bot=loci_travel_bot
```

> Only one process may poll a given bot. Two pollers on one token each receive
> half the updates, and the second one to start stops itself with "another
> process is receiving this bot's updates". Do not run this mode on more than
> one replica.

**Webhook — set `TELEGRAM_WEBHOOK_SECRET`.**

Telegram POSTs to `{BASE_URL}/webhooks/telegram`. This needs a **public HTTPS
URL** — `https://api.lociai.fyi` in production — so it is a production choice;
it will not work against `localhost` without a tunnel.

Generate a secret and register the webhook once:

```bash
# 1. Generate.
openssl rand -hex 32

# 2. Seal it as TELEGRAM_WEBHOOK_SECRET, then deploy. The server logs
#    "telegram in webhook mode; not polling" and mounts the route.

# 3. Tell Telegram where to send updates. Only messages are requested:
#    edits, reactions and channel posts have no handling and would be dropped.
curl -X POST "https://api.telegram.org/bot$TELEGRAM_BOT_TOKEN/setWebhook" \
  -d url="$BASE_URL/webhooks/telegram" \
  -d secret_token="$TELEGRAM_WEBHOOK_SECRET" \
  -d allowed_updates='["message"]'
```

Telegram echoes the secret back in the `X-Telegram-Bot-Api-Secret-Token`
header on every delivery, and it is the only thing separating a real delivery
from anyone who guesses the path. Loci compares it in constant time and answers
`401` with an empty body to anything else, which is why the secret is required
rather than optional in this mode.

The endpoint acknowledges a delivery with `200` before it answers it. Telegram
retries anything that is not a prompt `200`, and an itinerary takes minutes, so
holding the request open would guarantee both a timeout and a redelivery. If
Telegram redelivers anyway the message is answered twice; that is the accepted
cost.

Check what Telegram thinks:

```bash
curl "https://api.telegram.org/bot$TELEGRAM_BOT_TOKEN/getWebhookInfo"
```

`pending_update_count` climbing or a non-empty `last_error_message` means
deliveries are failing. `Wrong response from the webhook: 401 Unauthorized` is
the secret Telegram holds not matching the one Loci holds — usually a stale
`setWebhook` after a reseal, or whitespace around the sealed value.

Prove the endpoint refuses strangers:

```bash
curl -i -X POST "$BASE_URL/webhooks/telegram" -d '{}'   # expect 401, empty body
```

To switch back to polling, delete the webhook and clear the secret. Both are
needed: Telegram refuses `getUpdates` while a webhook is registered, and Loci
does not poll while a secret is set.

```bash
curl -X POST "https://api.telegram.org/bot$TELEGRAM_BOT_TOKEN/deleteWebhook"
```

### 5. Check it before you trust it

```bash
go run ./cmd/telegram-check
```

Read-only, so it is safe to run against production. It confirms the token
authenticates, prints which bot it belongs to, and — the part worth having —
compares how Loci is configured to receive updates against how Telegram is
actually set up:

| Symptom | What the check says |
|---|---|
| Nothing ever arrives in webhook mode | Telegram has no webhook registered |
| Every poll fails in polling mode | A webhook is still registered; Telegram refuses `getUpdates` while one exists |
| Wrong bot opens from Settings | `TELEGRAM_BOT_HANDLE` names a different bot than the token |

Both failure modes are silent otherwise: the tests pass either way, because
they run against a stand-in for the Bot API rather than the real one.

It reads `TELEGRAM_*` from the environment (and `.env`) directly rather than
through the full config loader, so it does not need a database, provider keys
or JWT secrets to answer. `--send-to <chat id>` sends a test message and is the
one flag that writes.

## Part 2 — link your account (once per person)

Each person does this for themselves. There is nothing to configure.

1. Open Loci in a browser and sign in.
2. Go to **Settings → Connections**.
3. Find the **Telegram** card and press **Get a link code**.
4. A short code appears — something like `K7PQ2MNX`. It is shown **once** and is
   valid for **15 minutes**. Only its hash is stored, and it works once.
5. Open the bot in Telegram (the card links to it) and send it that code. Pasting
   it after `/start`, with a hyphen, or in lower case all work.
6. It replies confirming the link.

From then on, ask it for an itinerary in plain language — "three days in Lisbon,
we like food and old buildings". What you ask there and what you ask in the app
are the same conversation: a follow-up from your phone continues the trip you
were planning in the browser.

Commands the bot answers without spending a generation: `/start`, `/help`,
`/unlink`. Disconnect from Settings works too.

## What a linked message runs as

A message from a linked chat acts as the account it is linked to. That is what
makes the provider routing apply: if the account brought its own API key, the
answer runs on it; if it is on the free tier, the free chain answers. Both the
account id and its email address travel with the request, because parts of it
read one and parts read the other.

Daily quota **is** consumed over Telegram. The subscription interceptor runs in
the Connect chain, which a chat message never enters, so the bridge meters the
message itself: asking for an itinerary here costs the same request that asking
in the app does. Commands — `/start`, `/help`, `/unlink` — do not, and neither
does anything sent by a chat that is not linked to an account yet.

An account that has used up the day's requests is told so, and told when they
reset, rather than being answered.

## Recordings

Send the bot a voice note and it answers the same way it answers a typed
question: same session, same account, same itinerary. Round video messages work
too, and can be turned off on their own. Replies come back as text — the bot
does not speak.

Transcription runs on **the cluster's own speech-to-text service**, not a paid
API. It costs nothing per request, needs no key, and no audio leaves the
cluster. See `platform/infra/docs/transcription.md`.

    TRANSCRIBE_PROVIDER_OPENAI_BASEURL=http://whisper.horus.svc.cluster.local:8000/v1
    TRANSCRIBE_PROVIDER_OPENAI_MODEL=Systran/faster-whisper-small

**Use the multilingual model, not `small.en`.** The English-only one does not
merely mangle Portuguese, it hallucinates fluent English over it: asked to
transcribe *"quero passar três dias em Lisboa, no bairro de Alfama"* it answered
*"I hope you enjoyed this video, and don't forget to like, comment and
subscribe!"* — which would then be echoed back and planned against.

**The app must be named in `allow-whisper`** in the infra repo's
`cluster/network-policies/horus.yaml`. `horus` is default-deny ingress, and
without that entry every request fails in a way that reads exactly like the
service being down.

What happens to a recording, in order:

1. Its length and size are checked against the update itself. Anything past the
   cap is refused without being downloaded.
2. The chat is resolved to an account and the account's quota is spent. **An
   unlinked chat is never transcribed.**
3. The account's place names are looked up and sent as a vocabulary hint. This
   is not decoration — without it *"take me to Cais do Sodré, then Bairro Alto
   and Belém"* comes back as *"Case 2 Soda, then Baro Alto and Bellum"*.
4. The recording is transcribed and the transcript is echoed back before the
   answer is worked out.
5. The answer is sent.

**Expect seconds, not milliseconds.** The service is CPU-bound on a single
replica shared with other apps — measured at roughly 2.5× the length of the
clip. A 45-second cap means up to about two minutes of transcription before
generation even starts, which is why the typing indicator runs throughout.

A recording cannot carry a link code — a transcript of "A3F9C1D2" read aloud is
"a three F nine see one D two" — and cannot be a command, because nobody says
"slash help". A recording is metered whatever it turns out to say: the
transcript is what would tell us it said "help", and taking it is the expense.

| Symptom | Cause |
|---|---|
| Bot says voice is switched off | `TRANSCRIBE_PROVIDER_OPENAI_BASEURL` empty, or the app is not in `allow-whisper`. |
| "I am behind on voice notes" | The service is queuing — it is one CPU-bound replica shared with other apps. |
| Fluent English from foreign speech | The model is `small.en`. Use the multilingual one. |
| Place names mangled | The vocabulary hint is empty, which it is until the account has a session with a city on it. |

