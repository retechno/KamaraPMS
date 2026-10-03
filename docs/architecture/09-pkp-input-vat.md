# PKP and input VAT (design, proposal for approval)

Status: **steps 1 and 2 built (migrations 00041 and 00042): PKP settings, the kind of a tax, the "PKP status" page; input VAT on supplier bills with the account 1425 (`INPUT_VAT`). Steps 3 and 4 are not built.** Step 2 differs from the design in one thing: `suppliers.is_pkp` is left out (it was only a hint); the VAT of a line is entered as an amount, there is no rate.
Step 1 of the finance order in `08-backlog.md`. Built as designed, with these differences: the settings live in the `taxfiling` module
(`settings.go`) and not in a module of their own; every property gets its first row (not PKP, input VAT as an expense, from 2000-01-01) from the
migration and from a trigger on `properties`, so the change always has a row to lock (`db.TaxSettings`, level of the tax); a change must begin
after the latest one (the history only moves forward); no `INPUT_VAT` account yet (step 2).
Decisions of the owner (2026-10-03): PB1 never takes part in the offset, only VAT; `deferred` stays as the third treatment; existing taxes are
migrated as `LOCAL` unless their code or name says VAT, and the owner confirms the kind under Setup → Taxes.

Original proposal follows. KamaraPMS is a SaaS: a hotel may or
may not be a taxable entrepreneur (PKP), so both modes must work and each property chooses.

## What exists today (read from the code, 2026-10-03)

- `taxes` (per property, `rate` in percent) and `tax_filing_profiles` (per tax: authority, registration number, due day).
  A "tax profile" in the code therefore means *how one tax is filed*, not whether the hotel is PKP.
- The monthly return (`taxfiling`) sees only the **output** side: tax snapshots of folio items. No input side.
- Accounts payable: `supplier_bills` with `supplier_bill_lines (account_id, amount)`. A bill is a journal of lines against
  `ACCOUNTS_PAYABLE`. A line has no tax split and no tax code. `suppliers.tax_id` (NPWP) exists as free text.
- `gl_account_map` keys are a CHECK list (`TAX_PAYABLE`, `ACCOUNTS_PAYABLE`, ...): a new key is a migration.
- `accounting_settings` is one row per property (start date, fiscal year start month).

## Decisions

1. **PKP is a setting of the property, with history.** New table `property_tax_settings`:
   `property_id, effective_from (date), is_pkp, npwp, nppkp_number, pkp_confirmed_on, input_vat_treatment,
   signer_name, signer_title`. Rows are append-only per property, the row in force on a date is the latest with
   `effective_from <= date`. A change never rewrites old documents. Dates are the **document date**, not today.
2. **`input_vat_treatment` is configurable** (`creditable`, `expense`, `deferred`):
   - `creditable`: input VAT goes to a new account `INPUT_VAT` (asset, 1xxx) and is offset against output VAT in the return.
   - `expense`: input VAT is added to the cost of the line; no VAT account is touched.
   - `deferred`: input VAT goes to `INPUT_VAT` but is **not** offset; it waits (e.g. until the hotel becomes PKP).
   - Rules (CHECK + service): a non-PKP row may use `expense` or `deferred`, never `creditable`. A PKP row may use
     `creditable` or `deferred`. Default on creation: PKP gives `creditable`, non-PKP gives `expense`.
3. **The treatment is frozen on each bill line** (`vat_treatment` column), so the journal of a bill never depends on a
   setting changed later. Voiding a bill reverses with the same treatment.
4. **Output VAT stays what it is.** The charge engine computes tax per charge code. Only taxes flagged as VAT take part in
   PKP logic (new `taxes.tax_kind`: `VAT`, `LOCAL`, `OTHER`; PB1 hotel tax is `LOCAL` and is never offset). Existing taxes
   are migrated as `LOCAL` unless their code or name says VAT: the owner confirms the mapping in setup.
5. **Crediting a deferred balance later** (hotel becomes PKP) is a manual journal-type use case with approval and audit,
   never automatic: Indonesian rules on time limits and supporting documents need a human decision.
6. **Tax invoices (faktur pajak)** only exist when `is_pkp` on the date of the sale. Not in this step; see "Steps".

## Schema (one migration, `00041_pkp_input_vat.sql`, with Down)

- `property_tax_settings` as above. FK `(tenant_id, property_id)`, UNIQUE `(property_id, effective_from)`,
  CHECKs for the treatment rule, trigger that forbids UPDATE and DELETE (append-only, `forbid_modification`).
  Seed: one row per existing property, `effective_from = accounting_settings.start_date`, `is_pkp = false`,
  `expense`, so nothing changes for current data.
- `taxes.tax_kind varchar(10) NOT NULL DEFAULT 'LOCAL'` with CHECK; backfill `VAT` where `code`/`name` matches.
- `gl_account_map`: add key `INPUT_VAT`; extend the CHECK; map to a new account `1xxx Input VAT` created for properties
  that have `accounting_settings` (same pattern as `ACCOUNTS_PAYABLE` in migration 31). Check the free code in the seed
  chart before choosing it.
- `supplier_bill_lines`: add `vat_amount numeric(18,3) NOT NULL DEFAULT 0`, `vat_treatment varchar(10)` (NULL when no VAT),
  CHECK `vat_amount >= 0` and `(vat_amount = 0) = (vat_treatment IS NULL)`. `supplier_bills.total` already means the
  gross amount: it becomes the sum of `amount + vat_amount` (the deferred check of totals must be updated, to be found
  in migration 31 and later ones).
- `suppliers`: add `is_pkp boolean`, so a bill can warn when VAT is entered for a non-PKP supplier. Keep `tax_id` as is.
- New error codes and constraint names go into `internal/platform/db/errors.go` with a test, as the rules say.
- Lock order: `property_tax_settings` is read under the existing "tax profiles" level; no new level.

## Backend

- `taxsettings` module (model, service, store, http) under `internal/`: `GET {P}/tax/settings` (current and history),
  `POST {P}/tax/settings` (new row, `effective_from` not before the start of the books and not inside a closed period,
  permission `tax.manage`, approval when the date is in the past, audit in the same transaction).
- `payables` bill use case: for each line with `vat_amount > 0`, read the setting for the bill date, freeze the treatment,
  and build the journal: `creditable`/`deferred` debit `INPUT_VAT` for the VAT, `expense` adds it to the line's account.
- `taxfiling` worksheet: for a VAT tax of a PKP property, add the input side (`INPUT_VAT` of `creditable` lines in the
  month) and show output, input, and net (payable or carried forward). Not PKP: no input side, unchanged behaviour.
  Output and input are frozen in the return lines (new `side` column on `tax_return_lines`, default `OUTPUT`).
- Blockers on the return: treatment missing for a month, VAT lines on bills with a date before the first PKP row.

## API and frontend

- `api/openapi.yaml`: the endpoints above, the new line fields, the worksheet's input side; then `npm run gen:api`.
- Setup page "Tax status" (PKP flag, NPWP, treatment, effective date, history) in English and Indonesian; the bill form
  gets a VAT amount per line that shows the treatment that will apply; worksheet page shows output, input and net.

## Tests (real PostgreSQL)

Treatment rule CHECKs (non-PKP cannot be `creditable`), setting in force by date (before/on/after a change), bill journal
balanced for each of the three treatments, void reverses with the frozen treatment, worksheet net for PKP and unchanged
for non-PKP, tenant isolation, append-only trigger, and a race of two bills and a return filed at the same time.

## Steps

1. `property_tax_settings`, `taxes.tax_kind`, setup page (no money moves yet).
2. VAT on supplier bills and the `INPUT_VAT` account.
3. Input side in the monthly return and the net payable.
4. Tax invoice (faktur pajak) and e-Faktur export for PKP properties: its own design after step 3.

## Open points for the owner

- Confirm that PB1 (local hotel tax) never takes part in the offset, only VAT.
- Do you want `deferred` as a third choice, or only `creditable` and `expense`? It costs one more journal path.
- Prefix and numbering of tax invoices (step 4) and the e-Faktur format version to target.
