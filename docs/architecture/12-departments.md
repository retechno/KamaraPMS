# 12. Departments and sub-departments as an accounting dimension

Status: **approved 2026-10-05 by the owner (decisions 1 to 12 below); the details marked "decided in the build" were chosen while writing this and can be changed.** Built in four steps, each committed
on its own: (1) the master and the columns (built), (2) posting: day close, manual journals, supplier bills (built), (3) the department report (built), (4) the budget by department and the drill-down.

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
11. A department is not required on every line: bank, payable, tax control and balance sheet lines may be NULL.
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
  A reversal copies the department of the line it reverses. A manual journal line and a supplier bill line name their department themselves (optional). The journals the system makes elsewhere (cash over and short, bank
  charges, tax, credit notes, the closing of a year) carry none for now.
- **Reports.** The department report adds up the revenue and the expenses by department (a department includes its sub-departments), with the departmental profit, and an Unassigned line for what has no
  department. Lines with a department on a balance sheet account are kept but not reported by it.
- **Budget.** A budget row is an account and a department (NULL allowed), twelve months. A figure of the same account may be given for several departments. Budget against actual shows each account with a row per
  department and sub-department under it, and a summary by department.
- **Permissions.** Reading is `accounting.view`, changing the master is `accounting.manage`: no new permission.
- **Locking.** The master is plain data (like the charge codes): a change takes the open business day in share mode and writes an audit entry. Posting does not lock a department; a department switched off at the
  very moment of a posting can take that one line.

## Not in the MVP

A rule that makes the department required on an account; allocation of a shared expense over departments; departments on the lines of the bank, tax and credit note journals; a department on the income statement as a
filter other than through the department report.
