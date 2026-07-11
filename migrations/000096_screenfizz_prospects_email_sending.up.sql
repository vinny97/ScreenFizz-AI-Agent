ALTER TABLE screenfizz_prospects
    ADD COLUMN IF NOT EXISTS sent_at timestamptz,
    ADD COLUMN IF NOT EXISTS brevo_message_id text;

CREATE INDEX IF NOT EXISTS screenfizz_prospects_approved_unsent_idx
    ON screenfizz_prospects (created_at)
    WHERE status = 'approved' AND sent_at IS NULL;
