-- +goose Up
-- Reports, PR 1 (docs/architecture/38-reports-night-audit-pack.md, 8.1 and 10): the audit log by business date and action.
--
-- The daily flash report counts reservations made, cancellations, no-shows, check-ins of walk-ins and check-outs for a range of business dates, and the cancellations report (PR 1 too) lists
-- them: the business date of a cancellation or a no-show lives only in audit_logs.business_date (38 F5). The index of the table was by (property_id, created_at), so every one of those
-- questions read the whole table. With two million rows the same count for one day took a full scan of 26,000 pages; with this index it reads under 2,000.
CREATE INDEX audit_logs_bd_action_idx ON audit_logs (property_id, business_date, action) WHERE property_id IS NOT NULL;

-- +goose Down
DROP INDEX audit_logs_bd_action_idx;
