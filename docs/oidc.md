# Native OIDC sign-in

Optional OpenID Connect login, off unless `oidc.enabled` is true. **Local signup,
password sign-in and Reader API keys are unaffected either way** — enabling OIDC
adds a second way in; it does not replace the first.

Implementation: `internal/config/oidc.go` (settings + validation),
`internal/oidc/` (account resolution), `internal/httpapi/oidc.go` (the HTTP flow),
`web/src/views/LoginView.vue` (the button and error copy).

## Flow

- **Authorization code with PKCE (S256), exchanged server-side** by the
  digesterr binary. `github.com/coreos/go-oidc/v3` performs discovery, JWKS
  caching and ID token verification (signature, `iss`, `aud`, `exp`);
  `golang.org/x/oauth2` performs the token exchange. Both are maintained
  libraries; do not hand-roll JWT or discovery code here.
- **Per-request secrets.** `state`, `nonce` and the PKCE verifier are generated
  per attempt. `state` is stored **hashed** (`oidc_auth_requests.state_hash`),
  and the callback **deletes the row it consumes**. A replayed state therefore
  finds nothing; the UNIQUE index means even a delete race cannot leave two.
- **Browser binding.** A second random value goes into an HttpOnly,
  `SameSite=Lax` cookie scoped to `/api/auth/oidc`. The callback requires that
  cookie and compares its hash in constant time against the stored
  `binding_hash`. A state value leaked into another browser cannot be redeemed
  there.
- **Fixed redirect URI.** `oidc.redirect_url` is used verbatim and validated to
  end in `/api/auth/oidc/callback`. It is never derived from the `Host` header,
  so a forged Host cannot redirect the code elsewhere.
- **Session reuse.** A successful callback issues the *same* HttpOnly
  `refresh_token` cookie local sign-in issues. The SPA's router guard silently
  refreshes, and signout, rotation and `requireAuth` behave identically. There
  is no parallel session mechanism.

## Endpoints

Registered only when the feature is enabled: with `oidc.enabled: false` these
paths return 404, rather than advertising an authentication mechanism that is
not configured.

| Route | Auth | Behaviour |
| --- | --- | --- |
| `GET /api/auth/oidc/status` | none | `{enabled, provider_name}`. Lets the login page decide whether to draw the button. Exposes no issuer, client id or endpoint. |
| `GET /api/auth/oidc/start?return_to=/path` | none | Sets the binding cookie, 302s to the provider with `state`, `nonce` and the S256 challenge. |
| `GET /api/auth/oidc/callback` | none | Consumes the authorization request, exchanges the code, verifies the ID token, resolves the account, sets the session cookie, then 303s to `return_to`. |

`return_to` must be a same-origin absolute path; anything with a scheme, a
protocol-relative `//host`, a backslash, or control characters falls back to `/`.
An absolute URL here would turn the callback into an open redirect.

### Error codes

The callback redirects to `/login?error=<code>` with one of these stable values.
Provider-supplied text (`error_description`, token endpoint bodies) is logged
locally but **never** reflected into the redirect or the page.

| Code | Meaning |
| --- | --- |
| `oidc_state_invalid` | Missing/unknown/expired/replayed state, or no matching browser-binding cookie. |
| `oidc_denied` | The provider returned an error (e.g. the user cancelled). |
| `oidc_exchange_failed` | The code exchange failed (PKCE mismatch, redirect mismatch, provider outage). |
| `oidc_invalid_token` | ID token missing, unverifiable, wrong `iss`/`aud`, expired, nonce mismatch, no `sub`, or malformed claims. |
| `oidc_email_missing` | The token carried no email. |
| `oidc_email_unverified` | `email_verified` was not the boolean `true`. |
| `oidc_email_ambiguous` | The verified email matches more than one local account. |
| `oidc_error` | Anything else (database failure and similar). |

## Account resolution, and the pre-hijack risk

`internal/oidc.Service.Resolve` applies, in this order:

1. **`(issuer, subject)` already mapped** → that user. Always wins. Email is
   mutable at the provider, so an address change never re-keys a login and
   never moves a user onto the account that now owns the old address.
2. **Verified email matches exactly one local account** → link it. Comparison
   is case-insensitive and Unicode-folded **in Go**: SQLite's `LOWER()` folds
   ASCII only, so a query-side match would disagree with the application about
   which accounts are candidates. The existing password hash is left intact, so
   the owner can still sign in locally after linking.
3. **Otherwise** → provision a user with an **empty password hash** (which no
   bcrypt comparison can match, so the account is not password-authenticatable
   until its owner sets one) and a generated Reader API key.

`email_verified` must be the boolean `true` on **every** login, not only the
first. A string `"true"` is rejected rather than coerced: a provider that can
send `"true"` can send `"false"`, and coercing would read it as verified.

### The risk this design accepts

Step 2 is what the deployment asks for: automatic match by verified email. It
means **whoever controls the identity provider decides which local account a
verified address maps to**. Combined with open local signup, that is a classic
account pre-hijacking path:

1. An attacker registers `victim@example.com` locally, choosing a password.
2. The victim never uses the app, so nothing looks wrong.
3. The victim later signs in through the IdP. The email is verified there, step
   2 links the attacker's pre-created account to the victim's identity, and the
   attacker retains the password they set.

Step 2 is also the reason the password hash is *kept* on link (so a legitimate
owner is not locked out), which is exactly what makes the pre-created account
usable by the attacker.

Mitigations, strongest first:

- **Do not expose open signup** on an instance whose accounts are meant to come
  from the IdP. There is no config switch for this yet; restrict it at the
  reverse proxy / network boundary.
- Keep provider registration closed, and treat proof of address ownership there
  as the only proof of identity.
- Keep the instance private until every intended user has linked.
- Accept that an ambiguous match (two local accounts differing only by case)
  **fails closed** with `oidc_email_ambiguous`. That is deliberate: choosing one
  would be a guess, and a guess here is account takeover, so the operator
  resolves it.

An operator who cannot accept this should leave `oidc.enabled` false. Password
auth and Reader keys remain fully functional.

## Offboarding

- **Provider-side removal does not end an existing session.** Deleting or
  disabling the user at the IdP stops *new* logins. An existing app session is
  an independent credential: the access token lives 15 minutes, and the refresh
  token lives 7 days and rotates without contacting the provider. To end access
  immediately, sign the session out (`POST /api/auth/signout` revokes the
  refresh token) or change/clear the account. Do not assume the IdP did it.
- **Unlinking one identity** is not exposed in the UI.
  `user_identities` is keyed on `(issuer, subject)`; removing the row stops that
  provider resolving to the account, while the account's password (if any) and
  Reader API key still work. Full unlink:
  `DELETE FROM user_identities WHERE user_id = ?`. Deleting the user cascades.
- **A provisioned (OIDC-only) account** has no password; its owner sets one only
  after signing in. Its Reader API key is visible in Settings and can be rotated
  there, which is the revocation path for machine clients.
- **Disabling the feature** (`oidc.enabled: false` and restart) removes the
  routes. Existing sessions and identities keep working; only new OIDC logins
  stop. Identities can still be used again if it is re-enabled.

## Configuration

See the `oidc:` section of `digesterr.example.yaml` for the annotated form.
Environment variables: `OIDC_ENABLED`, `OIDC_ISSUER`, `OIDC_CLIENT_ID`,
`OIDC_CLIENT_SECRET`, `OIDC_REDIRECT_URL`, `OIDC_PROVIDER_NAME`, `OIDC_SCOPES`
(comma- or space-separated).

Register `${redirect_url}` as the callback at the provider. It must be the exact
string: the exchange sends it back and providers compare it literally.

Validation (fails startup, not the login button later):

- `issuer` and `redirect_url` must be absolute; `https` unless the host is
  loopback (so a local test provider works without a trusted certificate).
- `redirect_url` must carry no query or fragment and must end in
  `/api/auth/oidc/callback`.
- `client_id` and `client_secret` are required — this is a confidential client,
  because the exchange happens on the server.
- `scopes`, if set, must include `openid`.

Discovery runs at **startup**. An unreachable issuer, or a discovery document
whose `issuer` disagrees with `oidc.issuer`, aborts startup.

## Operational notes

- **Nothing in this flow logs a URL, query string, code, state, nonce or
  token.** The request logger records `r.URL.Path` only;
  `internal/httpapi/request_log_url_test.go` pins that, and it was verified to
  fail when the middleware is switched to `r.URL.RequestURI()`.
- `oidc_auth_requests` rows are swept on each `/start` (expired rows only), so
  abandoned logins do not accumulate. Two concurrent logins do not cancel each
  other.
- Set `server.cookie_secure: true` behind a TLS-terminating proxy, or the
  binding and refresh cookies are issued without `Secure`.
- The flow cookie is `SameSite=Lax` rather than `Strict` on purpose: the
  provider's redirect back is a top-level cross-site GET, and `Strict` would
  withhold the cookie and fail every login.

## Tests

- `internal/config/oidc_test.go` — validation rules, env overrides, example
  config stays disabled by default.
- `internal/migrate/oidc_schema_test.go` — fresh and upgraded databases both get
  the tables; `(issuer, subject)` and `state_hash` uniqueness are enforced by
  the database.
- `internal/oidc/resolve_test.go` — provisioning, linking, ambiguity,
  unverified email on both first and returning login, email change, issuer
  separation.
- `internal/httpapi/oidc_flow_test.go` + `oidc_provider_test.go` — a real
  `httptest` OpenID provider (discovery, JWKS, `/authorize`, `/token`, RS256
  signing). Covers happy path, session reuse, returning login, linking,
  unverified and ambiguous email, state replay, cross-browser callback, provider
  error, wrong nonce/issuer/audience/expiry/subject/email, string
  `email_verified`, forged signature, `return_to` open-redirect attempts, log
  leakage, and that local auth still works.
- `web/src/views/LoginView.oidc.test.ts` — button visibility, href, error copy,
  and that no token reaches the URL.
