ALTER TABLE screenfizz_prospects
    ADD COLUMN IF NOT EXISTS status text;

UPDATE screenfizz_prospects
SET status = 'pending_review'
WHERE email_generated = true
  AND status IS NULL;

CREATE INDEX IF NOT EXISTS screenfizz_prospects_pending_review_idx
    ON screenfizz_prospects (created_at)
    WHERE status = 'pending_review';
