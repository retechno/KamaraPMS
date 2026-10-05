# 12. Departments and sub-departments as an accounting dimension

Status: **approved 2026-10-05 by the owner (decisions 1 to 12 below); the details marked "decided in the build" were chosen while writing this and can be changed.** Built in four steps, each committed
on its own: (1) the master and the columns (built), (2) posting: day close, manual journals, supplier bills (built), (3) the department report (built), (4) the budget by department and the drill-down (built).

## The decisions of the owner

1. A master of departments.
2. `parent_id`, at most two levels: Department, Sub-department.
3. A journal line has a `department_id`.
4. A folio or charge code may have a **default** department.
5. At posting the department is **snapshotted** onto the journal line.
6. A supplier bill line may have a `department_id`.
7. The budget is kept by fiscal year, month, account **and department**.
8. Budget against actual drills down to the sub-department.
9. `statement_group` stays the USALI/accounting classification. A department does not replace it.
10. A change of the master never changes history.
11. A department is not required on every line by default: an account says so (step 5: NONE, OPTIONAL, REQUIRED); bank, payable, tax control and balance sheet lines may be NULL unless their account requires one.
12. No more than two levels in the MVP.

## What it means (decided in the build)

- **Master.** `departments`: `code` (unique per property, never changes), `name`, `parent_id` (never changes: a trigger), `sort_order`, `is_active`. A trigger keeps the tree at two levels: a parent must itself have no parent.
  A department with sub-departments, or with any posting, budget line, charge code or bill line, cannot be deleted (foreign keys); it is switched off instead, which only stops new use. Because the parent never changes,
  rolling a sub-department up into its department gives the same answer today and next year: this is how decision 10 is kept for the hierarchy. A rename changes the label of old figures, as any master data does; the line
  keeps the id.
- **Seed.** Every property gets the USALI departments as top-level ones: Rooms, Food and beverage, Other operated departments, Administrative and general, Information and telecommunications, Sales and marketing,
  Property operations and maintenance, Utilities. Sub-departments (a restaurant, a bar, a spa) are the property's own. The charge codes of type ROOM, FOOD_BEVERAGE and SERVICE get the default department of Rooms,
  Food and beverage and Other operated departments; FEE and OTHER none.
- **Where a department sits.** `gl_journal_lines.department_id`, `folio_items.department_id` (the snapshot of the charge code's department when the item is posted: a payment has none), `charge_codes.department_id`
  (the default), `supplier_bill_lines.department_id`, `budget_lines.department_id`. All are composite foreign keys with the property. A line may name a department or a sub-department.
- **Posting.** The day close groups the revenue lines by account **and** department, so the journal of a day has a revenue line per account and department. Guest ledger, tax, payments and city ledger lines have none.
  A reversal copies the department of the line it reverses. A manual journal line and a supplier bill line name their department themselves (optional). The journals the system makes from a form where the user picks the other account carry an
  optional `department_id` too (see "Departments on system journals" in the README): a bank adjustment, the commission of a card settlement, a credit note line, a write-off, a cash pay-in or pay-out, the penalty of a tax payment and the cash over and short of a shift close (chosen when closing). What the system decides itself (the closing of a year, the tax lines of a credit note) carries none.
- **The department rule of an account (step 5, migration 00054).** `gl_accounts.department_requirement` is NONE, OPTIONAL (the default) or REQUIRED, and `default_department_id` is the department a line gets when none is named.
  It replaces the earlier "a department is not required on every line" for the accounts a property chooses.
  - NONE: the line has no department; a department named by a person is 422 `DEPARTMENT_NOT_ALLOWED` (field `...department_id`), one that comes from a charge code is dropped at the day close. No default on such an account.
  - OPTIONAL: a department may be named; the default, when there is one, fills what is left empty. Nothing is refused.
  - REQUIRED: every new line of the account has one, the one named or else the default; with neither the posting is refused (422 `DEPARTMENT_REQUIRED`, "account 4101 - Room revenue requires a department, but no department is configured for charge code ROOM"). A line is never posted with none as a warning.
  - **One gate for every journal.** `lineDepartment` (`accounting/deptrule.go`) is applied by the manual journal, by `Poster.Post` (payables, bank, credit notes, write-offs, cash movements, tax payments) and by the day close. `Poster.ResolveDepartment` lets a module do it
    earlier with the field name of its own form, and store what the line will carry (a supplier bill line keeps the resolved department). Reversals copy the original line and the closing entry of a year follows the balances, so neither is asked.
  - **Folio posting.** The department snapshotted on a folio item is the charge code's, else the default of its revenue account, and none when the account takes none. A charge whose revenue account is REQUIRED and finds neither is refused when it is posted
    (422 `DEPARTMENT_REQUIRED` on `charge_code_id`, with the account and the charge code in the message), so the night audit meets no such item. A reversal copies the department of the item.
  - **The day close** resolves each line it makes the same way (the default of the guest ledger, tax payable, payment and city ledger accounts fills their lines); a required department that is still missing stops the close with the account and what it came from, and the whole close rolls back.
  - **Configuration check.** `GET accounting/department-setup` lists, for every account with a rule other than OPTIONAL, what posts to it on its own (a charge code, a tax, a service charge, a system account) and has no department:
    ERROR `DEPARTMENT_MISSING` or `DEFAULT_SWITCHED_OFF`, and the WARNING `DEPARTMENT_IGNORED` (a charge code names a department but its account takes none). The same check refuses the change that would create such a gap:
    setting REQUIRED or removing a default on an account (409 `DEPARTMENT_SETUP_INCOMPLETE`), pointing a system key at one, saving a charge code, tax or service charge whose account would leave it without (422 `DEPARTMENT_REQUIRED` on `department_id` or `gl_account_code`).
    A department that is the default of an account cannot be switched off or deleted (409 `DEPARTMENT_IN_USE`).
  - **History.** A line keeps the department it was posted with; changing the rule or the default never rewrites it, and no report reads the rule.
  - Frontend: the account form has the requirement and the default department, the chart shows the rule, and the Departments screen shows the check.
- **Reports.** The department report adds up the revenue and the expenses by department (a department includes its sub-departments), with the departmental profit, and an Unassigned line for what has no
  department. Lines with a department on a balance sheet account are kept but not reported by it.
- **Budget.** A budget row is an account and a department (NULL allowed), twelve months. A figure of the same account may be given for several departments. Budget against actual shows each account with a row per
  department and sub-department under it, and a summary by department.
- **Permissions.** Reading is `accounting.view`, changing the master is `accounting.manage`: no new permission.
- **Locking.** The master is plain data (like the charge codes): a change takes the open business day in share mode and writes an audit entry. Posting does not lock a department; a department switched off at the
  very moment of a posting can take that one line.

## Not in the MVP

Allocation of a shared expense over departments; closing entries by department (a year closes each account in one line); a department on the income statement as a
filter other than through the department report.
