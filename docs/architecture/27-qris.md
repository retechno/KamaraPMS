# QRIS as a payment method

QRIS is a way for a guest or a company to pay (`payment_method = 'QRIS'`), received the way a card or an e-wallet is: the money is not in the bank yet; the acquirer pays it out later, net of its fee (MDR) and the VAT on it. So QRIS has its own clearing account and takes the fee rules and the settlement that cards have. Nothing about the `card_*` names changed: a "card fee rule" is the fee rule of an acquirer, whatever the method.

## What accepts the method (migration 00071)

Every CHECK that lists the methods of a payment learned `QRIS`:

| Constraint | Table | Meaning |
|---|---|---|
| `payments_method_ck` | `payments` | the payment of a guest (and its refund) |
| `clr_method_ck` | `city_ledger_receipts` | the receipt of a company |
| `properties_refund_methods_ck` | `properties` | the methods a refund may leave by |
| `cfr_method_ck` | `card_fee_rules` | the fee rule (MDR, VAT on it, days to payout) of the method |
| `payments_mdr_ck`, `clr_mdr_ck` | `payments`, `city_ledger_receipts` | the fee snapshot a payment or receipt keeps |
| `card_settlements_key_ck` | `card_settlements` | the clearing account a settlement clears |
| `gl_account_map_key_ck` | `gl_account_map` | the system key `QRIS` |

What is paid OUT (supplier payments, tax payments) is not changed: QRIS is a way to receive money.

The default of a property for refunds stays CASH only. A property that wants refunds by QRIS ticks it in its settings (`refund_methods`).

## Accounting

- **The key `QRIS`** points at its own clearing account, **1155** "QRIS clearing" (id: "Akun penampung QRIS"), an ASSET under 1100, beside the card (1150) and the e-wallet and other (1160) clearing accounts. It is a key of its own because the money of QRIS comes in a settlement with its own MDR, and what is still to come must be readable per source: mixed with e-wallets and vouchers it is not.
- `methodKey("QRIS")` is `QRIS`, so the day-close journal of a QRIS payment debits that account; the key is one of `dayCloseKeys` (the night audit is not ready when it is not mapped, or points at an account that is not an active asset that takes postings) and of `mapKeys` (the mapping screen shows it, for an ASSET account).
- **A new property** gets the account from `usali_chart()` (and its Indonesian name from `usali_chart_names('id')`) and the key from `SeedRetainedEarningsMap`.
- **A property that exists** gets them from migration 00071. The code 1155 may already be the user's own, so for each property that has a chart:
  - a property that already has the key QRIS is left as it is;
  - with no account 1155, the account 1155 is created and mapped;
  - with an account 1155 already, it is neither used nor overwritten: the next free code from 1156 to 1199 is used (the NOTICE of the migration says how many properties took a replacement code).
  The name is Indonesian when the account 1150 of the property has its standard Indonesian name, else English.
- **Down** is refused (with the count) while a payment, a receipt, a fee rule, a settlement or a refund setting holds QRIS. Otherwise the key goes, and the account it pointed at goes too when it is still what the migration or the seed made (its standard name) and nothing uses it (no journal line, no other key, no child, no reference); an account its user changed or used stays as an ordinary account. The account is found through the key, not through its code.

## Fee rules and settlement (`internal/bankrec`)

The fee rules and the settlements work per clearing account, so QRIS needed no new table:

- `POST bank/card-fee-rules` takes `payment_method = QRIS` (MDR %, VAT % on the MDR, days to payout, from a date). A payment (or a company receipt) by QRIS keeps the rule of its day, the fee, the VAT on it and the expected payout date, as a card payment does (`folios.hasAcquirerFee`, `cityledger`).
- The settlement takes `account_key = QRIS` (`SettlementLines`, `ExpectedSettlements`, `SettlementPreview`, `Settle`): the bank statement line of the payout clears the payment lines of 1155; the journal debits the bank with what was paid, the fee account with the MDR (and the VAT, by the treatment of the property) and credits 1155 with the payments. The expected and the booked MDR and VAT are compared as for cards.

## Cashier

The shift and cashier reports group by method, so QRIS appears under its own name without a change of query: on the report of a shift it is another tender, not drawer cash (`OtherTenders`), and on the cashier report it is a line of its own. The PDF receipt says "QRIS". The screens offer it where a method is chosen: folio payment, cashier, deposit of a reservation, receipt of a company, the refund methods of a property, the fee rules and the settlement of Cards and Reconcile, and the mapping of the system accounts.

## Tests

`db/tests/schema_test.sql` (each CHECK accepts QRIS and still refuses what is not a method), `scripts/db-test.sh` with `migration_00071_*.sql` (a chart with no 1155, with a taken 1155 and 1156, with a taken 1155 and no 1100, no chart, running the data step again, Down refused while QRIS is in use, Down that keeps an account its user changed), and in Go `internal/bankrec/qris_test.go` (the day-close journal into 1155, the fee rule and the snapshot, the settlement with MDR and VAT, the unknown key, tenant isolation and permissions), `internal/folios` (payment and refund by QRIS), `internal/shifts`, `internal/reports`, `internal/accounting` (a new property, the readiness of the night audit) and `internal/app/qris_api_test.go`.
