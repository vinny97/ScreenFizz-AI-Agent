DROP INDEX IF EXISTS screenfizz_prospects_pending_review_idx;

ALTER TABLE screenfizz_prospects
    DROP COLUMN IF EXISTS status;
