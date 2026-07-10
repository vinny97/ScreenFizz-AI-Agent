DROP INDEX IF EXISTS screenfizz_prospects_brevo_pending_idx;

ALTER TABLE screenfizz_prospects
    DROP COLUMN IF EXISTS brevo_synced_at,
    DROP COLUMN IF EXISTS brevo_contact_id;
