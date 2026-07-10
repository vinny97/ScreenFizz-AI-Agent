ALTER TABLE screenfizz_prospects
    ADD COLUMN brevo_contact_id bigint,
    ADD COLUMN brevo_synced_at timestamptz;

CREATE INDEX screenfizz_prospects_brevo_pending_idx
    ON screenfizz_prospects (created_at)
    WHERE brevo_contact_id IS NULL;
