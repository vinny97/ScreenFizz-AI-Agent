DROP INDEX IF EXISTS screenfizz_prospects_approved_unsent_idx;

ALTER TABLE screenfizz_prospects
    DROP COLUMN IF EXISTS brevo_message_id,
    DROP COLUMN IF EXISTS sent_at;
