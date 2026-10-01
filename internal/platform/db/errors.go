package db

import (
	"errors"

	"github.com/jackc/pgx/v5/pgconn"

	"kamarapms/internal/platform/apperr"
)

type mapped struct {
	kind    apperr.Kind
	code    string
	message string
}

// constraintErrors maps constraint names (including names raised by triggers via
// RAISE ... USING CONSTRAINT) to stable API errors. It is the single place where
// the schema's guarantees are translated into the API vocabulary.
// TestConstraintMapMatchesSchema fails if a name here does not exist in the schema.
var constraintErrors = map[string]mapped{
	// Identity / codes
	"tenants_code_uk":                  {apperr.KindConflict, "CODE_TAKEN", "the tenant code is already in use"},
	"users_tenant_email_uk":            {apperr.KindConflict, "EMAIL_TAKEN", "a user with this email already exists"},
	"roles_tenant_name_uk":             {apperr.KindConflict, "NAME_TAKEN", "a role with this name already exists"},
	"properties_tenant_code_uk":        {apperr.KindConflict, "CODE_TAKEN", "the property code is already in use"},
	"room_types_property_code_uk":      {apperr.KindConflict, "CODE_TAKEN", "the room type code is already in use"},
	"rooms_property_number_uk":         {apperr.KindConflict, "ROOM_NUMBER_TAKEN", "the room number is already in use"},
	"guests_tenant_code_uk":            {apperr.KindConflict, "CODE_TAKEN", "the guest code is already in use"},
	"taxes_property_code_uk":           {apperr.KindConflict, "CODE_TAKEN", "the tax code is already in use"},
	"service_charges_property_code_uk": {apperr.KindConflict, "CODE_TAKEN", "the service charge code is already in use"},
	"charge_codes_property_code_uk":    {apperr.KindConflict, "CODE_TAKEN", "the charge code is already in use"},
	"rate_plans_property_code_uk":      {apperr.KindConflict, "CODE_TAKEN", "the rate plan code is already in use"},
	"reservations_confirmation_uk":     {apperr.KindConflict, "NUMBER_TAKEN", "the confirmation number is already in use"},

	// Access grants (composite FKs keep grants inside the tenant)
	"user_properties_property_fk": {apperr.KindInvalid, "PROPERTY_NOT_FOUND", "a granted property does not exist in this tenant"},
	"user_properties_role_fk":     {apperr.KindInvalid, "ROLE_NOT_FOUND", "a granted role does not exist in this tenant"},
	"user_properties_pkey":        {apperr.KindInvalid, "DUPLICATE_PROPERTY_GRANT", "a user can have only one role per property"},

	// Business days
	"business_days_one_open_uk":        {apperr.KindConflict, "BUSINESS_DAY_ALREADY_OPEN", "the property already has an open business day"},
	"business_days_consecutive":        {apperr.KindConflict, "BUSINESS_DAY_NOT_CONSECUTIVE", "business days must be consecutive"},
	"business_days_closed_immutable":   {apperr.KindConflict, "BUSINESS_DAY_CLOSED", "the business day is closed"},
	"business_days_property_date_uk":   {apperr.KindConflict, "BUSINESS_DAY_EXISTS", "the business day already exists"},
	"business_days_identity_immutable": {apperr.KindConflict, "RECORD_IMMUTABLE", "the business day cannot be changed"},
	"business_days_no_delete":          {apperr.KindConflict, "RECORD_IMMUTABLE", "business days cannot be deleted"},

	// Configuration
	"charge_code_taxes_pair_uk":               {apperr.KindConflict, "RULE_ALREADY_MAPPED", "the tax is already mapped to this charge code"},
	"charge_code_taxes_sequence_uk":           {apperr.KindConflict, "SEQUENCE_TAKEN", "another active tax already uses this sequence"},
	"charge_code_service_charges_pair_uk":     {apperr.KindConflict, "RULE_ALREADY_MAPPED", "the service charge is already mapped to this charge code"},
	"charge_code_service_charges_sequence_uk": {apperr.KindConflict, "SEQUENCE_TAKEN", "another active service charge already uses this sequence"},
	"room_charge_code_type":                   {apperr.KindInvalid, "CHARGE_CODE_NOT_ROOM", "room revenue must use a charge code of type ROOM"},
	"properties_currency_lock":                {apperr.KindConflict, "CURRENCY_LOCKED", "the property currency cannot change once financial transactions exist"},
	"charge_codes_price_mode_lock":            {apperr.KindConflict, "PRICE_MODE_LOCKED", "the price mode of a charge code in use cannot change"},
	"charge_codes_charge_type_lock":           {apperr.KindConflict, "CHARGE_TYPE_LOCKED", "the charge type of a room revenue code cannot change"},

	// Inventory and occupancy
	"room_blocks_no_overlap_ex":       {apperr.KindConflict, "ROOM_BLOCK_CONFLICT", "the room already has an active block for these dates"},
	"reservation_rooms_no_overlap_ex": {apperr.KindConflict, "ROOM_NOT_AVAILABLE", "the room is already assigned for these dates"},
	"stays_idempotency_uk":            {apperr.KindConflict, "DUPLICATE_REQUEST", "this request was already processed"},
	"stays_live_line_uk":              {apperr.KindConflict, "ALREADY_CHECKED_IN", "this reservation room is already checked in"},
	"stay_rooms_open_room_uk":         {apperr.KindConflict, "ROOM_OCCUPIED", "the room is occupied"},
	"stay_rooms_open_stay_uk":         {apperr.KindConflict, "STAY_ALREADY_IN_ROOM", "the stay already occupies a room"},

	// Ledger
	"folios_stay_guest_uk":                     {apperr.KindConflict, "FOLIO_ALREADY_EXISTS", "the stay already has a guest folio"},
	"folios_unlinked_open_uk":                  {apperr.KindConflict, "FOLIO_ALREADY_EXISTS", "the reservation already has an open folio"},
	"folio_items_payment_uk":                   {apperr.KindConflict, "PAYMENT_ALREADY_POSTED", "the payment is already posted to the ledger"},
	"folio_items_reverses_uk":                  {apperr.KindConflict, "ALREADY_REVERSED", "the item has already been reversed"},
	"folio_items_idempotency_uk":               {apperr.KindConflict, "DUPLICATE_REQUEST", "this request was already processed"},
	"reservations_idempotency_uk":              {apperr.KindConflict, "DUPLICATE_REQUEST", "this request was already processed"},
	"cli_idempotency_uk":                       {apperr.KindConflict, "DUPLICATE_REQUEST", "this request was already processed"},
	"cli_number_uk":                            {apperr.KindConflict, "NUMBER_TAKEN", "the invoice number is already in use"},
	"cli_total_ck":                             {apperr.KindInvalid, "INVALID_AMOUNT", "an invoice total is positive"},
	"cli_company_fk":                           {apperr.KindNotFound, "COMPANY_NOT_FOUND", "the company does not exist in this property"},
	"cli_no_delete":                            {apperr.KindConflict, "LEDGER_IMMUTABLE", "invoices cannot be deleted"},
	"cli_void_only":                            {apperr.KindConflict, "LEDGER_IMMUTABLE", "an invoice can only change from ISSUED to VOIDED"},
	"clil_payment_live_uk":                     {apperr.KindConflict, "TRANSFER_NOT_AVAILABLE", "a transfer is already on an invoice"},
	"clil_no_delete":                           {apperr.KindConflict, "LEDGER_IMMUTABLE", "invoice lines cannot be deleted"},
	"clil_release_only":                        {apperr.KindConflict, "LEDGER_IMMUTABLE", "an invoice line can only be released"},
	"clr_idempotency_uk":                       {apperr.KindConflict, "DUPLICATE_REQUEST", "this request was already processed"},
	"clr_number_uk":                            {apperr.KindConflict, "NUMBER_TAKEN", "the receipt number is already in use"},
	"clr_amount_ck":                            {apperr.KindInvalid, "INVALID_AMOUNT", "a positive amount"},
	"clr_no_delete":                            {apperr.KindConflict, "LEDGER_IMMUTABLE", "city ledger receipts cannot be deleted"},
	"clr_void_only":                            {apperr.KindConflict, "LEDGER_IMMUTABLE", "a receipt can only change from POSTED to VOIDED"},
	"clr_company_fk":                           {apperr.KindNotFound, "COMPANY_NOT_FOUND", "the company does not exist in this property"},
	"payments_company_fk":                      {apperr.KindNotFound, "COMPANY_NOT_FOUND", "the company does not exist in this property"},
	"companies_property_code_uk":               {apperr.KindConflict, "CODE_TAKEN", "the company code is already in use"},
	"booking_groups_property_code_uk":          {apperr.KindConflict, "CODE_TAKEN", "the group code is already in use"},
	"reservations_company_fk":                  {apperr.KindNotFound, "COMPANY_NOT_FOUND", "the company does not exist in this property"},
	"reservations_group_fk":                    {apperr.KindNotFound, "GROUP_NOT_FOUND", "the group does not exist in this property"},
	"payments_idempotency_uk":                  {apperr.KindConflict, "DUPLICATE_REQUEST", "this request was already processed"},
	"charge_codes_gl_account_code_ck":          {apperr.KindInvalid, "INVALID_GL_ACCOUNT_CODE", "the account code is not valid"},
	"taxes_gl_account_code_ck":                 {apperr.KindInvalid, "INVALID_GL_ACCOUNT_CODE", "the account code is not valid"},
	"service_charges_gl_account_code_ck":       {apperr.KindInvalid, "INVALID_GL_ACCOUNT_CODE", "the account code is not valid"},
	"folio_items_revenue_account_code_ck":      {apperr.KindInvalid, "INVALID_GL_ACCOUNT_CODE", "the account code is not valid"},
	"folio_item_components_gl_account_code_ck": {apperr.KindInvalid, "INVALID_GL_ACCOUNT_CODE", "the account code is not valid"},
	"folio_items_approval_ck":                  {apperr.KindInvalid, "APPROVAL_REQUIRED", "a correction needs an approver"},
	"payments_approval_ck":                     {apperr.KindInvalid, "APPROVAL_REQUIRED", "a correction needs an approver"},
	"stay_charge_postings_once_uk":             {apperr.KindConflict, "ROOM_CHARGE_ALREADY_POSTED", "the room charge for this night is already posted"},
	"folio_items_totals_match_components":      {apperr.KindInternal, "LEDGER_INCONSISTENT", "the ledger entry is inconsistent"},
	"folio_items_business_day_fk":              {apperr.KindConflict, "BUSINESS_DAY_NOT_FOUND", "the posting business date does not exist"},
	"payments_business_day_fk":                 {apperr.KindConflict, "BUSINESS_DAY_NOT_FOUND", "the posting business date does not exist"},
	"stay_charge_postings_bd_fk":               {apperr.KindConflict, "BUSINESS_DAY_NOT_FOUND", "the posting business date does not exist"},
}

// MappedConstraintNames lists every constraint name with a dedicated API error
// (used by tests to verify the map against the live schema).
func MappedConstraintNames() []string {
	names := make([]string, 0, len(constraintErrors))
	for name := range constraintErrors {
		names = append(names, name)
	}
	return names
}

// MapError translates PostgreSQL errors into *apperr.Error. Errors that are not
// database errors, and errors that are already *apperr.Error, are returned
// unchanged. The original error is kept as the cause (logged, never exposed).
func MapError(err error) error {
	if err == nil {
		return nil
	}
	if _, ok := apperr.As(err); ok {
		return err
	}
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		return err
	}

	if m, ok := constraintErrors[pgErr.ConstraintName]; ok {
		return apperr.New(m.kind, m.code, m.message).WithCause(err)
	}

	switch pgErr.Code {
	case "23505": // unique_violation
		return apperr.Conflict("DUPLICATE", "the record already exists").WithCause(err)
	case "23503": // foreign_key_violation
		return apperr.Invalid("a referenced record does not exist").WithCause(err)
	case "23514", "23502": // check_violation, not_null_violation
		return apperr.Invalid("the data violates a business constraint").WithCause(err)
	case "23P01": // exclusion_violation
		return apperr.Conflict("CONFLICT", "the change conflicts with an existing record").WithCause(err)
	case "23001": // restrict_violation (append-only / immutable records)
		return apperr.Conflict("RECORD_IMMUTABLE", "the record cannot be changed").WithCause(err)
	case "55P03", "40P01", "40001": // lock_not_available, deadlock_detected, serialization_failure
		return apperr.Busy("RESOURCE_BUSY", "the resource is busy, please retry").WithCause(err)
	case "57014": // query_canceled (statement timeout or client cancel)
		return apperr.Busy("REQUEST_CANCELLED", "the operation was cancelled or timed out").WithCause(err)
	}
	return err
}
