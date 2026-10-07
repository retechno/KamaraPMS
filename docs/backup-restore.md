# Backup and restore

Status: written on 2026-10-07 with the scripts it describes, and checked by a real restore (section 6 says what was run and what was not). This is the single place for backup and restore; `deployment.md` points here. The owner approved the Backup/DR decisions on 2026-10-07 (section 13): **pilot values are decided; production values are TARGETS, not capabilities** (nothing in the repository reaches them yet, and none may be claimed before the mechanism is built and drilled).

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

- **Decided for the pilot (owner): 4 hours, as a target that the recovery drill must measure.** Measured so far: only on 0.7 MB of data (seconds), which says nothing about a year of data. The drill results are written in section 14.
- **Production TARGET (not a capability): 1 hour.** It must not be claimed before a drill on data of realistic size has achieved it, with the mechanism of production (WAL, off-site copy, decryption) in the path.
- **Assumption:** someone who has done the drill is available. A procedure nobody has practised has a much longer RTO.

## 9. Retention

Provider-agnostic. **Pilot (decided):** 14 daily backups on the machine, and 4 weekly backups on the secondary copy (section 13). **Production (decided as a target):** 14 daily, 8 weekly, 12 monthly and 1 fiscal-year snapshot, with the retention of accounting and tax records to be confirmed by the owner and the accountant; the monthly and the year-end tiers are not implemented.

- The scripts keep the newest N on the machine (`PMS_BACKUP_KEEP`, default 14) and apply the weekly rule on the secondary copy (section 14). A failed backup removes nothing.
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

## 12. Production checklist

- [ ] RPO and RTO decided by the owner and written here (replace the **PROPOSED** lines).
- [ ] A daily scheduled `db-backup.sh`; the log and the exit code are watched, and a failure reaches a person.
- [ ] Backups are copied to another machine or disk, at least weekly, by a method that does not make them public. The copies are access controlled.
- [ ] The backup directory is not in a Git checkout (`/backups/` and `*.dump` are ignored by `.gitignore`), not in a web root, and not world readable.
- [ ] `restore-drill.sh` passed on the production database (or a copy of it), with its time written down as the measured RTO.
- [ ] A backup is taken just before every upgrade, and the upgrade steps say so.
- [ ] `deploy/.env`, the JWT secret and the certificate are stored somewhere that is not the dumps.
- [ ] Someone other than the author has run the restore from this page.

## 13. Decisions (approved by the owner on 2026-10-07)

| # | Decision | Pilot (decided, to be built and drilled) | Production (decided as a TARGET: not a capability until built and drilled) |
|---|---|---|---|
| 1 | RPO | 24 hours | 15 minutes |
| 2 | RTO | 4 hours, measured by the drill | 1 hour, not to be claimed before a drill on realistic data |
| 3 | Frequency | once a day after the night audit, and a backup before every upgrade | daily base backup, continuous WAL, and a backup before every upgrade |
| 4 | Secondary copy | mandatory, on a machine or disk other than the main database | off-site, a different failure domain, separate or write-only credentials |
| 5 | Retention | 14 daily on the machine, 4 weekly on the secondary copy | 14 daily, 8 weekly, 12 monthly, 1 fiscal-year snapshot; accounting and tax retention to be confirmed with the accountant |
| 6 | WAL / PITR | not required | planned as the production mechanism for the 15 minute RPO; **not to be built as part of the pilot** |
| 7 | Encryption | mandatory for the secondary (off-machine) copy | mandatory at rest |

Not decided by the owner and **not guessable from the repository** (OWNER DECISIONS still open): the time of day of the backup (it depends on when the hotel's night audit is done), the channel that receives the failure alert (a webhook URL), where the secondary copy physically is (which machine, disk or off-site location, and over what protocol), who holds the private key of the encryption, and the accountant's answer on record retention.

Consequence for the pilot: the secondary copy gets **every** backup, and its retention keeps the 4 weekly ones **plus the newest few** (`PMS_BACKUP_COPY_KEEP_RECENT`, default 2). If the secondary kept weekly copies only, losing the main machine would lose up to a week, which contradicts the 24 hour RPO. This reading of "4 weekly" is mine; the owner can confirm or reject it.

What the pilot needs: a scheduled daily backup, a failure alert, a secondary copy, its encryption, retention, a backup before every upgrade, and a drill that restores from the secondary copy.
