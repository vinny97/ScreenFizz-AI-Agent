ALTER TABLE screenfizz_prospects
    ADD COLUMN IF NOT EXISTS email_generated boolean NOT NULL DEFAULT false,
    ADD COLUMN IF NOT EXISTS email_subject text,
    ADD COLUMN IF NOT EXISTS email_body text;

CREATE INDEX IF NOT EXISTS screenfizz_prospects_email_pending_idx
    ON screenfizz_prospects (created_at)
    WHERE email_generated = false AND analysed = true;
