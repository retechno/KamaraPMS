-- +goose Up
-- Chart of accounts of a property, laid out after the Uniform System of Accounts for the Lodging Industry (USALI): the
-- balance sheet accounts of a hotel, operating revenue by department (rooms, food and beverage, other operated
-- departments, rentals and other income, miscellaneous income), departmental expenses, undistributed operating expenses,
-- management fees and the non-operating items. `statement_group` is what the income statement and the balance sheet
-- add up. Header accounts only group (they take no postings). The codes are what charge codes, taxes and service
-- charges already carry in their `gl_account_code`.
CREATE TABLE gl_accounts (
    id               bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id        bigint       NOT NULL,
    property_id      bigint       NOT NULL,
    code             varchar(30)  NOT NULL,
    name             varchar(150) NOT NULL,
    account_type     varchar(9)   NOT NULL,
    normal_side      varchar(6)   NOT NULL,
    parent_id        bigint,
    is_postable      boolean      NOT NULL DEFAULT true,
    is_active        boolean      NOT NULL DEFAULT true,
    statement_group  varchar(30),
    description      varchar(300),
    created_at       timestamptz  NOT NULL DEFAULT now(),
    created_by       bigint REFERENCES users (id),
    updated_at       timestamptz  NOT NULL DEFAULT now(),
    updated_by       bigint REFERENCES users (id),
    CONSTRAINT gl_accounts_property_fk     FOREIGN KEY (tenant_id, property_id) REFERENCES properties (tenant_id, id),
    CONSTRAINT gl_accounts_parent_fk       FOREIGN KEY (property_id, parent_id) REFERENCES gl_accounts (property_id, id),
    CONSTRAINT gl_accounts_code_uk         UNIQUE (property_id, code),
    CONSTRAINT gl_accounts_property_id_uk  UNIQUE (property_id, id),
    CONSTRAINT gl_accounts_code_ck         CHECK (code ~ '^[A-Z0-9][A-Z0-9._:/-]{0,29}$'),
    CONSTRAINT gl_accounts_type_ck         CHECK (account_type IN ('ASSET', 'LIABILITY', 'EQUITY', 'REVENUE', 'EXPENSE')),
    CONSTRAINT gl_accounts_side_ck         CHECK (normal_side IN ('DEBIT', 'CREDIT')),
    CONSTRAINT gl_accounts_not_own_parent  CHECK (parent_id IS NULL OR parent_id <> id),
    CONSTRAINT gl_accounts_group_ck        CHECK (statement_group IS NULL OR statement_group IN ('CASH', 'RECEIVABLES', 'INVENTORIES', 'PREPAID', 'FIXED_ASSETS', 'OTHER_ASSETS', 'PAYABLES', 'ACCRUED', 'DEPOSITS', 'TAXES_PAYABLE', 'OTHER_CURRENT_LIABILITIES', 'LONG_TERM_DEBT', 'EQUITY', 'REV_ROOMS', 'REV_FB', 'REV_OOD', 'REV_RENTAL_OTHER', 'REV_MISC', 'EXP_ROOMS', 'EXP_FB', 'EXP_OOD', 'UND_AG', 'UND_IT', 'UND_SM', 'UND_POM', 'UND_UTIL', 'MGMT_FEES', 'NONOP', 'DEPRECIATION', 'INTEREST', 'INCOME_TAX', 'SUSPENSE'))
);
CREATE INDEX gl_accounts_parent_idx ON gl_accounts (property_id, parent_id) WHERE parent_id IS NOT NULL;
CREATE TRIGGER gl_accounts_set_updated_at BEFORE UPDATE ON gl_accounts
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- Accounting settings of a property. It is also the lock target of accounting (lock level 46): editing the chart, the
-- system accounts or a period takes it FOR UPDATE, posting a journal takes it FOR SHARE, so the chart cannot change under a
-- journal that is being posted. start_date is the first business date that is journalled.
CREATE TABLE accounting_settings (
    id                        bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id                 bigint      NOT NULL,
    property_id               bigint      NOT NULL,
    start_date                date        NOT NULL,
    fiscal_year_start_month   smallint    NOT NULL DEFAULT 1,
    created_at                timestamptz NOT NULL DEFAULT now(),
    updated_at                timestamptz NOT NULL DEFAULT now(),
    updated_by                bigint REFERENCES users (id),
    CONSTRAINT accounting_settings_property_fk FOREIGN KEY (tenant_id, property_id) REFERENCES properties (tenant_id, id),
    CONSTRAINT accounting_settings_property_uk UNIQUE (property_id),
    CONSTRAINT accounting_settings_month_ck    CHECK (fiscal_year_start_month BETWEEN 1 AND 12)
);
CREATE TRIGGER accounting_settings_set_updated_at BEFORE UPDATE ON accounting_settings
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- The accounts the system itself posts to. Every key always has an account (the seed gives each its default).
CREATE TABLE gl_account_map (
    tenant_id    bigint      NOT NULL,
    property_id  bigint      NOT NULL,
    map_key      varchar(20) NOT NULL,
    account_id   bigint      NOT NULL,
    updated_at   timestamptz NOT NULL DEFAULT now(),
    updated_by   bigint REFERENCES users (id),
    PRIMARY KEY (property_id, map_key),
    CONSTRAINT gl_account_map_property_fk FOREIGN KEY (tenant_id, property_id) REFERENCES properties (tenant_id, id),
    CONSTRAINT gl_account_map_account_fk  FOREIGN KEY (property_id, account_id) REFERENCES gl_accounts (property_id, id),
    CONSTRAINT gl_account_map_key_ck      CHECK (map_key IN ('CASH', 'CARD', 'BANK_TRANSFER', 'OTHER_PAYMENT', 'CITY_LEDGER', 'GUEST_LEDGER',
                                                             'ADVANCE_DEPOSITS', 'TAX_PAYABLE', 'SERVICE_PAYABLE', 'SUSPENSE'))
);
CREATE INDEX gl_account_map_account_idx ON gl_account_map (property_id, account_id);

-- The standard chart, one row per account. The single definition: the seed below and the application both use it.
-- +goose StatementBegin
CREATE FUNCTION usali_chart() RETURNS TABLE (code text, name text, account_type text, normal_side text, parent_code text, is_postable boolean, statement_group text)
LANGUAGE sql IMMUTABLE AS $$
    SELECT * FROM (VALUES
        ('1000', 'Assets', 'ASSET', 'DEBIT', NULL, false, NULL),
        ('1100', 'Cash and cash equivalents', 'ASSET', 'DEBIT', '1000', false, 'CASH'),
        ('1110', 'Cash on hand - front desk', 'ASSET', 'DEBIT', '1100', true, 'CASH'),
        ('1120', 'Cash on hand - petty cash', 'ASSET', 'DEBIT', '1100', true, 'CASH'),
        ('1130', 'Bank - operating account', 'ASSET', 'DEBIT', '1100', true, 'CASH'),
        ('1140', 'Bank - payroll account', 'ASSET', 'DEBIT', '1100', true, 'CASH'),
        ('1150', 'Credit card clearing', 'ASSET', 'DEBIT', '1100', true, 'CASH'),
        ('1160', 'Other payment clearing (e-wallet, vouchers)', 'ASSET', 'DEBIT', '1100', true, 'CASH'),
        ('1200', 'Receivables', 'ASSET', 'DEBIT', '1000', false, 'RECEIVABLES'),
        ('1210', 'Guest ledger (in-house guests)', 'ASSET', 'DEBIT', '1200', true, 'RECEIVABLES'),
        ('1220', 'City ledger - accounts receivable', 'ASSET', 'DEBIT', '1200', true, 'RECEIVABLES'),
        ('1230', 'Other receivables', 'ASSET', 'DEBIT', '1200', true, 'RECEIVABLES'),
        ('1240', 'Allowance for doubtful accounts', 'ASSET', 'CREDIT', '1200', true, 'RECEIVABLES'),
        ('1300', 'Inventories', 'ASSET', 'DEBIT', '1000', false, 'INVENTORIES'),
        ('1310', 'Food inventory', 'ASSET', 'DEBIT', '1300', true, 'INVENTORIES'),
        ('1320', 'Beverage inventory', 'ASSET', 'DEBIT', '1300', true, 'INVENTORIES'),
        ('1330', 'Operating supplies inventory', 'ASSET', 'DEBIT', '1300', true, 'INVENTORIES'),
        ('1340', 'Other inventories', 'ASSET', 'DEBIT', '1300', true, 'INVENTORIES'),
        ('1400', 'Prepaid expenses and other current assets', 'ASSET', 'DEBIT', '1000', false, 'PREPAID'),
        ('1410', 'Prepaid insurance', 'ASSET', 'DEBIT', '1400', true, 'PREPAID'),
        ('1420', 'Prepaid taxes', 'ASSET', 'DEBIT', '1400', true, 'PREPAID'),
        ('1430', 'Other prepaid expenses', 'ASSET', 'DEBIT', '1400', true, 'PREPAID'),
        ('1440', 'Security deposits paid', 'ASSET', 'DEBIT', '1400', true, 'PREPAID'),
        ('1500', 'Property and equipment', 'ASSET', 'DEBIT', '1000', false, 'FIXED_ASSETS'),
        ('1510', 'Land', 'ASSET', 'DEBIT', '1500', true, 'FIXED_ASSETS'),
        ('1520', 'Buildings', 'ASSET', 'DEBIT', '1500', true, 'FIXED_ASSETS'),
        ('1530', 'Furniture, fixtures and equipment', 'ASSET', 'DEBIT', '1500', true, 'FIXED_ASSETS'),
        ('1540', 'Operating equipment (china, glass, linen)', 'ASSET', 'DEBIT', '1500', true, 'FIXED_ASSETS'),
        ('1550', 'Vehicles', 'ASSET', 'DEBIT', '1500', true, 'FIXED_ASSETS'),
        ('1560', 'Construction in progress', 'ASSET', 'DEBIT', '1500', true, 'FIXED_ASSETS'),
        ('1590', 'Accumulated depreciation', 'ASSET', 'CREDIT', '1500', true, 'FIXED_ASSETS'),
        ('1600', 'Other assets', 'ASSET', 'DEBIT', '1000', false, 'OTHER_ASSETS'),
        ('1610', 'Pre-opening costs', 'ASSET', 'DEBIT', '1600', true, 'OTHER_ASSETS'),
        ('1620', 'Licences and intangible assets', 'ASSET', 'DEBIT', '1600', true, 'OTHER_ASSETS'),
        ('2000', 'Liabilities', 'LIABILITY', 'CREDIT', NULL, false, NULL),
        ('2100', 'Payables', 'LIABILITY', 'CREDIT', '2000', false, 'PAYABLES'),
        ('2110', 'Accounts payable - trade', 'LIABILITY', 'CREDIT', '2100', true, 'PAYABLES'),
        ('2120', 'Accounts payable - other', 'LIABILITY', 'CREDIT', '2100', true, 'PAYABLES'),
        ('2200', 'Accrued expenses', 'LIABILITY', 'CREDIT', '2000', false, 'ACCRUED'),
        ('2210', 'Accrued payroll', 'LIABILITY', 'CREDIT', '2200', true, 'ACCRUED'),
        ('2220', 'Accrued employee benefits', 'LIABILITY', 'CREDIT', '2200', true, 'ACCRUED'),
        ('2230', 'Accrued utilities', 'LIABILITY', 'CREDIT', '2200', true, 'ACCRUED'),
        ('2240', 'Accrued interest', 'LIABILITY', 'CREDIT', '2200', true, 'ACCRUED'),
        ('2250', 'Other accrued expenses', 'LIABILITY', 'CREDIT', '2200', true, 'ACCRUED'),
        ('2300', 'Guest deposits and unearned revenue', 'LIABILITY', 'CREDIT', '2000', false, 'DEPOSITS'),
        ('2310', 'Advance deposits', 'LIABILITY', 'CREDIT', '2300', true, 'DEPOSITS'),
        ('2320', 'Gift certificates and vouchers outstanding', 'LIABILITY', 'CREDIT', '2300', true, 'DEPOSITS'),
        ('2330', 'Unearned revenue (groups and events)', 'LIABILITY', 'CREDIT', '2300', true, 'DEPOSITS'),
        ('2400', 'Taxes and service charges payable', 'LIABILITY', 'CREDIT', '2000', false, 'TAXES_PAYABLE'),
        ('2410', 'Hotel and restaurant tax payable', 'LIABILITY', 'CREDIT', '2400', true, 'TAXES_PAYABLE'),
        ('2420', 'Value added tax payable', 'LIABILITY', 'CREDIT', '2400', true, 'TAXES_PAYABLE'),
        ('2430', 'Service charge payable', 'LIABILITY', 'CREDIT', '2400', true, 'TAXES_PAYABLE'),
        ('2440', 'Withholding tax payable', 'LIABILITY', 'CREDIT', '2400', true, 'TAXES_PAYABLE'),
        ('2450', 'Income tax payable', 'LIABILITY', 'CREDIT', '2400', true, 'TAXES_PAYABLE'),
        ('2460', 'Other taxes payable', 'LIABILITY', 'CREDIT', '2400', true, 'TAXES_PAYABLE'),
        ('2500', 'Other current liabilities', 'LIABILITY', 'CREDIT', '2000', false, 'OTHER_CURRENT_LIABILITIES'),
        ('2510', 'Current portion of long-term debt', 'LIABILITY', 'CREDIT', '2500', true, 'OTHER_CURRENT_LIABILITIES'),
        ('2520', 'Due to owners and management company', 'LIABILITY', 'CREDIT', '2500', true, 'OTHER_CURRENT_LIABILITIES'),
        ('2530', 'Other current liabilities', 'LIABILITY', 'CREDIT', '2500', true, 'OTHER_CURRENT_LIABILITIES'),
        ('2990', 'Suspense - unmapped items', 'LIABILITY', 'CREDIT', '2500', true, 'SUSPENSE'),
        ('2600', 'Long-term liabilities', 'LIABILITY', 'CREDIT', '2000', false, 'LONG_TERM_DEBT'),
        ('2610', 'Bank loans', 'LIABILITY', 'CREDIT', '2600', true, 'LONG_TERM_DEBT'),
        ('2620', 'Shareholder loans', 'LIABILITY', 'CREDIT', '2600', true, 'LONG_TERM_DEBT'),
        ('2630', 'Other long-term liabilities', 'LIABILITY', 'CREDIT', '2600', true, 'LONG_TERM_DEBT'),
        ('3000', 'Equity', 'EQUITY', 'CREDIT', NULL, false, NULL),
        ('3100', 'Paid-in capital', 'EQUITY', 'CREDIT', '3000', true, 'EQUITY'),
        ('3200', 'Retained earnings (prior years)', 'EQUITY', 'CREDIT', '3000', true, 'EQUITY'),
        ('3300', 'Owner drawings and dividends', 'EQUITY', 'DEBIT', '3000', true, 'EQUITY'),
        ('3900', 'Opening balance equity', 'EQUITY', 'CREDIT', '3000', true, 'EQUITY'),
        ('4000', 'Operating revenue', 'REVENUE', 'CREDIT', NULL, false, NULL),
        ('4100', 'Rooms revenue', 'REVENUE', 'CREDIT', '4000', false, 'REV_ROOMS'),
        ('4110', 'Room revenue - transient', 'REVENUE', 'CREDIT', '4100', true, 'REV_ROOMS'),
        ('4120', 'Room revenue - group', 'REVENUE', 'CREDIT', '4100', true, 'REV_ROOMS'),
        ('4130', 'Room revenue - corporate and contract', 'REVENUE', 'CREDIT', '4100', true, 'REV_ROOMS'),
        ('4140', 'Room revenue - packages', 'REVENUE', 'CREDIT', '4100', true, 'REV_ROOMS'),
        ('4150', 'Rooms - other revenue (extra bed, early check-in, late check-out)', 'REVENUE', 'CREDIT', '4100', true, 'REV_ROOMS'),
        ('4160', 'Rooms - allowances and rebates', 'REVENUE', 'DEBIT', '4100', true, 'REV_ROOMS'),
        ('4200', 'Food and beverage revenue', 'REVENUE', 'CREDIT', '4000', false, 'REV_FB'),
        ('4210', 'Food revenue', 'REVENUE', 'CREDIT', '4200', true, 'REV_FB'),
        ('4220', 'Beverage revenue', 'REVENUE', 'CREDIT', '4200', true, 'REV_FB'),
        ('4230', 'Minibar revenue', 'REVENUE', 'CREDIT', '4200', true, 'REV_FB'),
        ('4240', 'Banquet and meeting F&B revenue', 'REVENUE', 'CREDIT', '4200', true, 'REV_FB'),
        ('4250', 'Room service revenue', 'REVENUE', 'CREDIT', '4200', true, 'REV_FB'),
        ('4260', 'F&B - other revenue', 'REVENUE', 'CREDIT', '4200', true, 'REV_FB'),
        ('4290', 'F&B - allowances', 'REVENUE', 'DEBIT', '4200', true, 'REV_FB'),
        ('4300', 'Other operated departments', 'REVENUE', 'CREDIT', '4000', false, 'REV_OOD'),
        ('4310', 'Spa and wellness', 'REVENUE', 'CREDIT', '4300', true, 'REV_OOD'),
        ('4320', 'Telecommunications and internet', 'REVENUE', 'CREDIT', '4300', true, 'REV_OOD'),
        ('4330', 'Parking', 'REVENUE', 'CREDIT', '4300', true, 'REV_OOD'),
        ('4340', 'Recreation and leisure', 'REVENUE', 'CREDIT', '4300', true, 'REV_OOD'),
        ('4350', 'Laundry and valet', 'REVENUE', 'CREDIT', '4300', true, 'REV_OOD'),
        ('4360', 'Transportation', 'REVENUE', 'CREDIT', '4300', true, 'REV_OOD'),
        ('4370', 'Business centre', 'REVENUE', 'CREDIT', '4300', true, 'REV_OOD'),
        ('4390', 'Other operated departments - allowances', 'REVENUE', 'DEBIT', '4300', true, 'REV_OOD'),
        ('4400', 'Rentals and other income', 'REVENUE', 'CREDIT', '4000', false, 'REV_RENTAL_OTHER'),
        ('4410', 'Meeting room rental', 'REVENUE', 'CREDIT', '4400', true, 'REV_RENTAL_OTHER'),
        ('4420', 'Shop and space rental', 'REVENUE', 'CREDIT', '4400', true, 'REV_RENTAL_OTHER'),
        ('4430', 'Commissions earned', 'REVENUE', 'CREDIT', '4400', true, 'REV_RENTAL_OTHER'),
        ('4440', 'Concessions', 'REVENUE', 'CREDIT', '4400', true, 'REV_RENTAL_OTHER'),
        ('4500', 'Miscellaneous income', 'REVENUE', 'CREDIT', '4000', false, 'REV_MISC'),
        ('4510', 'No-show and cancellation fees', 'REVENUE', 'CREDIT', '4500', true, 'REV_MISC'),
        ('4520', 'Forfeited deposits', 'REVENUE', 'CREDIT', '4500', true, 'REV_MISC'),
        ('4530', 'Service charge income retained', 'REVENUE', 'CREDIT', '4500', true, 'REV_MISC'),
        ('4540', 'Foreign exchange gain', 'REVENUE', 'CREDIT', '4500', true, 'REV_MISC'),
        ('4590', 'Other miscellaneous income', 'REVENUE', 'CREDIT', '4500', true, 'REV_MISC'),
        ('5000', 'Departmental expenses', 'EXPENSE', 'DEBIT', NULL, false, NULL),
        ('5100', 'Rooms department expenses', 'EXPENSE', 'DEBIT', '5000', false, 'EXP_ROOMS'),
        ('5110', 'Salaries and wages', 'EXPENSE', 'DEBIT', '5100', true, 'EXP_ROOMS'),
        ('5120', 'Employee benefits', 'EXPENSE', 'DEBIT', '5100', true, 'EXP_ROOMS'),
        ('5130', 'Laundry and linen', 'EXPENSE', 'DEBIT', '5100', true, 'EXP_ROOMS'),
        ('5140', 'Cleaning supplies', 'EXPENSE', 'DEBIT', '5100', true, 'EXP_ROOMS'),
        ('5150', 'Guest supplies', 'EXPENSE', 'DEBIT', '5100', true, 'EXP_ROOMS'),
        ('5160', 'Commissions (travel agents and OTAs)', 'EXPENSE', 'DEBIT', '5100', true, 'EXP_ROOMS'),
        ('5170', 'Contract services', 'EXPENSE', 'DEBIT', '5100', true, 'EXP_ROOMS'),
        ('5180', 'Reservation expenses (GDS, channel manager)', 'EXPENSE', 'DEBIT', '5100', true, 'EXP_ROOMS'),
        ('5190', 'Other rooms expenses', 'EXPENSE', 'DEBIT', '5100', true, 'EXP_ROOMS'),
        ('5200', 'Food and beverage department expenses', 'EXPENSE', 'DEBIT', '5000', false, 'EXP_FB'),
        ('5210', 'Cost of food sales', 'EXPENSE', 'DEBIT', '5200', true, 'EXP_FB'),
        ('5220', 'Cost of beverage sales', 'EXPENSE', 'DEBIT', '5200', true, 'EXP_FB'),
        ('5230', 'Salaries and wages', 'EXPENSE', 'DEBIT', '5200', true, 'EXP_FB'),
        ('5240', 'Employee benefits', 'EXPENSE', 'DEBIT', '5200', true, 'EXP_FB'),
        ('5250', 'China, glassware, silver and linen', 'EXPENSE', 'DEBIT', '5200', true, 'EXP_FB'),
        ('5260', 'Kitchen fuel', 'EXPENSE', 'DEBIT', '5200', true, 'EXP_FB'),
        ('5270', 'Music and entertainment', 'EXPENSE', 'DEBIT', '5200', true, 'EXP_FB'),
        ('5280', 'Other F&B expenses', 'EXPENSE', 'DEBIT', '5200', true, 'EXP_FB'),
        ('5300', 'Other operated departments expenses', 'EXPENSE', 'DEBIT', '5000', false, 'EXP_OOD'),
        ('5310', 'Cost of sales', 'EXPENSE', 'DEBIT', '5300', true, 'EXP_OOD'),
        ('5320', 'Salaries and wages', 'EXPENSE', 'DEBIT', '5300', true, 'EXP_OOD'),
        ('5330', 'Employee benefits', 'EXPENSE', 'DEBIT', '5300', true, 'EXP_OOD'),
        ('5340', 'Other expenses', 'EXPENSE', 'DEBIT', '5300', true, 'EXP_OOD'),
        ('6000', 'Undistributed operating expenses', 'EXPENSE', 'DEBIT', NULL, false, NULL),
        ('6100', 'Administrative and general', 'EXPENSE', 'DEBIT', '6000', false, 'UND_AG'),
        ('6110', 'Salaries and wages', 'EXPENSE', 'DEBIT', '6100', true, 'UND_AG'),
        ('6120', 'Employee benefits', 'EXPENSE', 'DEBIT', '6100', true, 'UND_AG'),
        ('6130', 'Credit card commissions', 'EXPENSE', 'DEBIT', '6100', true, 'UND_AG'),
        ('6140', 'Bad debt expense', 'EXPENSE', 'DEBIT', '6100', true, 'UND_AG'),
        ('6150', 'Professional fees', 'EXPENSE', 'DEBIT', '6100', true, 'UND_AG'),
        ('6160', 'Office supplies and printing', 'EXPENSE', 'DEBIT', '6100', true, 'UND_AG'),
        ('6170', 'Travel and entertainment', 'EXPENSE', 'DEBIT', '6100', true, 'UND_AG'),
        ('6180', 'Licences and permits', 'EXPENSE', 'DEBIT', '6100', true, 'UND_AG'),
        ('6190', 'Other administrative and general', 'EXPENSE', 'DEBIT', '6100', true, 'UND_AG'),
        ('6200', 'Information and telecommunications systems', 'EXPENSE', 'DEBIT', '6000', false, 'UND_IT'),
        ('6210', 'Salaries and wages', 'EXPENSE', 'DEBIT', '6200', true, 'UND_IT'),
        ('6220', 'Software and licences', 'EXPENSE', 'DEBIT', '6200', true, 'UND_IT'),
        ('6230', 'Hardware and support', 'EXPENSE', 'DEBIT', '6200', true, 'UND_IT'),
        ('6240', 'Telecom lines and internet', 'EXPENSE', 'DEBIT', '6200', true, 'UND_IT'),
        ('6300', 'Sales and marketing', 'EXPENSE', 'DEBIT', '6000', false, 'UND_SM'),
        ('6310', 'Salaries and wages', 'EXPENSE', 'DEBIT', '6300', true, 'UND_SM'),
        ('6320', 'Employee benefits', 'EXPENSE', 'DEBIT', '6300', true, 'UND_SM'),
        ('6330', 'Advertising', 'EXPENSE', 'DEBIT', '6300', true, 'UND_SM'),
        ('6340', 'Promotions and loyalty programme', 'EXPENSE', 'DEBIT', '6300', true, 'UND_SM'),
        ('6350', 'Franchise marketing fees', 'EXPENSE', 'DEBIT', '6300', true, 'UND_SM'),
        ('6360', 'Online and digital marketing', 'EXPENSE', 'DEBIT', '6300', true, 'UND_SM'),
        ('6390', 'Other sales and marketing', 'EXPENSE', 'DEBIT', '6300', true, 'UND_SM'),
        ('6400', 'Property operations and maintenance', 'EXPENSE', 'DEBIT', '6000', false, 'UND_POM'),
        ('6410', 'Salaries and wages', 'EXPENSE', 'DEBIT', '6400', true, 'UND_POM'),
        ('6420', 'Employee benefits', 'EXPENSE', 'DEBIT', '6400', true, 'UND_POM'),
        ('6430', 'Repairs and maintenance - building', 'EXPENSE', 'DEBIT', '6400', true, 'UND_POM'),
        ('6440', 'Repairs and maintenance - equipment', 'EXPENSE', 'DEBIT', '6400', true, 'UND_POM'),
        ('6450', 'Grounds and landscaping', 'EXPENSE', 'DEBIT', '6400', true, 'UND_POM'),
        ('6460', 'Pest control and waste removal', 'EXPENSE', 'DEBIT', '6400', true, 'UND_POM'),
        ('6470', 'Maintenance supplies', 'EXPENSE', 'DEBIT', '6400', true, 'UND_POM'),
        ('6480', 'Contract services', 'EXPENSE', 'DEBIT', '6400', true, 'UND_POM'),
        ('6500', 'Utilities', 'EXPENSE', 'DEBIT', '6000', false, 'UND_UTIL'),
        ('6510', 'Electricity', 'EXPENSE', 'DEBIT', '6500', true, 'UND_UTIL'),
        ('6520', 'Water and sewage', 'EXPENSE', 'DEBIT', '6500', true, 'UND_UTIL'),
        ('6530', 'Gas and fuel', 'EXPENSE', 'DEBIT', '6500', true, 'UND_UTIL'),
        ('6540', 'Other utilities', 'EXPENSE', 'DEBIT', '6500', true, 'UND_UTIL'),
        ('6700', 'Management and franchise fees', 'EXPENSE', 'DEBIT', '6000', false, 'MGMT_FEES'),
        ('6710', 'Base management fee', 'EXPENSE', 'DEBIT', '6700', true, 'MGMT_FEES'),
        ('6720', 'Incentive management fee', 'EXPENSE', 'DEBIT', '6700', true, 'MGMT_FEES'),
        ('6730', 'Franchise royalty fee', 'EXPENSE', 'DEBIT', '6700', true, 'MGMT_FEES'),
        ('7000', 'Non-operating income and expenses', 'EXPENSE', 'DEBIT', NULL, false, NULL),
        ('7100', 'Rent and leases', 'EXPENSE', 'DEBIT', '7000', false, 'NONOP'),
        ('7110', 'Land and building rent', 'EXPENSE', 'DEBIT', '7100', true, 'NONOP'),
        ('7120', 'Equipment rent', 'EXPENSE', 'DEBIT', '7100', true, 'NONOP'),
        ('7200', 'Property and other taxes', 'EXPENSE', 'DEBIT', '7000', false, 'NONOP'),
        ('7210', 'Property tax', 'EXPENSE', 'DEBIT', '7200', true, 'NONOP'),
        ('7220', 'Other taxes (not on income)', 'EXPENSE', 'DEBIT', '7200', true, 'NONOP'),
        ('7300', 'Insurance', 'EXPENSE', 'DEBIT', '7000', false, 'NONOP'),
        ('7310', 'Property insurance', 'EXPENSE', 'DEBIT', '7300', true, 'NONOP'),
        ('7320', 'Liability and other insurance', 'EXPENSE', 'DEBIT', '7300', true, 'NONOP'),
        ('7400', 'Other non-operating items', 'EXPENSE', 'DEBIT', '7000', false, 'NONOP'),
        ('7410', 'Other non-operating expense', 'EXPENSE', 'DEBIT', '7400', true, 'NONOP'),
        ('7420', 'Gain or loss on disposal of assets', 'EXPENSE', 'DEBIT', '7400', true, 'NONOP'),
        ('7430', 'Other non-operating income', 'EXPENSE', 'CREDIT', '7400', true, 'NONOP'),
        ('7500', 'Depreciation and amortization', 'EXPENSE', 'DEBIT', '7000', false, 'DEPRECIATION'),
        ('7510', 'Depreciation - buildings', 'EXPENSE', 'DEBIT', '7500', true, 'DEPRECIATION'),
        ('7520', 'Depreciation - furniture, fixtures and equipment', 'EXPENSE', 'DEBIT', '7500', true, 'DEPRECIATION'),
        ('7530', 'Amortization', 'EXPENSE', 'DEBIT', '7500', true, 'DEPRECIATION'),
        ('7600', 'Interest', 'EXPENSE', 'DEBIT', '7000', false, 'INTEREST'),
        ('7610', 'Interest expense - bank loans', 'EXPENSE', 'DEBIT', '7600', true, 'INTEREST'),
        ('7620', 'Interest expense - other', 'EXPENSE', 'DEBIT', '7600', true, 'INTEREST'),
        ('7630', 'Interest income', 'EXPENSE', 'CREDIT', '7600', true, 'INTEREST'),
        ('7900', 'Income taxes', 'EXPENSE', 'DEBIT', '7000', false, 'INCOME_TAX'),
        ('7910', 'Current income tax', 'EXPENSE', 'DEBIT', '7900', true, 'INCOME_TAX'),
        ('7920', 'Deferred income tax', 'EXPENSE', 'DEBIT', '7900', true, 'INCOME_TAX')
    ) AS c (code, name, account_type, normal_side, parent_code, is_postable, statement_group)
$$;
-- +goose StatementEnd

-- Seeds the standard chart, the accounts the system posts to, and the revenue accounts of the standard charge codes
-- that have none yet. Idempotent: it never overwrites what a property already has.
-- +goose StatementBegin
CREATE FUNCTION seed_chart_of_accounts(p_tenant_id bigint, p_property_id bigint, p_actor_id bigint, p_start_date date) RETURNS integer
LANGUAGE plpgsql AS $$
DECLARE
    v_inserted integer;
BEGIN
    INSERT INTO gl_accounts (tenant_id, property_id, code, name, account_type, normal_side, is_postable, statement_group, created_by, updated_by)
    SELECT p_tenant_id, p_property_id, c.code, c.name, c.account_type, c.normal_side, c.is_postable, c.statement_group, p_actor_id, p_actor_id
      FROM usali_chart() c
    ON CONFLICT (property_id, code) DO NOTHING;
    GET DIAGNOSTICS v_inserted = ROW_COUNT;

    UPDATE gl_accounts a SET parent_id = p.id
      FROM usali_chart() c
      JOIN gl_accounts p ON p.property_id = p_property_id AND p.code = c.parent_code
     WHERE a.property_id = p_property_id AND a.code = c.code AND a.parent_id IS NULL;

    INSERT INTO gl_account_map (tenant_id, property_id, map_key, account_id, updated_by)
    SELECT p_tenant_id, p_property_id, m.map_key, a.id, p_actor_id
      FROM (VALUES ('CASH', '1110'), ('CARD', '1150'), ('BANK_TRANSFER', '1130'), ('OTHER_PAYMENT', '1160'), ('CITY_LEDGER', '1220'), ('GUEST_LEDGER', '1210'), ('ADVANCE_DEPOSITS', '2310'), ('TAX_PAYABLE', '2410'), ('SERVICE_PAYABLE', '2430'), ('SUSPENSE', '2990')) AS m (map_key, code)
      JOIN gl_accounts a ON a.property_id = p_property_id AND a.code = m.code
    ON CONFLICT (property_id, map_key) DO NOTHING;

    INSERT INTO accounting_settings (tenant_id, property_id, start_date)
    VALUES (p_tenant_id, p_property_id, COALESCE(p_start_date, CURRENT_DATE))
    ON CONFLICT (property_id) DO NOTHING;

    UPDATE charge_codes cc SET gl_account_code = d.code
      FROM (VALUES ('ROOM', '4110'), ('ROOM_EXEMPT', '4110'), ('BREAKFAST', '4210'), ('RESTAURANT', '4210'), ('LAUNDRY', '4350'), ('MINIBAR', '4230'), ('EXTRA_BED', '4150'), ('NO_SHOW_FEE', '4510'), ('CANCEL_FEE', '4510'), ('OTHER', '4590')) AS d (charge_code, code)
     WHERE cc.property_id = p_property_id AND cc.code = d.charge_code AND cc.gl_account_code IS NULL;
    RETURN v_inserted;
END
$$;
-- +goose StatementEnd

SELECT seed_chart_of_accounts(p.tenant_id, p.id, NULL, (SELECT min(b.business_date) FROM business_days b WHERE b.property_id = p.id)) FROM properties p;

-- +goose Down
DROP FUNCTION IF EXISTS seed_chart_of_accounts(bigint, bigint, bigint, date);
DROP FUNCTION IF EXISTS usali_chart();
DROP TABLE IF EXISTS gl_account_map;
DROP TABLE IF EXISTS accounting_settings;
DROP TABLE IF EXISTS gl_accounts;
