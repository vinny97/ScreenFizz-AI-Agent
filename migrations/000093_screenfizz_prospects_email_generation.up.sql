ALTER TABLE screenfizz_prospects
    ADD COLUMN email_generated boolean NOT NULL DEFAULT false,
    ADD COLUMN email_subject text,
    ADD COLUMN email_body text;

CREATE INDEX screenfizz_prospects_email_pending_idx
    ON screenfizz_prospects (created_at)
    WHERE email_generated = false AND analysed = true;
