-- +goose Up
-- The department of a folio item for the day close (design: docs/architecture/12-departments.md): the one snapshotted onto the item, or, for a reversal, that of the item it reverses.
CREATE OR REPLACE VIEW folio_item_gl AS
SELECT fi.tenant_id, fi.property_id, fi.id AS item_id, fi.folio_id, fi.business_date,
       CASE WHEN COALESCE(o.transaction_type, fi.transaction_type) IN ('PAYMENT', 'REFUND') THEN 'PAYMENT' ELSE 'CHARGE' END AS kind,
       fi.debit - fi.credit AS signed_amount,
       fi.net_amount,
       COALESCE(fi.revenue_account_code, o.revenue_account_code) AS revenue_account_code,
       COALESCE(cc.code, occ.code) AS charge_code,
       p.payment_method,
       (p.id IS NOT NULL AND rp.payment_method <> 'CITY_LEDGER'
        AND (st.id IS NULL OR rp.paid_at < st.actual_check_in_at)) AS is_deposit,
       p.id AS payment_id, p.payment_number, p.reference_number AS payment_reference,
       COALESCE(fi.department_id, o.department_id) AS department_id
  FROM folio_items fi
  LEFT JOIN folio_items o      ON o.property_id = fi.property_id AND o.id = fi.reverses_item_id
  LEFT JOIN charge_codes cc    ON cc.property_id = fi.property_id AND cc.id = fi.charge_code_id
  LEFT JOIN charge_codes occ   ON occ.property_id = o.property_id AND occ.id = o.charge_code_id
  LEFT JOIN payments p         ON p.property_id = fi.property_id AND p.id = COALESCE(fi.payment_id, o.payment_id)
  LEFT JOIN payments rp        ON rp.property_id = p.property_id AND rp.id = COALESCE(p.refund_of_payment_id, p.id)
  LEFT JOIN folios f           ON f.property_id = fi.property_id AND f.id = fi.folio_id
  LEFT JOIN stays st           ON st.property_id = f.property_id AND st.id = f.stay_id;

-- +goose Down
DROP VIEW IF EXISTS folio_item_gl;
CREATE VIEW folio_item_gl AS
SELECT fi.tenant_id, fi.property_id, fi.id AS item_id, fi.folio_id, fi.business_date,
       CASE WHEN COALESCE(o.transaction_type, fi.transaction_type) IN ('PAYMENT', 'REFUND') THEN 'PAYMENT' ELSE 'CHARGE' END AS kind,
       fi.debit - fi.credit AS signed_amount,
       fi.net_amount,
       COALESCE(fi.revenue_account_code, o.revenue_account_code) AS revenue_account_code,
       COALESCE(cc.code, occ.code) AS charge_code,
       p.payment_method,
       (p.id IS NOT NULL AND rp.payment_method <> 'CITY_LEDGER'
        AND (st.id IS NULL OR rp.paid_at < st.actual_check_in_at)) AS is_deposit,
       p.id AS payment_id, p.payment_number, p.reference_number AS payment_reference
  FROM folio_items fi
  LEFT JOIN folio_items o      ON o.property_id = fi.property_id AND o.id = fi.reverses_item_id
  LEFT JOIN charge_codes cc    ON cc.property_id = fi.property_id AND cc.id = fi.charge_code_id
  LEFT JOIN charge_codes occ   ON occ.property_id = o.property_id AND occ.id = o.charge_code_id
  LEFT JOIN payments p         ON p.property_id = fi.property_id AND p.id = COALESCE(fi.payment_id, o.payment_id)
  LEFT JOIN payments rp        ON rp.property_id = p.property_id AND rp.id = COALESCE(p.refund_of_payment_id, p.id)
  LEFT JOIN folios f           ON f.property_id = fi.property_id AND f.id = fi.folio_id
  LEFT JOIN stays st           ON st.property_id = f.property_id AND st.id = f.stay_id;
