ALTER TABLE screenfizz_prospects
    ADD COLUMN IF NOT EXISTS brevo_contact_id bigint,
    ADD COLUMN IF NOT EXISTS brevo_synced_at timestamptz;

CREATE INDEX IF NOT EXISTS screenfizz_prospects_brevo_pending_idx
    ON screenfizz_prospects (created_at)
    WHERE brevo_contact_id IS NULL;
