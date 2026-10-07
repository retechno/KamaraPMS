# Backup and restore

Status: written on 2026-10-07 with the scripts it describes, and checked by a real restore (section 6 says what was run and what was not). This is the single place for backup and restore; `deployment.md` points here. Three states are kept apart. **Pilot mechanism: implemented and drilled once on one machine** (sections 14 to 16). **Pilot configuration: not complete** until the owner sets the OWNER CONFIGURATION values (section 13) and the final drill is done with the real secondary copy and key. **Production DR: not ready.** The owner approved the Backup/DR decisions on 2026-10-07 (section 13): pilot values are decided; production values are TARGETS, not capabilities (nothing in the repository reaches them yet, and none may be claimed before the mechanism is built and drilled).

## 1. Scope

- **What is backed up:** the PostgreSQL database `pms`. All state of KamaraPMS is in it: the images hold no data and there are no uploaded files. A backup of the database is the backup of the system.
- **What is not in a backup, on purpose:** `PMS_JWT_SECRET`, the SMTP password, `POSTGRES_PASSWORD` and every other setting. They are environment variables of the deployment (`deploy/.env`). Keep them separately, in the place where you keep secrets: a restored database without the old JWT secret works (every user signs in again, which is harmless), and a backup that contains them would make the file more dangerous to lose.
- **The backup is still confidential.** It contains password hashes, refresh-token hashes and guest data (names, documents, e-mail addresses). Handle the file like the database.
- **Not covered:** point-in-time recovery (WAL archiving), replication, backup of the Docker host, of the images or of `deploy/certs/`. See section 11.

| File | Purpose |
|---|---|
| `scripts/db-backup.sh` | one verified backup file |
| `scripts/db-restore.sh` | restore into an empty database, with checks |
| `scripts/db-verify.sql` | fingerprint of a database: row count and row hash per table (compare source and copy) |
| `scripts/restore-drill.sh` | the whole rehearsal in one command, with a throwaway PostgreSQL and the API on the copy |
| `scripts/lib-pg.sh` | shared: runs the PostgreSQL tools in a container or on the host |
| `scripts/backup-agent.sh` | the scheduler of the `backup` service: waits for the time of the day, runs the backup, repeats; `now` runs it once; `healthcheck` |
| `scripts/backup-run.sh` | one scheduled run: backup, encryption, secondary copy, retention, alert |
| `scripts/lib-backup.sh` | the alert, the secondary copy (a directory or SSH) and its retention |

## 2. Backup prerequisites

Either:

- **Container mode** (nothing to install): `PMS_PG_CONTAINER=<name of the running PostgreSQL container>`. The tools of the container are used, so they always match the server version. The user and the password are those of the container (`POSTGRES_USER`, `POSTGRES_PASSWORD`), or `PGUSER` and `PGPASSWORD` if you set them. For the stack of `deploy/` the container is `kamarapms-deploy-db-1`.
- **Host mode:** `pg_dump`, `pg_restore` and `psql` on `PATH`, **of the same major version as the server or newer**, and the libpq variables `PGHOST`, `PGPORT`, `PGUSER` and `PGPASSWORD` (or `PGPASSFILE`).

The password is never an argument of a command: it is read from the environment, and `docker exec -e PGPASSWORD` passes it without writing it. Do not type it in the command line; put it in the environment of a scheduled job, in `~/.pgpass`, or in a file with `chmod 600` that the job sources. The scripts print no credential.

A directory with room for several backups, **on another disk or machine than the database** for the copies you rely on (section 9).

## 3. Backup procedure

```bash
PMS_PG_CONTAINER=kamarapms-deploy-db-1 PMS_BACKUP_DIR=/var/backups/kamarapms scripts/db-backup.sh
```

| Setting | Default | Meaning |
|---|---|---|
| `PGDATABASE` | `pms` | the database to back up |
| `PMS_BACKUP_DIR` | `./backups` | where the files go; created `0700` |
| `PMS_BACKUP_KEEP` | `14` | the newest N backups are kept (`0` keeps all) |

- **Format:** `pg_dump -Fc -Z 6 --no-owner --no-acl`: PostgreSQL custom format, compressed (level 6), without owners and grants so that it restores under any user. It is a consistent snapshot of the whole database taken in one transaction, while the application keeps running. Restore it with `pg_restore`, not with `psql`.
- **Name:** `pms-<UTC timestamp>.dump`, for example `pms-20261007T071236Z.dump`, and `pms-<timestamp>.dump.sha256` next to it. The timestamp is UTC (the hotel's business date is not involved).
- **Permissions:** the script runs with `umask 077`: the directory is `0700` and the files `0600` on Linux. On Windows (Git Bash) `chmod` does nothing; the protection is the NTFS permission of the folder, so put the directory under your user profile or restrict it yourself.
- **Written safely:** the dump goes to a hidden `.partial` file and is renamed only after verification (section 4). A failed backup leaves no file that looks like a backup.
- **Retention:** after a successful backup, older files beyond `PMS_BACKUP_KEEP` are removed (a file and its checksum together). A failed backup removes nothing.
- **Exit codes:** `0` done · `1` wrong usage or configuration · `2` `pg_dump` failed · `3` the dump failed verification · `4` the server cannot be reached (down, wrong credentials, no such database). A scheduler must treat anything but `0` as a failed backup and alert a person.
- **Scheduling is not installed by this repository.** A daily job is one line of cron (`15 2 * * * ... scripts/db-backup.sh >> /var/log/kamarapms-backup.log 2>&1`) or a Windows Task Scheduler task; choose the hour away from the night audit.

## 4. Backup verification

Every backup is checked by the script before it is kept:

1. `pg_restore --list` reads the whole table of contents of the file;
2. the file contains the data of `goose_db_version` (so it is a KamaraPMS database) and has data entries for every table;
3. a SHA-256 is written next to it; `db-restore.sh` checks it again before restoring, so a damaged or modified copy is refused.

This proves the file is readable and complete in structure. It does **not** prove that it restores. Only a restore does (section 5 and the drill, section 6). Do one before relying on the backups, and then regularly (proposed: monthly, and after every release that adds migrations).

## 5. Restore procedure

**Restore into a new, empty database on a new server or container, never over the database in use.** A restore over a live database, even by accident, would destroy today's ledger. The script refuses a target that has any table.

```bash
# 1. a new, empty PostgreSQL (the same major version; here a throwaway container with a name of its own, never "kamarapms")
docker network create kamarapms-restore
docker run -d --name kamarapms-restore-db --network kamarapms-restore -e POSTGRES_USER=pms -e POSTGRES_PASSWORD="$NEW_PASSWORD" postgres:16-alpine

# 2. restore (creates the database if it does not exist; refuses one that is not empty)
PMS_PG_CONTAINER=kamarapms-restore-db PGPASSWORD="$NEW_PASSWORD" PMS_RESTORE_DB=pms scripts/db-restore.sh backups/pms-20261007T071236Z.dump

# 3. bring the schema to the current release, then start the API on it
docker run --rm --network kamarapms-restore -e PMS_DATABASE_URL="postgres://pms:$NEW_PASSWORD@kamarapms-restore-db:5432/pms?sslmode=disable" \
  --entrypoint /usr/local/bin/migrate kamarapms-api:local up
```

For a real recovery, the new database becomes the one the deployment uses (`POSTGRES_PASSWORD`, `PMS_DATABASE_URL` in `deploy/.env`, a new volume), and the API is started with the **old `PMS_JWT_SECRET` or a new one** (a new one signs everybody out). A backup taken at release N restores to schema N; run `migrate up` with the images of the release you want to run.

What `db-restore.sh` does: checks the file (exists, checksum, readable) → connects → creates or checks the target (empty) → `pg_restore --exit-on-error --no-owner --no-acl` (the first error stops it; a partial restore is never reported as done) → checks the migration version, the number of tables against the backup, and prints the fingerprint.

Exit codes: `0` restored and checked · `1` wrong usage (target name must be letters, digits and underscores; `postgres`, `template0` and `template1` are refused) · `2` the file is unusable (missing, checksum differs, not readable) · `3` the server cannot be reached, the credentials are refused, or the container is not running · `4` the target is not empty · `5` `pg_restore` failed (the target is incomplete: drop it) · `6` the restored database failed the checks.

## 6. Restore verification: what was run

On 2026-10-07, on the development machine (Docker Desktop, PostgreSQL 16).

**Real restore of the development database** (the richest data available; read only on the source):

| Step | Result |
|---|---|
| source | container `kamarapms-db-1`, database `pms`, migration version 58, 99 tables, 1 994 rows |
| backup | `db-backup.sh`: 717 315 bytes, verified |
| target | a new container `kamarapms-backup-test` (postgres:16-alpine, own network, no published port), empty |
| restore | `db-restore.sh`: exit 0, version 58, 99 tables |
| comparison | `db-verify.sql` on the source and on the copy: **identical output**, the row count and the row hash of all 99 tables |
| migration on the copy | `migrate up` applied 4 migrations (58 → 62) on the populated copy; the version is 62 |
| API on the copy | `/healthz` 200, `/readyz` ready; sign-in with a new administrator created on the copy; a wrong password is refused; `/properties`, `/reservations`, `/folios`, `/business-days` and `/payments` return the restored data |

Data seen in the source and in the copy (counts equal; the content hash equal): tenants 1, properties 2, users 2, roles 1, user_properties 1, reservations 1, stays 1, folios 1, folio_items 5, payments 3 (one is a refund), gl_journals 3, gl_journal_lines 6, business_days 4, cashier_shifts 2, companies 1, guests 5, suppliers 1, taxes 2, tax_filing_profiles 1, audit_logs 42.

**UNVERIFIED with data** (the tables were empty in the source, so only their structure was restored and checked): city ledger invoices and receipts, supplier bills and payments, tax invoices, tax returns and tax payments, bank statements, the e-mail outbox. A restore of a database with these rows has not been seen.

**The drill on the stack of `deploy/`** (`scripts/restore-drill.sh kamarapms-deploy-db-1`): backup, new container, restore, fingerprint identical (migration 62, 101 tables, 69 rows), the API ready on the copy, the temporary containers removed. The stack `kamarapms-deploy` kept running and was not changed. The development database was only read: same container, same volume, same data afterwards.

**Not done:** a restore of a database of realistic size and the measured time of it (the data here is 0.7 MB, so no RTO can be derived); a restore on another machine; host mode (`pg_dump` on the host); a restore of a backup made by an older release other than the 58 → 62 case above.

## 7. RPO

- **Decided for the pilot (owner, 2026-10-07): 24 hours.** A daily backup after the night audit gives a recovery point of up to 24 hours of lost data. The assumption that makes it acceptable: during the parallel pilot the old system stays the system of record, so a lost day can be entered again from it.
- **Production TARGET (not a capability): 15 minutes**, to be reached with continuous WAL archiving (section 13, decision 6). It is not implemented. Until WAL archiving exists and a point-in-time restore has been drilled, the real RPO of this repository is 24 hours, and nobody may state 15 minutes as something the system delivers.

## 8. RTO

- **Decided for the pilot (owner): 4 hours, as a target that the recovery drill must measure.** Measured so far: only on 0.7 MB of data (seconds), which says nothing about a year of data. The drill results are in section 16.
- **Production TARGET (not a capability): 1 hour.** It must not be claimed before a drill on data of realistic size has achieved it, with the mechanism of production (WAL, off-site copy, decryption) in the path.
- **Assumption:** someone who has done the drill is available. A procedure nobody has practised has a much longer RTO.

## 9. Retention

Provider-agnostic. **Pilot (decided):** 14 daily backups on the machine; on the secondary copy 14 daily backups **plus** 4 weekly ones. The weekly ones are in addition to the daily ones, never instead of them: the secondary must always hold a recovery point of the last 24 hours, because that is the RPO (section 13). **Production (decided as a target):** 14 daily, 8 weekly, 12 monthly and 1 fiscal-year snapshot, with the retention of accounting and tax records to be confirmed by the owner and the accountant; the monthly and the year-end tiers are not implemented.

- The scripts keep the newest N files on the machine (`PMS_BACKUP_KEEP`, default 14) and apply the rule of section 14.5 on the secondary copy. A failed backup removes nothing.
- A backup is not the legal archive of the books. The database is; how long records must be kept is for the owner and the accountant, and it is not stated here.
- Never delete the newest verified backup by hand, and never keep the only copy on the disk that holds the database.

## 10. Failure handling

All of these were run on 2026-10-07 and the exit code observed:

| Situation | Result |
|---|---|
| backup: wrong password | exit 4, no file |
| backup: no such database | exit 4, no file |
| backup: server stopped | exit 4, no file |
| backup: a database that is not KamaraPMS | exit 3, no file kept |
| backup: directory that cannot be written | exit 1 |
| restore: a file that is not a dump | exit 2, nothing created |
| restore: truncated dump | exit 2, nothing created |
| restore: checksum differs | exit 2 |
| restore: missing file | exit 2 |
| restore: wrong password | exit 3 |
| restore: container not running or not found | exit 3 |
| restore: the target already has tables | exit 4, "nothing was changed", the target is untouched |
| restore: failing half way (target refused writes) | exit 5, "this is NOT a restored backup" |
| restore: bad target name, or `postgres` | exit 1 |

Not run: `pg_dump` and `pg_restore` of different major versions in host mode; a full disk during a backup (the `.partial` file is removed on any exit of the script, but this was not tried). A backup that failed leaves the older backups in place.

## 11. Disaster recovery notes

- **Lost database volume or server:** new PostgreSQL → `db-restore.sh` of the latest verified backup → `migrate up` → API and proxy → check one folio and the business day. Then think about what was lost since the backup (section 7) and re-enter it from paper records.
- **A bad migration or a bad release:** `migrate down` is not a safe way back (docs/deployment.md, section 8). The way back is a restore taken **before** the migration. **Take a backup immediately before every upgrade** (`scripts/db-backup.sh`), and keep it until the release is accepted.
- **Corrupt data found days later:** restore the backup from before it into a *separate* database, read what is needed from it, and fix the live data with a correcting entry. The ledger is append-only; do not replace the live database with an old one unless you accept losing everything since.
- **After a restore:** the business day, the document sequences and the folio numbers continue from the backup. Anything created after the backup is gone, so numbers issued after it (for example a tax invoice already given to a guest) can be issued again. Check the last numbers against paper before opening the day.
- **Not covered, needed later:** WAL archiving or a managed database with point-in-time recovery (an RPO of minutes), a standby, restore of the Docker host, backup of `deploy/.env` and the TLS certificate (store them in a secret store, not with the dumps), encryption of the backup files at rest (encrypt the destination, or add `age` or `gpg` to the copy step; the scripts do not).

## 12. Checklists

**Pilot (the minimum before one property starts a parallel pilot).** Everything below is built (section 14); each line needs its value from the owner, or its drill:

- [ ] The decisions of section 13 are in force, and the OWNER CONFIGURATION values of that section are set (the hour, the alert webhook, the secondary place, the key and its two storage places; the accountant's answer for the production tiers).
- [ ] The `backup` service runs, with `PMS_BACKUP_AT` after the usual night audit, and its container is healthy (`docker compose -f deploy/compose.yaml ps`).
- [ ] The alert reaches a person: a deliberate failure was sent and received (section 14.3).
- [ ] The secondary copy is on **another machine or disk** (the service refuses the same file system), is encrypted with the public key of the owner, and the private key is **not** on the server and is stored in two places that are not the server (section 14.4).
- [ ] `restore-drill.sh` with `DRILL_FROM` and `DRILL_IDENTITY` restored the **latest file of the real secondary copy**, and the time is written in section 16.
- [ ] A backup is taken just before every upgrade (section 15).
- [ ] `deploy/.env`, the JWT secret and the certificate are stored somewhere that is not the dumps.
- [ ] Someone other than the author has run the restore from this page.
- [ ] The people of the pilot know that the recovery point is 24 hours, and that the old system is the system of record until KamaraPMS is.

**Production (TARGETS: none of these exists yet).** WAL archiving and a point-in-time restore, drilled; an off-site copy with separate or write-only credentials; 14 daily, 8 weekly, 12 monthly and 1 fiscal-year retention; encryption at rest; a drill on data of realistic size that reaches the 1 hour RTO; the accountant's retention requirement.

## 13. Decisions (approved by the owner on 2026-10-07)

| # | Decision | Pilot (decided, to be built and drilled) | Production (decided as a TARGET: not a capability until built and drilled) |
|---|---|---|---|
| 1 | RPO | 24 hours | 15 minutes |
| 2 | RTO | 4 hours, measured by the drill | 1 hour, not to be claimed before a drill on realistic data |
| 3 | Frequency | once a day after the night audit, and a backup before every upgrade | daily base backup, continuous WAL, and a backup before every upgrade |
| 4 | Secondary copy | mandatory, on a machine or disk other than the main database | off-site, a different failure domain, separate or write-only credentials |
| 5 | Retention | 14 daily on the machine; on the secondary copy 14 daily **plus** 4 weekly (the weekly ones are an addition, never a replacement of daily recovery points) | 14 daily, 8 weekly, 12 monthly, 1 fiscal-year snapshot; accounting and tax retention to be confirmed with the accountant |
| 6 | WAL / PITR | not required | planned as the production mechanism for the 15 minute RPO; **not to be built as part of the pilot** |
| 7 | Encryption | mandatory for the secondary (off-machine) copy | mandatory at rest |

**OWNER CONFIGURATION** (values only the owner can give; the repository does not guess them, and the pilot configuration is **not complete** until they are set; `deploy/.env.example` has the variable for each):

| # | Value | Who / when | Variable |
|---|---|---|---|
| 1 | The time of the daily backup, after the night audit | the owner, from the time of the hotel's night audit | `PMS_BACKUP_AT`, `TZ` |
| 2 | The alert webhook | the owner, at deployment | `PMS_BACKUP_ALERT_WEBHOOK` (and optionally `PMS_BACKUP_PING_URL`) |
| 3 | The secondary location (which machine or disk, over what protocol) | the owner, at deployment | `PMS_BACKUP_SECONDARY_PATH` + `PMS_BACKUP_COPY_DIR`, or `PMS_BACKUP_COPY_SSH` + `PMS_BACKUP_SSH_DIR` |
| 4 | The owner's private key and its two storage locations | the owner, at deployment (the public key goes to the server) | `PMS_BACKUP_AGE_RECIPIENT` (public key only) |
| 5 | The accounting and tax retention requirement | the owner with the accountant | none yet: it decides the production tiers (monthly, fiscal year) |
| 6 | Secondary retention | **decided: 14 daily + 4 weekly (in addition)** | `PMS_BACKUP_COPY_KEEP_DAILY=14`, `PMS_BACKUP_COPY_KEEP_WEEKS=4` |

The final drill (section 16) is done after 1 to 4 are set, with the real secondary copy, the real private key and the real deployment, and its time is written down.

What the pilot needs: a scheduled daily backup, a failure alert, a secondary copy, its encryption, retention, a backup before every upgrade, and a drill that restores from the secondary copy.

## 14. The pilot mechanism (built)

The `backup` service of `deploy/compose.yaml` (image target `backup` of the `Dockerfile`: Alpine with the PostgreSQL 16 client tools, age, ssh, curl and the scripts; not root; read-only file system; no Docker socket). It reaches the database over the private network like the API does and needs no cron, no systemd and no host feature, so it runs wherever the stack runs. It replaces nothing: `db-backup.sh` and `db-restore.sh` are what it calls.

### 14.1 The scheduler

`scripts/backup-agent.sh` waits for `PMS_BACKUP_AT` (HH:MM, 24 hour clock, in the time zone `TZ`; default 03:30 UTC) and runs `scripts/backup-run.sh`, every day. **Choose the time after your night audit is normally done** (the dump is a consistent snapshot even while the system is in use, so a late audit does not corrupt it; it is only that the late audit is then in the next day's backup). If the service starts and the last successful backup is older than 24 hours, or there is none, it runs at once (a machine that was off at the scheduled time does not wait another day). The health of the container is "the last run succeeded less than 26 hours ago" (`docker compose ps`).

### 14.2 What one run does

1. `db-backup.sh`: the verified local dump (section 3); the newest 14 are kept (`PMS_BACKUP_KEEP`).
2. **The secondary copy is mandatory.** Without one the run fails (exit 3) and says so, after making the local backup (`PMS_BACKUP_REQUIRE_COPY=0` is for a trial only).
3. The destination is checked: a directory must be writable and on **another file system** than the local backups (a second directory on the same disk is refused, exit 5; `PMS_BACKUP_COPY_ALLOW_SAME_DISK=1` lifts it for a trial and is logged as a warning). An SSH destination must be reachable with its host key checked.
4. The dump is encrypted (14.4), copied through a temporary name, renamed, and its SHA-256 is checked **at the destination**.
5. Retention of the secondary copy (14.5).
6. The result is written to the state volume, and a success requests `PMS_BACKUP_PING_URL` if it is set.

Exit codes of a run: `0` done · `1` configuration · `2` the local backup failed · `3` no secondary copy configured · `4` encryption failed (also: no key configured; nothing is copied) · `5` the secondary copy failed · `6` retention failed.

### 14.3 The alert

A failure of any step posts JSON (`{"text": ..., "content": ..., "level": ..., "service": ..., "host": ...}`) to `PMS_BACKUP_ALERT_WEBHOOK`: any URL that accepts a JSON POST (Slack-style and Discord-style incoming webhooks read `text` and `content`; ntfy, Mattermost or a script of your own can read it too). The URL usually holds a token: it is read from the environment and never logged. If the webhook itself fails, the log says so and the result of the backup is unchanged. A script cannot report that it never ran: for that, set `PMS_BACKUP_PING_URL` to a dead-man's-switch address (any service of that kind, or your own) that is requested after every success and alerts when the pings stop. The health of the container (14.1) is the third signal. **Which webhook, and who receives it, is an owner decision**; test it once with a deliberate failure (for example start the service without a secondary copy).

### 14.4 The secondary copy: place, format, encryption and keys

- **Place** (provider-agnostic, one of): `PMS_BACKUP_COPY_DIR` (the container path `/secondary`, mounted from `PMS_BACKUP_SECONDARY_PATH`: a second disk, a NAS or USB share, a folder that another tool replicates), or `PMS_BACKUP_COPY_SSH=user@host:/absolute/path` with a directory (`PMS_BACKUP_SSH_DIR`) holding `ssh_key` and `known_hosts` (the host key is verified, never accepted blindly). No cloud provider is named or assumed. Where it physically is stays an owner decision.
- **Format:** `pms-<UTC timestamp>.dump.age` (the custom-format dump, encrypted) and `pms-<timestamp>.dump.age.sha256` (the checksum of the encrypted file).
- **Encryption:** [age](https://age-encryption.org) with the **public** key(s) in `PMS_BACKUP_AGE_RECIPIENT`. The server can encrypt and **cannot decrypt**: a server that is stolen or hacked does not give up the old backups. Make the key pair once, off the server: `age-keygen -o kamarapms-backup.key` (it prints the public key `age1...`). The **private key file is the only way to read a secondary copy**: store it in at least two places that are not the server and not the secondary copy itself (the owner's password manager, a sealed paper copy), and write down who holds it. A lost key is a lost backup. The local backups on the machine are not encrypted (the decision asks encryption for the copy that leaves the machine; encrypt the disk of the machine, BitLocker or LUKS).
- **Restore from it:** `db-restore.sh pms-....dump.age` with `PMS_BACKUP_AGE_IDENTITY=<the key file>` checks the checksum, decrypts into a private temporary directory that is removed at the end, and restores (a wrong key, a changed file and a missing key each exit 2).

### 14.5 Retention (pilot: 14 daily + 4 weekly)

Local: the newest 14 files (`PMS_BACKUP_KEEP`). Secondary: **14 daily recovery points plus 4 weekly ones.** Concretely, a backup is kept when it is the newest of one of the last 14 calendar days (UTC) that have a backup (`PMS_BACKUP_COPY_KEEP_DAILY`), **or** the newest of one of the last 4 ISO weeks that have a backup (`PMS_BACKUP_COPY_KEEP_WEEKS`). The weekly ones reach further back than the daily ones and are an addition: they never remove a daily backup, so after the loss of the main machine the secondary always holds a recovery point from the last 24 hours (the RPO). A day with several backups (a backup before an upgrade) keeps its newest. Only files named `pms-<stamp>.dump.age` and their `.sha256` are ever removed; other files in the destination are never touched. Retention runs after a verified copy.

### 14.6 Enabling it (the values the owner must give)

In `deploy/.env` (the variables are in `deploy/.env.example`): `TZ` and `PMS_BACKUP_AT`; the secondary place (`PMS_BACKUP_SECONDARY_PATH` with `PMS_BACKUP_COPY_DIR=/secondary`, or `PMS_BACKUP_COPY_SSH` with `PMS_BACKUP_SSH_DIR`); `PMS_BACKUP_AGE_RECIPIENT`; `PMS_BACKUP_ALERT_WEBHOOK` (and optionally `PMS_BACKUP_PING_URL`). These are the OWNER CONFIGURATION values of section 13. Then `docker compose -f deploy/compose.yaml up -d --build backup`. Until the secondary place and the key are set, the service runs, makes the local backup, and **fails and alerts every day**: that is intended.

## 15. Backup before an upgrade (procedure; mandatory)

Before every upgrade (new images, `migrate up`), in this order:

1. `docker compose -f deploy/compose.yaml run --rm backup now`. It is the full run: dump, encryption, secondary copy. **Do not go on unless it exits 0** (a failed copy means the upgrade has no safe way back).
2. Note the file name (`pms-<timestamp>.dump`) in the upgrade record.
3. Upgrade (`up -d --build`, which runs the migration job).
4. Run `scripts/prod-smoke.sh` and the readiness check (`GET .../accounting/readiness`).
5. Keep that backup until the release is accepted. The way back from a migration on a populated database is a restore of it (section 11), never `migrate down`.

## 16. Drill record

How to run the drill from the secondary copy (`DRILL_FROM` is the file, `DRILL_IDENTITY` the private key; `DRILL_COMPARE=0` when the source changed since that backup was made):

```bash
DRILL_FROM=/path/to/secondary/pms-<stamp>.dump.age DRILL_IDENTITY=/path/to/kamarapms-backup.key scripts/restore-drill.sh kamarapms-deploy-db-1
```

It decrypts and restores in the `backup` image onto a throwaway PostgreSQL (`kamarapms-backup-test`, its own network), compares the row counts and row hashes of all tables with the source, runs `migrate up` on the copy, starts the API on it, and prints the time. It removes what it made.

**Run on 2026-10-07 on the development machine (stack `kamarapms-deploy`, 101 tables, 1.3 MB dump, 69 rows):**

| What | Result |
|---|---|
| Scheduled run (the service waited for 14:48 UTC and ran at 14:48:00) | encrypted copy verified at the destination, state `ok`, container healthy |
| Catch-up at start (no successful backup on record) | ran at once |
| Secondary on a host directory (another file system than the volume) | accepted; the same Docker volume is refused (exit 5) |
| Secondary over SSH, host key checked | copied and checksum-verified; a wrong host key is refused (exit 5) |
| Retention on 46 daily files (rule 14 daily + 4 weekly) | kept the 14 daily recovery points and the weekly one that reaches further back (15 files), removed the rest, left `notes.txt` alone. An earlier version kept one a week plus the 2 newest, which could lose up to a week; it was replaced the next day |
| Alert | received by a local webhook for: no secondary (exit 3), no key (exit 4); an unreachable webhook is logged and the exit code stays |
| **Restore from the encrypted secondary copy** | checksum ok, decrypted, restored (migration 62, 101 tables), fingerprint **identical** to the source, `migrate up` applied 0, API ready |
| **Recovery time measured** | file restored after 20 s, API ready after **25 s**; the file was 1 minute old (the recovery point of that drill) |
| Wrong private key, changed file, no key | each exit 2, nothing restored |

**What this does not prove.** The 25 seconds are for 1.3 MB; they are **not** the pilot RTO of 4 hours being met in practice, and nothing here says what a year of data takes. The drill was run by someone who knows the system; a person doing it for the first time during an incident will take longer. The recovery point of the drill (1 minute) is the age of one file, not the 24 hours that the schedule gives in the worst case. The alert was received by a local test receiver, not by the owner's real channel; the secondary copy was a host directory and an SSH container on the same machine, not another machine; the private key was a test key. The production values (15 minutes, 1 hour, WAL, off-site, 14/8/12/1) are **not built and not drilled**.
