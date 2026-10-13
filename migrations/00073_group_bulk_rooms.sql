-- +goose Up
-- Bulk rooms for a booking group (docs/architecture/30-group-folio.md, part A).
--
-- A confirmed reservation needs a booker (a guest row), and the contact of a group is only text. So the group may point at the guest made from its contact:
-- the booker of every room that is booked for the group. Nothing refers to a guest row from the group otherwise.
ALTER TABLE booking_groups ADD COLUMN contact_guest_id bigint;
ALTER TABLE booking_groups ADD CONSTRAINT booking_groups_contact_guest_fk FOREIGN KEY (tenant_id, contact_guest_id) REFERENCES guests (tenant_id, id);

-- A guest made by a group booking and not yet named ("group name · 7"): the room has to have a guest for the check-in, the folio and the invoice, but the person is not known yet.
-- Such a guest is left out of the guest search and of the duplicate hints; naming it (the rooming list, the check-in) clears the flag.
ALTER TABLE guests ADD COLUMN is_placeholder boolean NOT NULL DEFAULT false;
CREATE INDEX guests_placeholder_idx ON guests (tenant_id, id) WHERE is_placeholder;

-- +goose Down
-- A placeholder is data, not a setting: while a reservation line still has one, the column cannot be dropped without losing what it says.
-- +goose StatementBegin
DO $$
DECLARE v_n bigint;
BEGIN
    SELECT count(*) INTO v_n FROM guests g WHERE g.is_placeholder AND EXISTS (SELECT 1 FROM reservation_rooms l WHERE l.tenant_id = g.tenant_id AND l.guest_id = g.id);
    IF v_n > 0 THEN
        RAISE EXCEPTION '00073 down: % guest(s) made by a group booking are still the guest of a reservation room; name them (the rooming list) before going back', v_n;
    END IF;
END $$;
-- +goose StatementEnd
DROP INDEX guests_placeholder_idx;
ALTER TABLE guests DROP COLUMN is_placeholder;
ALTER TABLE booking_groups DROP CONSTRAINT booking_groups_contact_guest_fk;
ALTER TABLE booking_groups DROP COLUMN contact_guest_id;
