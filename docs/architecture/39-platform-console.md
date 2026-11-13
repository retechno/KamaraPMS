# 39. Platform console: operator accounts with TOTP, tenant and plan management, the tenant's Subscription page and banner

Status: **APPROVED by the owner (2026-11-14).** The owner decided that the console lives under `/platform` in the same web app (not a second
app), that an operator signs in with **password + TOTP, mandatory**, and that this work also delivers the hotel-side Subscription page and the
GRACE / READ_ONLY banner (step 2 of `26-subscriptions.md` §9). The questions Q1 to Q6 (§15) were answered **as recommended** on the same day; the
text below already follows those answers. **Nothing is built; this document has no code and no migration.**

UI design (approved together with this document): the canvas "KamaraPMS Platform Console" (11 screens, P1 to P9 and T1, T2), linked in the PR
that adds this file. The `.dc.html` source of every screen is in `docs/design/` (files 75 to 85, listed in its README) so the copy, the layout and
the states can be read without the canvas; §11 describes every screen in words and is the reference when they disagree.

## 0. The idea in one page

Today a KamaraPMS operator (the company that sells KamaraPMS) manages plans, subscriptions and invoices only with `pms-admin` on the server
(`26-subscriptions.md` §8). This adds a web console for that work:

- A new kind of account, the **operator**, that is not a user of any tenant. It has its own table, its own password, a **mandatory TOTP
  second factor** with recovery codes, its own sessions and refresh cookie, its own JWT audience, and its own audit trail. A tenant token is
  never accepted by the console and an operator token is never accepted by the hotel API.
- Operator accounts are created, disabled and reset only with `pms-admin` (there is no "invite operator" screen).
- The console API lives under `/api/v1/platform/`, mounted on the outer mux so none of the tenant middleware (iam, subscription gate) runs on it.
  It calls the existing `subscription` and `tenancy` services; the subscription rules of `26-subscriptions.md` do not change.
- The console UI lives under `/platform` in `web/`, with its own layout (a dark top bar marked "Platform"), its own login and its own token store.
- On the hotel side: Setup → **Subscription** for tenant administrators, a **banner** in the shell for an ending trial, GRACE and READ_ONLY, and
  a better `PLAN_LIMIT_REACHED` message.

Three PRs (§14): **PR 1** the hotel side (small, uses only endpoints that exist), **PR 2** operator accounts, TOTP and the console API,
**PR 3** the console screens.

## 1. Findings in the code (read this first)

| # | Finding | Consequence |
|---|---|---|
| F1 | There is no MFA/TOTP code and no TOTP library in `go.mod` (`19-post-implementation-audit.md` F-17 records "no MFA"). | TOTP (RFC 6238, HMAC-SHA1, 30 s, 6 digits) is written with the standard library (`crypto/hmac`, `crypto/sha1`, `encoding/base32`), ~60 lines, tested with the RFC vectors. No new dependency. |
| F2 | `github.com/skip2/go-qrcode` is already used by `internal/documents/assetlabels.go`. | The enrolment QR is rendered on the server as a PNG data URI; the web app needs no QR library. Mark the module as a direct dependency in `go.mod` if `go mod tidy` asks. |
| F3 | `user_sessions.user_id` is a hard FK to `users`; `audit_logs.tenant_id` is NOT NULL and `audit_logs.user_id` means a user. | Operators get their own sessions table and their own audit table. Tenant audit entries written by an operator carry a new nullable `audit_logs.operator_id` instead of a user. |
| F4 | Operator actions today (`subscription.*`, `subscription_invoice.*`, `tenant.created`) write `user_id` NULL: the hotel's audit trail cannot tell an operator from the system. | From now on they set `operator_id` when an operator did it (console), and stay NULL for `pms-admin` and the system. The hotel's audit page shows "KamaraPMS (operator)", never the operator's name. |
| F5 | `iam`'s login limiter (`ratelimit.go`) is unexported. | Move it to `internal/platform/loginlimit` (same behaviour, exported) and use it from both `iam` and `operator`. A pure move: `iam` tests must pass unchanged. |
| F6 | `iam.tokens.parseAccess` rejects a token without `tid ≥ 1`, so an operator token is already refused by the hotel API; nothing checks an audience. | Operator tokens use issuer `kamarapms-platform` and audience `platform`; the hotel parser also checks `aud` is absent or `hotel` (defence in depth). Both directions are tested. |
| F7 | There is no function to suspend or reactivate a tenant and no query lists tenants. `Login`, `Refresh` and `Authenticate` already refuse a tenant whose status is not ACTIVE. | Add `tenancy.SetTenantStatus` (with reason and revocation of the tenant's sessions, reason `ACCOUNT_DISABLED`) and an operator list query (§7.3). |
| F8 | `authz_matrix_test.go` reads routes from `internal/*/*.go` only, and three tests read every route (`TestEveryRouteButThePublicOnesRequiresAToken`, `TestEveryRouteHasAModule`, `TestOpenAPIDescribesEveryRoute`). | The new package is `internal/operator` (one level, so its routes are found). `ModuleOf` skips `/api/v1/platform/` (not a tenant module). New lists `platformPublicRoutes` and `platformRoutes` in the matrix test (§12). |
| F9 | `web/src/router/index.ts` always runs the tenant `auth.init()` (a refresh) in `beforeEach`; `App.vue` picks the layout from `meta.public`. | A `meta.platform` branch runs first in the guard and never touches the tenant store; `App.vue` gets a third branch that renders `PlatformLayout`. |
| F10 | `web/src/api/session.ts` is a module singleton with hard-coded `/api/v1/auth/*` paths; `throwOnError` in `client.ts` is not exported. | New `api/platformSession.ts` and `api/platformClient.ts` (own memory token, own refresh URL); export `throwOnError` and reuse it. |
| F11 | `stores/subscription.ts` keeps only `modules`; nothing reads `X-Subscription-Access`. | The store keeps the whole summary; a client middleware reads the header and reloads the summary when the state changes (§10.2). |
| F12 | `views/pageStructure.test.ts` requires one `<PageHeader>` and no `<h1>` in every routed view except `LoginView.vue`. | `PlatformLoginView.vue` joins the exclusion list; every other console view uses `PageHeader`. |
| F13 | i18n tests: en/id parity, every error code has text, every audit action/entity has text and a filter entry, glossary terms (`Penyewa` for tenant, `Invoice`, `di-void`). | Every new code, action and key ships with en and id text in the same PR. |

## 2. Vocabulary

| Term | Meaning |
|---|---|
| Operator | A person of the company that runs KamaraPMS. Not a user of any tenant. Table `operator_accounts`. Shown in the UI as "operator". |
| Console | The operator's web UI under `/platform` and its API under `/api/v1/platform/`. ("Platform" is the word in URLs and on the top bar; "operator" is the word for the account.) |
| Challenge | The short-lived, single-use proof that the password was right, exchanged for a session after the TOTP step. |
| Recovery code | One of 10 single-use codes shown once at enrolment, accepted instead of a TOTP code. |
| Tenant, plan, subscription, invoice, access state | As in `26-subscriptions.md` §1. |

## 3. Decisions

| # | Decision |
|---|---|
| P1 | The console is part of the monolith and the same web build: `/platform` (UI) and `/api/v1/platform/` (API). A deployment that does not want it leaves `PMS_OPERATOR_TOTP_KEY` unset: every `/api/v1/platform/` route then answers 404 `ROUTE_NOT_FOUND` and the UI shows "The console is not enabled on this server." |
| P2 | Operators are global (no tenant). One role in v1: every operator can do everything in the console. (Q1) |
| P3 | Password (argon2id, same parameters and `ValidatePassword` as hotel users) **and** TOTP on every sign-in. There is no way to switch TOTP off. An operator without TOTP must enrol before the console opens. |
| P4 | TOTP: RFC 6238, SHA-1, 6 digits, 30 s step, accepted window −1/0/+1 step. The last accepted step is stored per operator and a code of the same or an earlier step is refused (no replay). |
| P5 | The TOTP secret (20 random bytes) is stored encrypted with AES-256-GCM under `PMS_OPERATOR_TOTP_KEY` (32 bytes, base64). Rotating the key is a `pms-admin` command that re-encrypts every secret (§9). |
| P6 | 10 recovery codes at enrolment, format `XXXX-XXXX-XX` from the alphabet `ABCDEFGHJKMNPQRSTVWXYZ23456789` (≈ 49 bits), stored as SHA-256, each single use. They are shown once. Using one is audited; when 3 or fewer are left the console shows a notice. `pms-admin regenerate-operator-recovery` makes a new set (the old set dies). |
| P7 | Sessions: `operator_sessions`, refresh token rotation and reuse detection exactly as `iam` (`Refresh`, 30 s rotation grace, `REUSE_DETECTED` revokes all). Access token 10 minutes; refresh token 12 hours **absolute** (a new sign-in with TOTP every working day). Every request re-reads the session and the account (`is_active`). |
| P8 | Cookie `pms_platform_refresh`, path `/api/v1/platform/auth`, HttpOnly, SameSite Strict, `Secure` from `PMS_COOKIE_SECURE`. The hotel cookie (`pms_refresh`, path `/api/v1/auth`) and this one never see each other. |
| P9 | JWT: HS256 with `PMS_JWT_SECRET` (the existing secret), issuer `kamarapms-platform`, audience `platform`, subject = operator id, claim `osid` = session id. No `tid`. |
| P10 | Rate limits (the moved `loginlimit`): 5 failed passwords per 15 min per email; 50 per 15 min per IP; 5 wrong TOTP or recovery codes per challenge (then the challenge dies and the operator starts again); 10 wrong codes per 15 min per operator. 429 `TOO_MANY_ATTEMPTS` with `retry_after`. |
| P11 | The console never opens hotel data (no folios, guests, reservations). It shows a tenant's subscription, usage counts, tenant administrators' names and e-mails and their last sign-in, and the tenant's subscription audit entries. There is no "log in as tenant". |
| P12 | Every console write is audited twice when it concerns a tenant: in `operator_audit_logs` (the operator's trail, with the operator's name) and in the tenant's `audit_logs` with `operator_id` set (the hotel sees "KamaraPMS (operator)"). Plan changes and sign-ins only go to `operator_audit_logs`. |
| P13 | Suspending a tenant is separate from the paid period (`26-subscriptions.md` §4): it needs a reason, revokes every session of the tenant's users (reason `ACCOUNT_DISABLED`) and blocks sign-in. Late payment is handled by GRACE → READ_ONLY, not by suspension. Reactivating needs a reason too. |
| P14 | The console uses the existing subscription service methods (`ListPlans`, `CreatePlan`, `UpdatePlan`, `Show`, `Change`, `IssueInvoice`, `PayInvoice`, `VoidInvoice`, `TenantInvoices`); their rules, error codes and locks are unchanged. They gain an optional actor (§7.4). |
| P15 | Hotel side: the Subscription page is admin-only (`adminOnly`), read only; the banner shows to every user, with copy for admins and for the others. The page shows a billing contact from the optional `PMS_BILLING_CONTACT` (free text, e.g. "billing@… · +62…"); without it the line is left out. |

## 4. Data model (migration 00100, PR 2)

PR 1 has no migration.

```sql
CREATE TABLE operator_accounts (
    id                bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    email             varchar(254) NOT NULL,
    full_name         varchar(200) NOT NULL,
    password_hash     text         NOT NULL,
    totp_secret_enc   bytea,                     -- AES-GCM(nonce || ciphertext); NULL until enrolled
    totp_enrolled_at  timestamptz,
    totp_last_step    bigint,                    -- the last accepted 30 s step (P4)
    is_active         boolean      NOT NULL DEFAULT true,
    last_login_at     timestamptz,
    created_at        timestamptz  NOT NULL DEFAULT now(),
    updated_at        timestamptz  NOT NULL DEFAULT now(),
    CONSTRAINT operator_accounts_totp_ck CHECK ((totp_secret_enc IS NULL) = (totp_enrolled_at IS NULL))   -- the secret being enrolled lives on the challenge
);
CREATE UNIQUE INDEX operator_accounts_email_uk ON operator_accounts (lower(email));
-- set_updated_at trigger as the other tables

CREATE TABLE operator_recovery_codes (
    id           bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    operator_id  bigint      NOT NULL REFERENCES operator_accounts (id) ON DELETE CASCADE,
    code_hash    bytea       NOT NULL,          -- sha256 of the normalised code (upper case, no dashes)
    used_at      timestamptz,
    created_at   timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT operator_recovery_codes_uk UNIQUE (operator_id, code_hash)
);

CREATE TABLE operator_login_challenges (
    id           bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    operator_id  bigint      NOT NULL REFERENCES operator_accounts (id) ON DELETE CASCADE,
    token_hash   bytea       NOT NULL,          -- sha256 of the random challenge token (32 bytes, base64url)
    purpose      varchar(10) NOT NULL,          -- VERIFY (enrolled) or ENROLL
    pending_secret_enc bytea,                   -- ENROLL: the secret shown, until confirmed
    attempts     smallint    NOT NULL DEFAULT 0,
    expires_at   timestamptz NOT NULL,          -- 5 minutes
    used_at      timestamptz,
    created_at   timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT operator_login_challenges_token_uk UNIQUE (token_hash),
    CONSTRAINT operator_login_challenges_purpose_ck CHECK (purpose IN ('VERIFY', 'ENROLL'))
);

CREATE TABLE operator_sessions (            -- the shape of user_sessions (00002 + 00012)
    id                  bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    operator_id         bigint       NOT NULL REFERENCES operator_accounts (id) ON DELETE CASCADE,
    refresh_token_hash  bytea        NOT NULL,
    expires_at          timestamptz  NOT NULL,
    revoked_at          timestamptz,
    revoked_reason      varchar(20),
    user_agent          varchar(300),
    ip_address          inet,
    created_at          timestamptz  NOT NULL DEFAULT now(),
    CONSTRAINT operator_sessions_token_uk UNIQUE (refresh_token_hash),
    CONSTRAINT operator_sessions_revoked_ck CHECK ((revoked_at IS NULL) = (revoked_reason IS NULL)),
    CONSTRAINT operator_sessions_revoked_reason_ck CHECK (revoked_reason IN ('ROTATED','LOGOUT','REUSE_DETECTED','PASSWORD_CHANGED','PASSWORD_RESET','DEACTIVATED','TOTP_RESET'))
);
CREATE INDEX operator_sessions_active_idx ON operator_sessions (operator_id) WHERE revoked_at IS NULL;

CREATE TABLE operator_audit_logs (          -- append-only, as audit_logs (forbid_modification, forbid_truncate)
    id            bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    operator_id   bigint,                     -- NULL: pms-admin or the system. No FK, as audit_logs.user_id
    action        varchar(80)  NOT NULL,
    entity_type   varchar(60)  NOT NULL,
    entity_id     bigint       NOT NULL,
    entity_label  varchar(120),
    tenant_id     bigint,                     -- set when the action concerns a tenant
    old_data      jsonb,
    new_data      jsonb,
    request_id    varchar(64),
    ip_address    inet,
    created_at    timestamptz  NOT NULL DEFAULT now()
);
CREATE INDEX operator_audit_logs_created_idx ON operator_audit_logs (created_at DESC);
CREATE INDEX operator_audit_logs_tenant_idx ON operator_audit_logs (tenant_id, created_at DESC) WHERE tenant_id IS NOT NULL;

ALTER TABLE audit_logs ADD COLUMN operator_id bigint;   -- no FK, as user_id
ALTER TABLE audit_logs ADD CONSTRAINT audit_logs_actor_ck CHECK (user_id IS NULL OR operator_id IS NULL);

ALTER TABLE tenants ADD COLUMN status_reason varchar(500), ADD COLUMN status_changed_at timestamptz;
ALTER TABLE tenants ADD CONSTRAINT tenants_suspended_reason_ck CHECK (status = 'ACTIVE' OR status_reason IS NOT NULL);
```

- Every new constraint and index name goes into `internal/platform/db/errors.go` (`operator_accounts_email_uk` → 409 `EMAIL_TAKEN`,
  `audit_logs_actor_ck` → 500, `tenants_suspended_reason_ck` → 422 `VALIDATION_FAILED`, the others as their neighbours).
- `scripts/db-test.sh` must pass up/down/up; the Down drops the new tables and columns.
- Check `audit_logs` writes still pass the `ALTER` on a large table: `ADD COLUMN ... NULL` without default is metadata only in PostgreSQL 16/18.

## 5. Sign-in, enrolment and sessions (PR 2)

All under `/api/v1/platform/auth/`, unauthenticated except `me` and `logout`.

| Route | Body | Answer |
|---|---|---|
| `POST /login` | `{email, password}` | 200 `{challenge, next: "totp" \| "enroll", expires_at}`. Wrong pair or inactive account: 401 `INVALID_CREDENTIALS` (one burnt hash for an unknown e-mail, as `iam`). |
| `POST /totp` | `{challenge, code}` or `{challenge, recovery_code}` | 200 `{access_token, expires_in, operator}` + refresh cookie. Wrong code: 401 `TOTP_INVALID` with `context.attempts_left`; reused step: 401 `TOTP_REPLAYED`; challenge expired/used/too many attempts: 401 `CHALLENGE_INVALID`. A challenge of purpose ENROLL: 409 `TOTP_NOT_ENROLLED`. |
| `POST /enroll/start` | `{challenge}` (purpose ENROLL) | 200 `{secret: "JBSW Y3DP …" (grouped by 4), otpauth_uri, qr_png: "data:image/png;base64,…"}`. Calling again replaces the pending secret. |
| `POST /enroll/confirm` | `{challenge, code}` | 200 `{access_token, expires_in, operator, recovery_codes: [10]}` + refresh cookie. Sets `totp_secret_enc`, `totp_enrolled_at`, `totp_last_step`; writes the codes. |
| `POST /refresh` | cookie | As `iam.Refresh` (rotation, reuse detection). |
| `POST /logout` | cookie or bearer | Revokes the session (`LOGOUT`), clears the cookie. |
| `GET /me` | bearer | `{id, email, full_name, recovery_codes_left, last_login_at}` |

- `otpauth_uri` = `otpauth://totp/KamaraPMS%20Platform:<email>?secret=<base32 no padding>&issuer=KamaraPMS%20Platform&algorithm=SHA1&digits=6&period=30`.
- The challenge token is returned in the body (never a cookie), kept by the web app in memory only, and travels in the next request body.
- Every step writes `operator_audit_logs`: `operator.login_password_ok`, `operator.login_failed` (entity = the account when known), `operator.totp_enrolled`,
  `operator.login` (session opened), `operator.recovery_code_used`, `operator.logout`, `operator.refresh_token_reused`.
- Authentication middleware for the console (`operator.Handler.Middleware`): Bearer token → parse (issuer, audience, `osid`) → one query that reads
  the session and the account (revoked, expired, `is_active`) → `operator.WithPrincipal(ctx, Principal{OperatorID, SessionID})`. Missing or bad
  token on a console route: 401 `PLATFORM_TOKEN_INVALID`; expired: 401 `TOKEN_EXPIRED`; revoked: 401 `SESSION_REVOKED`. A hotel user's token on a
  console route: 401 `PLATFORM_TOKEN_INVALID`.

## 6. pms-admin (PR 2)

```
create-operator -email E -name N [-password-env VAR]     (prints a generated password once; TOTP is enrolled at the first sign-in)
list-operators
disable-operator -email E          (is_active=false, revokes sessions DEACTIVATED)
enable-operator -email E
reset-operator-password -email E [-password-env VAR]   (revokes sessions PASSWORD_RESET)
reset-operator-totp -email E       (clears the secret and the recovery codes, revokes sessions TOTP_RESET: next sign-in enrols again)
regenerate-operator-recovery -email E   (prints 10 new codes)
rotate-operator-totp-key -old-env VAR -new-env VAR     (re-encrypts every secret and pending secret in one transaction)
suspend-tenant -tenant T -reason R / reactivate-tenant -tenant T -reason R
```

Each writes `operator_audit_logs` with `operator_id` NULL. `create-operator` refuses when `PMS_OPERATOR_TOTP_KEY` is unset (the console would be off).

## 7. The console API (PR 2)

All routes below require an operator principal. Request and response shapes go into `api/openapi.yaml` under a new tag `platform`
(and `npm run gen:api` in the same PR).

### 7.1 Tenants

| Route | What |
|---|---|
| `GET /api/v1/platform/tenants?q=&status=&plan=` | The list of P4 (§7.3). `status` ∈ `active, trial, grace, read_only, suspended`. Sorted: READ_ONLY, GRACE, trials ending ≤ 14 days, then by name. Answer `{items, summary: {active, trial, trial_ending, grace, read_only, suspended, open_invoice_count, open_invoice_total, overdue_invoice_count}}`. |
| `GET /api/v1/platform/tenants/export.csv` | The same rows as CSV, written with `internal/platform/csvlang.Write` like the other report exports (header in the language of `lang=`, cells unchanged). |
| `POST /api/v1/platform/tenants` | `{code, name, timezone, admin?: {email, full_name}}` → `tenancy.CreateTenant` (its hook starts the default plan) and, with `admin`, `iam.BootstrapAdmin` in the same transaction. Answer includes `admin_password` once. |
| `GET /api/v1/platform/tenants/{code}` | P5: tenant (code, name, timezone, created_at, status, status_reason), `subscription.Show` (subscription + summary with usage and limits incl. plan limits and overrides), `property_count`. |
| `GET /api/v1/platform/tenants/{code}/admins` | Tenant administrators: `full_name, email, is_active, last_login_at`. Nothing else of `users`. |
| `GET /api/v1/platform/tenants/{code}/activity?limit=50` | The tenant's `audit_logs` rows whose `action` starts with `subscription.`, `subscription_invoice.` or `tenant.`, newest first, with the actor resolved: a user's name, "operator: <name>" (from `operator_id`), or "system". |
| `PATCH /api/v1/platform/tenants/{code}/subscription` | Body = `subscription.ChangeInput` in JSON (`plan_code, paid_through, no_end, grace_days, status, limits, clear_limits`). |
| `POST /api/v1/platform/tenants/{code}/suspend` / `/reactivate` | `{reason}` (1–500 chars) and, for suspend, `{confirm_code}` that must equal the tenant code (server-side check, 422 `CONFIRMATION_MISMATCH`). |

### 7.2 Plans and invoices

| Route | What |
|---|---|
| `GET /api/v1/platform/plans` | `ListPlans` plus `tenant_count` per plan. |
| `POST /api/v1/platform/plans` / `PATCH /api/v1/platform/plans/{code}` | `PlanInput` (code only on create). The PATCH answer also returns `affected: [{tenant_code, resource, used, limit}]` for the tenants on this plan that are now over a limit (no override): the warning of P8. |
| `GET /api/v1/platform/invoices?status=open\|overdue\|paid\|void&tenant=` | Every tenant's invoices (P9). `overdue` = ISSUED and `due_on` < today in the tenant's time zone. Answer `{items, total_open}`; each item carries `tenant_code, tenant_name, overdue_days`. |
| `GET /api/v1/platform/tenants/{code}/invoices` | `TenantInvoices`. |
| `POST /api/v1/platform/tenants/{code}/invoices` | `IssueInput` (`plan_code, periods, amount, due_on, notes`). |
| `GET /api/v1/platform/tenants/{code}/invoices/preview?plan=&periods=` | What P6's "Issue invoice" shows before saving: `{period_start, period_end, amount, next_number, open_invoice?: {number, period_start, period_end}}`. Read only; `next_number` is a hint, not reserved. |
| `POST /api/v1/platform/invoices/{number}/pay` | `{paid_on, reference}` → `PayInvoice`. |
| `POST /api/v1/platform/invoices/{number}/void` | `{reason}` → `VoidInvoice`. |

### 7.3 Queries

The tenant list is one query in `internal/operator/queries.sql` (operator scope: no tenant filter by design, the only such query outside
migrations and `pms-admin`). It joins `tenants`, `tenant_subscriptions`, `subscription_plans`, the three usage counts of
`subscription/queries.sql` `Usage` as lateral subqueries, and the sum and count of ISSUED invoices. The access state and the days are computed in
Go with `subscription.Subscription.Access` and the tenant's time zone, never in SQL. With 1000 tenants it must stay under 200 ms on the dev
database (add a test that seeds 300 tenants and checks the plan uses the indexes, or document the measured time in the PR).

### 7.4 Actor of subscription changes

`subscription.Service` methods gain the actor from the context: `operator.PrincipalFrom(ctx)` → `audit.Entry.OperatorID` and an extra
`operator_audit_logs` row (P12). `audit.Entry` gets `OperatorID *int64`; `audit.Writer.Write` fills the new column. `pms-admin` keeps writing
both NULL. Do not import `operator` from `subscription`: put the principal type in `internal/platform/auth` (`auth.OperatorPrincipal`,
`auth.WithOperator`, `auth.OperatorFrom`) so every module can read it.

### 7.5 Locks and transactions

Suspend/reactivate: `WithinTx`, `db.EnterLockLevel(LevelSubscription)` + the tenant's subscription row `FOR UPDATE` (the same row the quota uses),
update `tenants`, revoke the tenant's `user_sessions`, audit. Everything else reuses the service methods and their locks. Sign-in steps lock the
challenge row `FOR UPDATE` to count attempts and to make "single use" hold under concurrency (race test).

## 8. Hotel-side API changes (PR 1)

- `GET /api/v1/subscription` gains `billing_contact` (string or null, from `PMS_BILLING_CONTACT`) and `is_trial_ending` (true when status TRIAL and
  `days_left` ≤ 7). OpenAPI + types.
- `errors.PLAN_LIMIT_REACHED` (en, id) uses the context: en "The plan allows {limit} and {used} are in use.", id "Batas paket tercapai: paket
  mengizinkan {limit}, dan {used} sedang dipakai." The `errorActions` text stays.
- `auditEntity`/`auditAction` gain nothing in PR 1. PR 2 adds the actor label "KamaraPMS (operator)" (en) / "KamaraPMS (operator)" (id) in the
  audit page when `operator_id` is set (the reader returns `actor_kind: "user" | "operator" | "system"`).

## 9. Configuration

| Env | Default | Rule |
|---|---|---|
| `PMS_OPERATOR_TOTP_KEY` | unset (console off, P1) | base64 of exactly 32 bytes; refused otherwise at start-up. |
| `PMS_OPERATOR_ACCESS_TTL` | 10m | ≤ 15m |
| `PMS_OPERATOR_REFRESH_TTL` | 12h | ≤ 24h |
| `PMS_BILLING_CONTACT` | unset | ≤ 200 chars, shown on the hotel's Subscription page |

Document them in `docs/deployment.md` and `.env.example`; the production checklist gets "set `PMS_OPERATOR_TOTP_KEY` and keep a copy in the secret store:
losing it means every operator enrols again (`reset-operator-totp`)".

## 10. Hotel side: Subscription page and banner (PR 1)

### 10.1 Subscription page (screen T1)

- Route `/setup/subscription`, name `subscription`, `meta: { adminOnly: true }`; nav item `{ id: 'subscription', to: '/setup/subscription', adminOnly: true }`
  in the Setup section after `system`; `nav.items.subscription` en "Subscription", id "Langganan".
- `views/setup/SubscriptionView.vue`: `PageHeader` (title, description), the shell banner stays above it.
  - Card "Plan <name>" with the access badge (FULL "Active" / GRACE "Grace period" / READ_ONLY "Read only" / TRIAL "Trial"), `dl` of paid through,
    grace until (+ days left), subscribed since; the billing contact line when set.
  - Card "Usage": three meters (properties, rooms, users) `used / limit` ("Unlimited" when null); colour by ratio: < 85 % accent, ≥ 85 % warning, ≥ 100 % danger,
    with the text of the colour also in words ("Full: a new property cannot be added." / "Almost full: N left."). `role="meter"` with aria values.
  - Card "Modules": CORE ("always") + the five optional modules, included or not.
  - Card "Invoices": `GET /api/v1/subscription/invoices` in `DataTable` (number, period, amount, status, due). Status ISSUED past due shows "Unpaid" (danger),
    ISSUED not due "Issued", PAID "Paid", VOID "Voided". No actions.
- Loading: `Skeleton`; error: `ErrorNotice`.

### 10.2 Store and header (no screen)

- `stores/subscription.ts` keeps `summary` (the whole `SubscriptionSummary`); `modules`, `has()`, `loaded`, `reset()` keep their behaviour; add `access`,
  `daysLeft`, `paidThrough`, `graceEndsOn`, `isTrialEnding`.
- `api/client.ts`: a middleware `watchSubscriptionAccess` that reads `X-Subscription-Access` from every response; when it differs from `summary.access`,
  it calls `useSubscriptionStore().load(true)` (debounced 1 s). A 403 `SUBSCRIPTION_READ_ONLY` also triggers the reload.
- `auth.signedOut()` calls `subscription.reset()` (check it does already).

### 10.3 Banner (screen T2)

`components/shell/SubscriptionBanner.vue`, rendered in `AppShell.vue` inside `<main>` next to the night-audit banner (before `<slot/>`), `data-testid="subscription-banner"`:

| State | Style | Who | Copy (en / id) | Dismiss |
|---|---|---|---|---|
| TRIAL and `days_left` ≤ 7 | new `.alert.info` (accent border, accent 10 % fill) | everyone | "The trial ends on {date} ({n} days left)." + admins: link "View subscription" / "Masa trial berakhir {date} ({n} hari lagi)." | yes, for the rest of the day (`localStorage` key `kamarapms.subBanner.<tenant>.<date>`, wrapped in try/catch) |
| GRACE | `.alert.warning`, `role="status"` | admins | "The subscription ended on {paid}. {n} days of grace left (until {grace}); after that the data is read only." + link | no |
| GRACE | same | others | "This hotel's subscription must be renewed within {n} days. Ask your hotel administrator." | no |
| READ_ONLY | `.alert` (danger), `role="alert"`, lock icon | everyone | "The subscription has ended. Data can still be opened and exported, but not changed until it is renewed." + admins: link | no |

- Days and dates come from the summary, never computed in the browser from the server clock; dates are formatted with the app's date formatter.
- Buttons stay enabled in READ_ONLY: the server refuses and the toast shows `errors.SUBSCRIPTION_READ_ONLY` with its action (no per-page disabling).
- Add `.alert.info` to `assets/tailwind.css` next to `.alert.warning` (accent colours, both themes).

## 11. Console screens (PR 3)

Shared: `layouts/PlatformLayout.vue`: a dark top bar (`#1c2128` light theme, the dark theme's surface in dark mode) with the K mark,
"KamaraPMS", a "Platform" pill (warning colour), the nav **Tenants · Plans · Invoices** (active: white text, accent underline, `aria-current="page"`),
and on the right "<name> · operator" and "Sign out". Content `max-width: 1200px`. Every page uses `PageHeader`. Copy is i18n `platform.*` (en + id;
id uses "Penyewa" for tenant, "Paket", "Invoice", "di-void").

| Screen | Route | Content |
|---|---|---|
| P1 Sign in | `/platform/login` (public, `meta.platform`) | Card max 400 px: mark, title "Platform console", "Operators of KamaraPMS only", Platform pill; e-mail, password (show/hide); "Continue"; error `role="alert"`; note that the next step asks for a 6-digit code and that a lost device is reset with `pms-admin`; link "Sign in as hotel staff" (`/login`); language select. |
| P2 Code | same view, step 2 | "Authenticator code"; the e-mail; one input `name="totp"` `inputmode="numeric"` `autocomplete="one-time-code"` maxlength 6 (spaces ignored); "Sign in"; disclosure "Use a recovery code" (`name="recovery_code"`); "Back"; attempts left from `context.attempts_left`. |
| P3 Enrol | same view, step "enroll" | Step 1 QR (`qr_png`) + the secret with "Copy"; step 2 code + "Activate"; after success the 10 recovery codes with "Download .txt", "Copy all", a checkbox "I have saved them" that enables "Open the console". The codes live only in component state. |
| P4 Tenants | `/platform/tenants` (home) | KPI row from `summary` (Active, Trial, Grace period, Read only, Open invoices with count/overdue); filters: search, Status, Plan (query string, so a link keeps them); `DataTable`: tenant (name + code, link), plan, status badge, paid through + hint ("N days left" / "grace: N days left" / "trial: N days left" / "sign-in blocked"), rooms `used / limit` with a thin meter, users, open invoice amount (danger when overdue), "Open". Buttons: "Export CSV", "+ New tenant" (dialog: code, name, time zone select, optional first admin; shows the admin password once with Copy). |
| P5 Tenant | `/platform/tenants/:code` | Breadcrumb; title + status badge; meta line (code, time zone, since, properties); actions "Change subscription", "Issue invoice", "Suspend" (danger outline) or "Reactivate" when suspended. A status callout for GRACE/READ_ONLY/TRIAL ending with "Record payment" when an invoice is open. Cards: Subscription (`dl`: plan, status, start, paid through, grace days, price/period); Quotas & usage (meters; a "tenant-specific (plan: N)" pill when an override is set; "Set quotas" opens the change dialog at the quota fieldset); Modules (6 tiles); Invoices (table with "Record payment" and "Void" for ISSUED; paid shows date + reference; void shows reason). Aside: Tenant administrators (name, e-mail link, last sign-in; note "The console shows contacts only"); Subscription activity (from `/activity`, "See all" loads 200). |
| P6 Dialogs | on P5 / P9 | **Change subscription**: plan select (active plans + the current one), status (Active/Trial), paid through (date) + "No end date", grace days 0–90, tenant quotas (empty = plan's, shown as placeholder with "Plan: N, used: M"), a note that a paid upgrade is done with an invoice. **Issue invoice**: plan, periods 1–36, the preview (period, next number), the open-invoice conflict notice from the preview, amount (default periods × price), due date, notes. **Record payment**: invoice summary, paid on (default today), reference, "After saving" box (new paid-through, plan, status), note that a paid invoice is final. **Suspend**: `alertdialog`, three bullets (sign-out of every user, data kept, late payment → let grace run out), reason, "Type <CODE> to confirm", danger button. **Void invoice**: reason only. All dialogs use `components/ui/dialog`, focus the first field, `Esc` closes, errors in `ErrorNotice` inline. |
| P7 Plans | `/platform/plans` | Cards (grid, min 340 px): code, name, "Default" / "Not for sale" pills, price + period, three limit tiles, module chips, trial, "N tenants" (link to P4 filtered), "Edit". "+ New plan". |
| P8 Plan form | `/platform/plans/new`, `/platform/plans/:code` | Sections: Identity & price (code read-only on edit, name, IDR price, period 1/3/6/12); Modules (CORE locked on; five checkboxes with a one-line description); Quotas & trial (each with "Unlimited"/"No trial" checkbox that empties the field); Sale ("For sale", "Default plan for new tenants" with the current default named). After save: the `affected` list as a warning callout if any. |
| P9 Invoices | `/platform/invoices` | Tabs Open (default) / Overdue / Paid / Voided with counts; table: number, tenant (link), plan, period, amount, due + "N days late" (danger) / "in N days"; actions as on P5; footer "Total open". |

Router: every console route has `meta.platform: true` (P1 also `public: true`); the guard checks `meta.platform` first and uses
`usePlatformAuthStore` (`init()` = platform refresh; not signed in → `/platform/login?redirect=`). `App.vue`: `meta.platform && !meta.public` →
`PlatformLayout`, `meta.platform && meta.public` → bare view. `document.title` suffix "· KamaraPMS Platform". The `/platform` routes go before the catch-all.

Web files (new): `api/platformSession.ts`, `api/platformClient.ts`, `stores/platformAuth.ts`, `layouts/PlatformLayout.vue`,
`views/platform/{PlatformLoginView,TenantsView,TenantView,PlansView,PlanFormView,InvoicesView}.vue`,
`components/platform/{ChangeSubscriptionDialog,IssueInvoiceDialog,RecordPaymentDialog,SuspendTenantDialog,VoidInvoiceDialog,NewTenantDialog,QuotaMeter,AccessBadge}.vue`.
`QuotaMeter` and `AccessBadge` are also used by the hotel's Subscription page (put them in `components/app/` instead and import from both).

## 12. Tests

Backend (real PostgreSQL, `dbtest`):

- TOTP: RFC 6238 SHA-1 vectors (59, 1111111109, 1111111111, 1234567890, 2000000000, 20000000000 → the 6-digit values), window ±1, replay of the
  same step refused, an earlier step refused, a fake clock throughout.
- Crypto: AES-GCM round trip; a wrong key fails; `rotate-operator-totp-key` keeps codes valid.
- Sign-in: wrong password, unknown e-mail (same timing path), inactive account, challenge expiry, single use under two concurrent `/totp` calls
  (race: exactly one session), 5 wrong codes kill the challenge, rate limits per email/IP/operator, enrolment end to end (start → confirm → codes),
  recovery code single use and normalisation (lower case, no dashes), `recovery_codes_left`.
- Sessions: rotation, reuse detection revokes all, `disable-operator`/`reset-*` revoke, absolute refresh expiry.
- Token separation: an operator token on every hotel route → 401; a hotel token on every console route → 401 `PLATFORM_TOKEN_INVALID`
  (driven from the route list, not hand-picked).
- `authz_matrix_test.go`: `platformPublicRoutes` (login, totp, enroll/start, enroll/confirm, refresh, logout) and `platformRoutes` (all others) with
  their own expectations; `TestEveryRouteButThePublicOnesRequiresAToken` knows both lists. `ModuleOf` skips `/api/v1/platform/`
  (`TestEveryRouteHasAModule` keeps classifying the rest). OpenAPI describes every new route.
- Console API: list filters and sort, summary counts, CSV, create tenant with admin (password returned once; the tenant starts on the default plan),
  change subscription (audit in both trails with the actor), suspend/reactivate (sessions revoked, sign-in refused, reason required, confirm code),
  plan PATCH `affected`, invoice preview, pay, void, overdue in the tenant's time zone.
- Audit: `audit_logs.operator_id` set by console writes and NULL by `pms-admin`; `audit_logs_actor_ck`; the hotel's audit reader shows the
  operator label without the name; `operator_audit_logs` append-only (UPDATE/DELETE/TRUNCATE refused).
- `scripts/db-test.sh` (migration up/down/up); `errors_test` constraint map.

Web (Vitest, fresh `vi.fn()` per test):

- PR 1: SubscriptionView (all access states, unlimited limits, meters' words, invoices, billing contact present/absent), SubscriptionBanner (each row of
  §10.3, admin vs not, dismiss for the day, storage throwing), the header middleware reloading on change, nav item admin-only, guard, i18n parity.
- PR 3: platform guard (never calls the tenant `auth.init()`; redirect to `/platform/login`), platformSession refresh/lost, login steps (wrong
  password, TOTP wrong with attempts left, recovery code, enrolment with codes and the checkbox gate), each list/detail/dialog with mocked client,
  `pageStructure.test.ts` exclusion for `PlatformLoginView.vue`, i18n parity and glossary.
- E2E (`web/e2e/`, optional in PR 3): an operator created with `pms-admin` in the e2e database enrols (TOTP computed in the test from the
  returned secret), issues an invoice and records the payment; the hotel admin sees the banner disappear.

## 13. Security checklist (review before merging PR 2)

- No route under `/api/v1/platform/` other than the six public auth routes works without an operator token (matrix test).
- The TOTP secret never leaves the server after enrolment; it is never logged; `pending_secret_enc` is cleared on confirm and expires with the challenge.
- Challenge tokens, refresh tokens and recovery codes are stored hashed; comparisons are constant-time.
- The console sets `Cache-Control: no-store` on every response (the security headers middleware covers it; check).
- Every operator write is audited with request id and IP (P12).
- Error messages do not reveal whether an e-mail exists.
- The access-log middleware redacts `password`, `code`, `recovery_code`, `challenge` (check `AccessLog` logs no bodies).

## 14. PRs and order

| PR | Contents | Migration | Depends on |
|---|---|---|---|
| **PR 1** hotel side | §8 and §10: `billing_contact`, `is_trial_ending`, the store, the header middleware, `SubscriptionBanner`, `.alert.info`, `SubscriptionView`, nav, i18n, tests | none | nothing (the endpoints exist) |
| **PR 2** operator backend | §4–§9 and §12 backend: migration 00100, `internal/platform/loginlimit` (moved), `internal/operator` (model, totp, crypto, service, store, queries, http), `auth.OperatorPrincipal`, audit `OperatorID`, `tenancy.SetTenantStatus`, the console API, `pms-admin` commands, OpenAPI + types, config, docs | 00100 | — (can run in parallel with PR 1) |
| **PR 3** console UI | §11 and §12 web: platform session/client/store, layout, guard branch, views, dialogs, i18n `platform.*`, tests | none | PR 2 merged (types) |

Each PR runs the full check list of `CLAUDE.md` (build, vet, lint, `go test ./...`, `scripts/db-test.sh` when migrations change, `scripts/sqlc.sh diff`,
web tests, type-check, build) and updates `docs/architecture/README.md` and §16 of this document with what was built.

## 15. Questions for the owner

| # | Question | Answer of the owner (2026-11-14): the recommendation |
|---|---|---|
| Q1 | One operator role, or also a read-only role (e.g. for support staff who may look but not change)? | One role in v1; a `role` column can come later without breaking anything. |
| Q2 | Refresh lifetime of an operator: 12 h absolute (TOTP every working day) or longer? | 12 h. |
| Q3 | May the console create a tenant and its first administrator (P4 "+ New tenant"), or stays that a `pms-admin` task? | Allow it in the console; `pms-admin create-tenant` stays. |
| Q4 | Does a hotel administrator see the operator's name in the hotel's audit trail, or only "KamaraPMS (operator)"? | Only "KamaraPMS (operator)"; the name is in the operator trail. |
| Q5 | Should READ_ONLY last forever, or should the console offer "suspend all tenants READ_ONLY for more than N days"? | Out of scope (as `26-subscriptions.md` §10); the P4 list sorts them first. |
| Q6 | The billing contact on the hotel's page: free text from `PMS_BILLING_CONTACT`, or editable in the console? | Env var in v1. |

## 16. What is built

Nothing yet. Each PR adds its line here: what was built, the migration, deviations from this document and why.

## 17. Not in this work

A payment gateway, proration, e-mail reminders before `paid_through`, PDF of subscription invoices, operator self-service password change in the
console (use `pms-admin reset-operator-password`), WebAuthn/passkeys, IP allow-lists for the console, "log in as tenant", multi-currency plans.
