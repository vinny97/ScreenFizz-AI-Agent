DROP INDEX IF EXISTS screenfizz_prospects_unenriched_idx;

ALTER TABLE screenfizz_prospects
    DROP COLUMN IF EXISTS website_html,
    DROP COLUMN IF EXISTS enriched;
