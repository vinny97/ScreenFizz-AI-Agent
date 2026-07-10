DROP INDEX IF EXISTS screenfizz_prospects_email_pending_idx;

ALTER TABLE screenfizz_prospects
    DROP COLUMN IF EXISTS email_body,
    DROP COLUMN IF EXISTS email_subject,
    DROP COLUMN IF EXISTS email_generated;
