# PKP and input VAT (design, proposal for approval)

Status: **steps 1, 2 and 3 built (migrations 00041, 00042 and 00043): PKP settings, the kind of a tax, the "PKP status" page; input VAT on supplier bills with the account 1425 (`INPUT_VAT`); the input side of the monthly VAT return with the credit carried forward. Step 4 (tax invoices) is not built.** Step 2 differs from the design in one thing: `suppliers.is_pkp` is left out (it was only a hint); the VAT of a line is entered as an amount, there is no rate.
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

---

# Step 3: the input side of the monthly VAT return and the credit carried forward (approved 2026-10-03)

Decisions of the owner: a **running balance chain** on the returns (no credit ledger); the offset is **journaled when the return
is filed**; **no refund (restitution) of a credit** to cash in this step; late and voided bills follow the claim rules below;
an **opening credit** for hotels that start with one is part of this step.

## The arithmetic of a return of the VAT tax

    available       = credit_brought_forward + input_claimed
    offset_amount   = least(tax_amount, available)          (tax_amount = the output VAT of the month)
    payable_amount  = tax_amount - offset_amount             (what is paid to the authority)
    credit_carried  = available - offset_amount              (what the next month starts with)

Month A: output 10,000,000, input 15,000,000, brought forward 0 gives offset 10,000,000, payable 0, carried 5,000,000.
Month B: output 12,000,000, input 3,000,000, brought forward 5,000,000 gives offset 8,000,000, payable 4,000,000, carried 0.
`available` can be negative (more claimed input was reversed than there is credit): the offset is then negative, the payable
exceeds the output and the credit carried is 0. Other taxes (PB1) have all of this at zero and `payable = tax_amount`.

## Schema (migration 00043)

- `tax_returns` gets `input_claimed`, `credit_brought_forward`, `offset_amount`, `payable_amount`, `credit_carried_forward`,
  `offset_journal_id`, `offset_void_journal_id`. `tax_amount` keeps meaning the output tax. One CHECK holds the arithmetic above, so a
  frozen return cannot disagree with itself. Existing returns are backfilled once (`payable_amount = tax_amount`).
- A trigger on insert: `credit_brought_forward` equals the credit carried by the live return of the month before (or the opening
  credit when there is none), and no live return of a later month exists. This is what makes the credit consumed once.
- `tax_return_input_claims`: the input side of a return, frozen, one row per bill line claimed (or a negative row that reverses
  an earlier claim of a bill that was voided later). Append-only except `released_at`, set when the return is voided. A bill line is
  claimed once among the live claims (partial unique index); a reversal reverses one claim once; the claims add up to
  `input_claimed` (deferred trigger).
- `tax_opening_credits`: the credit a hotel starts with (POSTED or VOIDED, journal Dr INPUT_VAT / Cr opening balance equity 3900),
  one live per tax, only while the tax has no live return.
- `tax_filing_profiles.claims_input_vat`: the profile whose return claims the input VAT; at most one per property, a VAT tax.

## Rules

- **Claims.** A return claims the CREDITABLE lines of POSTED bills with a bill date up to the end of its month that no live return
  claims yet (so a late bill goes to the first open month), and reverses the claims of bills voided on or before the end of its month.
- **Filing** (profile lock): worksheet recomputed, the offset journal (type TAX, dated the filing date: Dr the tax payable account,
  Cr INPUT_VAT, sides swapped when negative), the return, its lines and its claims.
- **Void**: only the latest live return of the tax (`TAX_RETURN_NOT_LATEST`), without payments (as before); the offset journal is
  reversed on the business date and the claims are released. A correction is a void and a new filing.
- **Payments** are limited by `payable_amount`; a return with nothing payable is paid already. The journal of a payment is unchanged.
- **Liability** report: owed = collected - offset of the filed returns - paid; the credit available is shown.

## API and UI

Worksheet and return: `input` (claims), `input_claimed`, `credit_brought_forward`, `offset`, `payable`, `credit_carried_forward`.
Periods: `payable`, `credit_carried_forward`. Profile: `claims_input_vat`, `opening_credit`. `POST/void
{P}/tax/profiles/{id}/opening-credit`. The worksheet card shows output, input (by bill), credit brought forward, offset, payable and
credit carried; the profile form has the flag and the opening credit; the liability page shows the credit.

---

# Step 4: tax invoices (faktur pajak) and the export (approved 2026-10-03, built in migration 00044)

Status: **built as proposed** (decisions of the owner: city ledger invoices and closed folios; every VAT component is on the invoice; no backdating). **The export file is a neutral CSV**: the layout of the tax authority's system (Coretax XML or the older e-Faktur CSV) was not given, so it is not built; it can be added behind `FormatCSV` when the official template is at hand. The original proposal follows.

Only for a property that is PKP on the date of the invoice. The VAT return of step 3 is not
touched: it keeps reading the VAT collected from the folios, so an invoice never changes a return.

## What exists

Seller data (`property_tax_settings`: NPWP, PKP number, signer; `properties`: name, address), buyer data (`companies.tax_id`, name,
address), the VAT of every folio item (`folio_item_components`, `component_type = 'TAX'`, base and amount, tax of kind VAT in
`taxes.tax_kind`), the city ledger invoice (groups transfers of closed folios) and its PDF, the sequence table
(`document_sequences`), the approval flow and the append-only pattern.

## Proposal

1. **What is invoiced.** A tax invoice is issued for one *source*: a city ledger invoice (the company is the buyer, its NPWP is
   `companies.tax_id`) or a closed guest folio (the buyer is typed at issue: name, NPWP or NIK, address). Exactly one live tax
   invoice per source.
2. **What it says.** One line per VAT component of the folios behind the source: charge, taxable base (DPP), rate, VAT. A city
   ledger invoice takes the VAT of a folio in proportion to the transfer (`folio VAT x transfer / charged`, the rounding left on the
   last line); a fully transferred folio, the usual case, is exact. Seller and buyer are copied onto the invoice when it is issued,
   so a later change of an address or a signer does not rewrite it.
3. **Numbers.** An internal reference (new sequence `TAX_INVOICE`, `TXI000001`) and the **official number** (`djp_number`), which the
   tax authority's system gives when the invoice is uploaded: it is recorded afterwards, unique per property, never made up here.
4. **Void and replace.** A tax invoice goes `ISSUED -> VOIDED` (reason and approval); a replacement is a new invoice that points to
   the one it replaces (`replaces_invoice_id`) and can only be made once the old one is void. A city ledger invoice with a live tax
   invoice cannot be voided before it.
5. **Export.** A batch of the invoices of a period is written as a file and recorded (`tax_invoice_exports`, the invoices in it,
   a hash of the file), so what was sent can be shown and sent again. The file format is behind one interface and **one format is
   built first** (to be chosen, see the questions).
6. **Where the VAT is.** Seller NPWP is required, the buyer's NPWP must be 15 or 16 digits, the VAT must be above zero, and the
   property must be PKP on the issue date (`SettingsOn`).
7. **Reconciliation.** A report of the VAT collected in a month against the VAT on tax invoices, with the folios that carry VAT and
   have no tax invoice (information only; nothing blocks a return).

## Schema (one migration)

- `tax_invoices`: id, tenant, property, `invoice_ref`, `status` (ISSUED, VOIDED), `issue_date` (the business date),
  `source_type` (CITY_LEDGER_INVOICE, FOLIO) with `city_ledger_invoice_id` or `folio_id` (CHECK: exactly one, composite FKs),
  seller and buyer snapshots, `taxable_base`, `vat_amount`, `djp_number` (unique per property when set), `replaces_invoice_id`,
  void columns, `approved_by`, `idempotency_key`. A partial unique index per source where `status = 'ISSUED'`. A guard trigger: only
  `ISSUED -> VOIDED` and the one-time set of `djp_number`.
- `tax_invoice_lines`: frozen lines (charge code and name, base, rate, VAT); append-only; a deferred trigger makes the lines add up
  to the totals of the invoice.
- `tax_invoice_exports` and `tax_invoice_export_items`: append-only.
- `document_sequences`: the type `TAX_INVOICE`. New permission `tax.invoice` (issue, void, set the official number, export).

## API and screens

`GET/POST {P}/tax/invoices` (issue, `Idempotency-Key`), `GET {P}/tax/invoices/{id}`, `POST .../void`, `PUT .../djp-number`,
`GET .../{id}/pdf`, `POST {P}/tax/invoices/exports` (a period, returns the file) and `GET .../exports`, `GET {P}/tax/invoices/coverage`.
A "Tax invoices" page under Tax (list, filters, status, official number, export of a period), the button "Issue tax invoice" on a
city ledger invoice and on a closed folio, a PDF of the invoice, and the coverage report.

## Tests and edge cases

Issue for a city ledger invoice and for a folio; the proportional VAT of a partly transferred folio and its rounding; not PKP on the
date; missing seller or buyer NPWP; one live invoice per source under a race; void and replace; the official number set once and
unique; a city ledger invoice with a live tax invoice cannot be voided; an export lists each invoice once per batch and can be
repeated with the same content; the totals and the snapshots do not change when the company or the property changes; tenant
isolation and permissions.

## Questions for the owner

1. **Scope.** City ledger invoices and closed folios (as above), or city ledger invoices only at first?
2. **File format.** Which one does the tax authority's system take today for the upload of tax invoices (the XML of Coretax, or the
   older e-Faktur CSV)? One is built; the other can follow behind the same interface.
3. **Which VAT lines.** Are all charges that carry a VAT-kind tax on the invoice, or only those the hotel marks as subject to a tax
   invoice (for example meeting rooms but not the room, whose tax is the hotel tax PB1)? The proposal takes every VAT component.
4. **Late and past months.** An invoice is dated the business date it is issued; for a supply of an earlier month the invoice is
   still issued today (no backdating). Agreed?
