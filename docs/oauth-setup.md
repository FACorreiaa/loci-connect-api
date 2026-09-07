# Google and Apple sign-in: setup

The code is already written — `internal/domain/custom_auth/service/oauth_service.go`
via `markbates/goth`, wired at `cmd/api/dependencies.go`. What follows is
everything needed to turn it on. Both buttons already appear on `/auth/signin`;
until this is done they answer "Google sign-in isn't available right now."

**Read this first, before the provider consoles.** Two things about this flow
are not what they look like:

1. **The redirect URI is chosen by the server, not the browser.** goth builds
   each provider with a fixed `OAUTH_CALLBACK_URL + "/<provider>/callback"`. The
   `redirect_uri` the client sends in `GetOAuthURL` is passed through as the
   OAuth `state` and does not affect where the provider redirects. So the URI
   registered in each console must match `OAUTH_CALLBACK_URL` exactly.
2. **`OAUTH_CALLBACK_URL` points at the *client*, not the API.** The provider
   redirects a popup window, and that window hands the authorization code to
   the page that opened it with `postMessage(..., window.location.origin)`. It
   can only do that from the app's own origin. The route that receives it is
   `loci-client/src/routes/auth/oauth/[provider]/callback.tsx`.

So, for production:

```
OAUTH_CALLBACK_URL=https://lociai.fyi/auth/oauth
```

which makes the two registered redirect URIs:

```
https://lociai.fyi/auth/oauth/google/callback
https://lociai.fyi/auth/oauth/apple/callback
```

and locally, with the client on Vinxi's default port:

```
OAUTH_CALLBACK_URL=http://localhost:3000/auth/oauth
```

---

## 1. Google

Google's own credential is straightforward; the consent screen is the part that
blocks people.

### 1.1 Create the project and consent screen

1. <https://console.cloud.google.com/> → create a project, or pick an existing
   one. The project name is internal and never shown to users.
2. **APIs & Services → OAuth consent screen.**
   - User type **External**. (Internal is only for a Google Workspace domain,
     and restricts sign-in to that domain.)
   - App name `Loci`, support email your own.
   - App logo: upload `loci-client/public/images/brand/icon-512.png`. Google
     wants a square PNG and rejects one with an alpha channel; that file is
     opaque RGB for exactly this reason.
   - Application home page `https://lociai.fyi`, privacy policy and terms
     links — Google requires both before it will let you publish.
   - Authorized domain `lociai.fyi`.
3. **Scopes.** Add only `.../auth/userinfo.email` and
   `.../auth/userinfo.profile`. These are what the server asks for
   (`google.New(..., "email", "profile")`) and they are *non-sensitive*, so
   they need no verification review. Adding anything else puts the app into
   Google's review queue and it will sit there.
4. **Test users**, while the app is in Testing: add every address you intend to
   sign in with. An address that is not listed gets "access blocked" and no
   explanation. This is the single most common reason a correctly configured
   Google sign-in appears broken.
5. **Publishing status.** Testing is capped at 100 users and its refresh tokens
   expire after 7 days. With only non-sensitive scopes, **Publish app** needs no
   review — do it before letting strangers in.

### 1.2 Create the OAuth client

**APIs & Services → Credentials → Create credentials → OAuth client ID.**

- Application type **Web application**
- Name: anything, internal only
- **Authorized JavaScript origins**: `https://lociai.fyi` (and
  `http://localhost:3000` for local work)
- **Authorized redirect URIs**: `https://lociai.fyi/auth/oauth/google/callback`

Copy the client ID and client secret:

| Console value | Environment variable |
|---|---|
| Client ID (`…apps.googleusercontent.com`) | `GOOGLE_CLIENT_ID` |
| Client secret (`GOCSPX-…`) | `GOOGLE_CLIENT_SECRET` |

> Redirect URIs are matched by Google **exactly** — scheme, host, port, path,
> and trailing slash. `https://lociai.fyi/auth/oauth/google/callback/` is a
> different URI and fails with `redirect_uri_mismatch`.

---

## 2. Apple

Apple needs four things where Google needs two, and one of them is a private
key the server signs with rather than a secret you paste.

### 2.1 The pieces

1. **Team ID** — <https://developer.apple.com/account> → Membership. Ten
   characters, e.g. `A1B2C3D4E5`. → `APPLE_TEAM_ID`
2. **App ID** — Certificates, Identifiers & Profiles → Identifiers → **+** →
   **App IDs** → App. Give it a bundle ID (`fyi.lociai.app`) and enable the
   **Sign In with Apple** capability. This is the iOS app's identity; the web
   flow needs it to exist so the Services ID can be grouped under it.
3. **Services ID** — Identifiers → **+** → **Services IDs**. Description
   `Loci Web`, identifier `fyi.lociai.web`. **This identifier — not the App
   ID — is `APPLE_CLIENT_ID`.** Getting these two the wrong way round produces
   `invalid_client`, which says nothing about which one is wrong.

   Then **Configure** the Services ID:
   - Primary App ID: the App ID from step 2
   - **Domains and Subdomains**: `lociai.fyi`
   - **Return URLs**: `https://lociai.fyi/auth/oauth/apple/callback`

   Apple rejects `http://` and rejects `localhost` here. See
   [Local development](#4-local-development) for what to do instead.

4. **Domain verification.** Apple's Configure dialog offers a
   `apple-developer-domain-association.txt` download and then verifies it at
   `https://lociai.fyi/.well-known/apple-developer-domain-association.txt`.
   Put the file at
   `loci-client/public/.well-known/apple-developer-domain-association.txt`
   and deploy the client before pressing Verify — the client serves
   `public/` at the root, so no route is needed.

5. **Sign In with Apple key** — Keys → **+** → enable **Sign In with Apple**,
   Configure → primary App ID → Save → **Register** → **Download**.
   - The 10-character **Key ID** → `APPLE_KEY_ID`
   - The downloaded `AuthKey_XXXXXXXXXX.p8` → its **contents** are
     `APPLE_PRIVATE_KEY`

   **The .p8 can be downloaded exactly once.** Apple will not give it to you
   again; losing it means revoking the key and creating another.

### 2.2 APPLE_PRIVATE_KEY is the file, not a JWT

Apple's OAuth "client secret" is a short-lived ES256 JWT signed with that .p8.
The server signs it — `appleMakeSecret` in `oauth_service.go` — and re-signs it
every 24 hours, because **Apple refuses any client secret whose lifetime exceeds
six months**. A hand-minted JWT pasted into an env var therefore stops working
on a date nobody has written down, and Apple's rejection says nothing about
expiry. There is no `APPLE_SECRET` variable any more; if you find one in an old
`.env`, delete it.

Set the whole PEM file, headers included:

```
APPLE_PRIVATE_KEY="-----BEGIN PRIVATE KEY-----
MIGTAgEAMBMGByqGSM49AgEGCCqGSM49AwEHBHkwdwIBAQQg...
-----END PRIVATE KEY-----"
```

A path to the file will not work, and neither will the base64 body on its own.
Both fail at boot with a message naming `APPLE_PRIVATE_KEY`.

**All four Apple variables or none.** With none set, Apple sign-in is simply
off — that is how you run locally without an Apple developer account. With
*some* set, the server refuses to start and names the missing ones: a
half-configured provider means somebody meant it to work, and silently skipping
it leaves a button on the sign-in page that fails for every visitor with nothing
in the logs.

### 2.3 Two more things Apple does differently

- **The email may be a relay.** People can choose "Hide My Email", and you get
  a `@privaterelay.appleid.com` address that forwards. It is stable per user per
  app, so it works as an identity, but mail to it only delivers if you register
  the sending domain with Apple.
- **The name arrives once.** Apple includes the user's name only on the *first*
  authorization, in the form POST — never again. `LoginOrRegisterOAuth` has to
  persist it on first sight; there is no second chance and no endpoint to ask.

---

## 3. Environment variables

| Variable | Where it goes | Secret? |
|---|---|---|
| `OAUTH_CALLBACK_URL` | `apps/loci/data/config.yaml` | no |
| `GOOGLE_CLIENT_ID` | `loci-env` SealedSecret | not really, but kept with its secret |
| `GOOGLE_CLIENT_SECRET` | `loci-env` SealedSecret | **yes** |
| `APPLE_CLIENT_ID` | `loci-env` SealedSecret | no |
| `APPLE_TEAM_ID` | `loci-env` SealedSecret | no |
| `APPLE_KEY_ID` | `loci-env` SealedSecret | no |
| `APPLE_PRIVATE_KEY` | `loci-env` SealedSecret | **yes — a signing key** |
| `SESSION_SECRET` | `loci-env` SealedSecret | **yes** |

`SESSION_SECRET` keys the gothic cookie store. Generate it, do not choose it:

```sh
openssl rand -base64 32
```

### Sealing them into the cluster

Following `platform/infra/secrets/README.md`. `loci-env` already exists, so
this reseals the whole set — a SealedSecret cannot be added to a key at a time.

```sh
cd ~/Work/production/platform/infra

# 1. Reconstruct the full plaintext env file for the secret. Every key
#    currently in secrets/loci/loci-env.yaml has to be present, or resealing
#    drops it: DB_USER, DB_PASSWORD, JWT_SECRET, JWT_REFRESH_SECRET,
#    OPENROUTER_*, ENCRYPTION_KEY, MFA_SECRET_KEY, TELEGRAM_BOT_TOKEN, STRIPE_*
$EDITOR /tmp/loci-env            # NOT in the repo, and delete it after

# 2. Seal
kubectl create secret generic loci-env -n horus \
  --from-env-file=/tmp/loci-env --dry-run=client -o yaml \
  | kubeseal --controller-name sealed-secrets-controller \
             --controller-namespace kube-system -o yaml \
  > secrets/loci/loci-env.yaml

# 3. Check no plaintext escaped into the committed file
grep -c encryptedData secrets/loci/loci-env.yaml
grep -i "BEGIN PRIVATE KEY" secrets/loci/loci-env.yaml && echo "STOP: plaintext key" || echo "clean"

shred -u /tmp/loci-env 2>/dev/null || rm -f /tmp/loci-env
```

Commit `secrets/loci/loci-env.yaml`, ArgoCD applies it, and the controller
materializes the real `Secret`. Multi-line values survive `--from-env-file`
only if quoted, so keep the `APPLE_PRIVATE_KEY="..."` quotes in the plaintext
file.

Then restart the API so it re-reads the secret — env vars are read at boot:

```sh
kubectl -n horus rollout restart deploy/loci-api
kubectl -n horus logs deploy/loci-api | grep "oauth provider registered"
```

Two lines, one per provider, means both are on.

---

## 4. Local development

Google works locally. Add `http://localhost:3000` as a JavaScript origin and
`http://localhost:3000/auth/oauth/google/callback` as a redirect URI on the same
OAuth client, then set `OAUTH_CALLBACK_URL=http://localhost:3000/auth/oauth`.

**Apple does not work on localhost.** It rejects `http://` and rejects
`localhost` in Return URLs, so there is no local Apple flow without a public
HTTPS hostname. Either leave all four `APPLE_*` unset locally — Apple sign-in is
then off and the rest of auth is unaffected — or point a tunnel (`cloudflared`,
`ngrok`) at the client and register that hostname as a second Services ID.

---

## 5. Verifying it worked

```sh
go build ./... && go vet ./... && go test ./... && golangci-lint run
```

Then end to end, against the deployed pair:

1. `https://lociai.fyi/auth/signin` → **Google**. A popup opens on
   `accounts.google.com`, consent, popup closes, you land signed in.
2. Confirm the identity was recorded (migration `0037`):

   ```sql
   SELECT provider_name, provider_user_id, created_at
     FROM user_oauth_identities ORDER BY created_at DESC LIMIT 5;
   ```

3. Repeat for **Apple**. Then sign out and in again — the second time exercises
   the "existing identity" path rather than the create path, and for Apple it is
   the pass where no name is sent.

If a button still says the provider is unavailable, the server registered no
provider for it: check the boot logs for `oauth provider registered`, and that
the pod actually has the variables (`kubectl -n horus exec deploy/loci-api --
printenv | grep -c APPLE_`).

### Failures and what they actually mean

| Symptom | Cause |
|---|---|
| `redirect_uri_mismatch` (Google) | Registered URI differs from `OAUTH_CALLBACK_URL + "/google/callback"` — often a trailing slash, or `http` vs `https` |
| "Access blocked: Loci has not completed verification" | App is in Testing and the address is not a test user |
| `invalid_client` (Apple) | `APPLE_CLIENT_ID` holds the App ID instead of the Services ID |
| `invalid_grant` (Apple) | The signed client secret is wrong or expired — check `APPLE_TEAM_ID` and `APPLE_KEY_ID` match the .p8 |
| Server refuses to boot, "apple sign-in is partly configured" | Some but not all four `APPLE_*` are set; the message names which |
| Popup opens, then hangs on "Processing authentication…" | The provider redirected somewhere `/auth/oauth/<provider>/callback` is not served — usually `OAUTH_CALLBACK_URL` pointing at the API domain instead of the client |
| "Google sign-in isn't available right now" | No provider registered: the variables are absent from the running pod |

---

## Known gap: the OAuth `state` is not verified

Worth knowing before this carries real traffic. `GetAuthURL` passes the
client's `redirect_uri` to `goth`'s `BeginAuth` as the `state`, returns the
marshalled session to the browser, and `CompleteAuth` unmarshals whatever the
browser sends back — nothing binds the callback to the request that started it,
and `session.Authorize` does not check `state` either. A cross-site request
forgery on the callback is therefore not prevented by this flow.

Closing it properly means a server-side, single-use, per-request state bound to
the browser session, which changes the RPC contract. Deliberately out of scope
for turning the providers on; it should not stay open.
