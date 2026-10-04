# 08. Next features

Ideas that were left out on purpose, with why and what they would touch. A request starts the work (see `README.md`);
this list only keeps them from being forgotten. Move an item to the README status when it is built.

## Rooms and availability

- **Bed counts per room type** (for example 1 King or 2 Twin in one room type) and **bed type as a sellable variant** with
  its own stock or rate. Today a bed type is a description: availability is counted per room type, and a room with
  another bed than the one asked for can still be assigned (see "Bed types" in the README). Touches the availability
  engine (inventory per type and bed), the search, the rate grid and the calendar. Decide first whether a variant
  has its own price or only its own stock.

## Finance (recommended order, 2026-10-03; item 1 started: PKP settings built, see 09-pkp-input-vat.md)

What exists: folios, payments and corrections with approval, the cashier report, the city ledger with invoices and
their payments, the chart of accounts, journals and periods, fiscal years, the statements and the control accounts,
accounts payable, bank reconciliation and monthly tax filing. What is missing, most useful first:

1. **Input VAT and tax invoices.** Tax on purchases (input VAT) and its offset against the output tax (the filing only
   sees the output side today), and a tax invoice (faktur pajak, e-Faktur export) for VAT customers. Open question for
   the owner: does the hotel issue tax invoices (PKP), or only record input VAT and reconcile it?
2. **Credit notes and write-offs.** An issued city ledger invoice can only be voided: add customer and supplier credit
   notes (journalled), write-off of bad debt with approval, and a list of overdue invoices for reminders (dunning,
   interest). **Customer side done** (credit notes, write-offs, overdue list, reminders, interest shown); the supplier credit notes remain.
3. **Cashier shifts and the cash drawer.** (**Built**, see README; the Z-report PDF and a shift handover screen remain.) Open and close a shift, opening float, drops and pay-ins, the counted cash,
   the over and short journalled, and the hand-over between shifts.
4. **Budget and cash flow.** (**Built**, see README, both methods.) A budget per account and month, actual against budget by USALI department, and a cash
   flow statement.
5. **Card settlement.** (**Built** except the VAT on the commission, see README.) Match the bank deposit of the card machine or gateway with the card payments, with the merchant
   fee (MDR) as its own line; bank reconciliation matches line by line today.
6. **Later:** fixed assets and depreciation, purchase orders and goods receipt, withholding tax on payables, MT940/OFX
   bank statements, a master folio for a group, e-mailing invoices and receipts.

## Language

- **Numbers in the Indonesian field errors.** The generic texts leave out the limits the server hint carries ("at
  most 100 characters"). Needs interpolation per error code from the backend.
- **The confirmation e-mail** is sent by the server in English; it would take a language per guest or per property.
