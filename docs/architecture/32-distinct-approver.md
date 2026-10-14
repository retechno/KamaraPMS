# 32. An approver who is not the person who acts (PLAN: nothing is built; waiting for the owner)

Status: **plan, no code**, a PR of its own. Asked by the owner on 2026-10-14 (review of PR 6). Decisions of the owner are marked **D**.

## 1. What exists

A correction carries `approval: { email, password }`: the approver's own credentials, checked by `iam.Service.VerifyApprovalFor` (`internal/iam/approval.go`), who must hold `correction.approve` (or another permission for the approval of a rate override or of a budget) at the property; a tenant administrator holds every permission. **The actor may approve their own correction** (`06-api.md` 14.1: "A separate approver is optional"); the entry of the audit carries `actor` and `approved_by`, equal when self-approved. There is no setting, per property or otherwise, and no constraint of the database (`approved_by` only has to be present). All approvals pass through the one function.

The call sites (25 files; each reads the function): **folios** (adjustment, reversal, void, refund, move of a charge, move of the posted room nights of a group, the transfers of part B, the adjustments of a night), **front desk** (the rate of a stay), **reservations** (a rate override, a stay that breaks a restriction, a free room), **city ledger** (credit notes, write-offs, void of an invoice, void of a receipt), **accounting** (manual journals and their reversal, the closing and reopening of a fiscal year), **bank reconciliation** (the adjustment of a line), **budget** (approval), **payables** (a bill, a credit note, a supplier payment), **tax filing and tax invoices** (credit, payment, settings, a tax invoice), and **cashier shifts** (a difference beyond `max_variance`). Some approvals ask for another permission than `correction.approve` (`reservation.override_rate_approve`, `budget.approve`).

## 2. The setting (D)

- A table `property_correction_settings (tenant_id, property_id PK, require_distinct_approver boolean NOT NULL DEFAULT false, updated_at, updated_by)`, a row for each property made by the migration (**next free migration at the time of the PR: 00078 if it comes first, 00079 after the plan of document 31**). The default is **false**: nothing changes until a property turns it on. It can be turned on and off at any time (unlike the release of deposits, nothing about the books depends on it); each change is audited (`property.correction_settings_changed`, old and new).
- Permission: `property.manage` (the settings of the property), the page of settings of the property. **Where:** one switch with a sentence ("The person who approves a correction must be someone other than the person who makes it").

## 3. The check, in one place (D)

In `VerifyApprovalFor`, after the password and the permission are good: when the property's setting is on **and the approver is the actor** (`user.ID == auth.Principal.ActorID()`):

- **the approver is a tenant administrator (D, the exception): the approval is accepted**, and the answer carries a marker so that the use case writes it in its audit entry: `self_approved_by_admin: true` (next to `actor` and `approved_by`). The `Approval` value gets a method `AuditFields()` that every use case adds to its entry, so that the marker is written the same way everywhere and a use case that forgets it fails a test (see 6).
- **anyone else: 403 `APPROVAL_SAME_USER`** ("the approver must be someone other than the person who makes the change"), counted nowhere as a failed password (the credentials were right). With the setting off nothing changes, and no marker is written.

The marker is meant to be **traced and filtered in the audit trail**: the audit list gets the filter `self_approved=true` (the entries whose data has `self_approved_by_admin`), on the page of the audit and in the export; the label of the action does not change.

## 4. The screens (D)

- **Settings of the property:** the switch, with a **warning when the property has only one approver other than the administrators** (fewer than two active users who hold `correction.approve` at the property, administrators not counted): "Only one person can approve corrections; they cannot approve their own, so a correction needs an administrator." The warning is a notice, not a block: the owner may have set it so.
- **The dialog of approval** (`ApprovalDialog`, shared by every correction): when the setting is on, a line says that the approver must be someone other than the person who makes the change, and it is filled with nothing (today it pre-fills the logged-in email: with the setting on it is left empty, except for an administrator, for whom it stays as it is and the line says an administrator may approve their own and it is recorded). The setting is read from the property (`GET /properties/{id}`, a field `require_distinct_approver`); the error `APPROVAL_SAME_USER` has its text in en and id.

## 5. API, errors, audit

`GET /properties/{id}` and its update carry `require_distinct_approver`; the audit list takes `self_approved`; the new code `APPROVAL_SAME_USER` (403); `approved_by`, `actor` and the marker in every entry that has an approval. The documents `06-api.md` 14.1 and the OpenAPI say that the actor may approve their own correction **unless the property says otherwise**.

## 6. Tests

For **each family of call sites listed in section 1** (a use case of each: folio adjustment, reversal, void, refund and move; front desk rate; reservation rate override and restriction; city ledger credit note, write-off and voids; journal and its reversal; fiscal year close and reopen; bank adjustment; budget approval; payables bill, credit and payment; tax filing and tax invoice; shift difference), with the setting off and on:

1. **Off:** the actor approves their own correction; no marker in the entry.
2. **On, the actor approves their own correction:** 403 `APPROVAL_SAME_USER`, nothing is written, no failed attempt counted.
3. **On, another user who holds the right approves:** accepted; `approved_by` is the other user; no marker.
4. **On, a tenant administrator approves their own:** accepted; the entry has `self_approved_by_admin: true`; the audit filter finds it.
5. **On, an administrator makes the change and another user approves:** accepted, no marker.
6. A structural test (as the guard of the sale paths) that every call of `VerifyApproval` and `VerifyApprovalFor` outside `iam` is followed by `AuditFields()` in the entry of its use case, so that a new path cannot skip the marker.
7. **The setting:** default false; changed and audited; the permission to change it; the count of approvers behind the warning (a property with one, with two, with an administrator only); the dialog and the screen in en and id.

## 7. Not in this plan

Two-person approval for large amounts (a threshold), a different rule for each kind of correction, and an approval queue (a request that waits for another person's decision): the setting says only "not the same person".
