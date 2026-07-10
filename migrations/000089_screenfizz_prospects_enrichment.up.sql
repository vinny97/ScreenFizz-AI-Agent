ALTER TABLE screenfizz_prospects
    ADD COLUMN enriched boolean NOT NULL DEFAULT false,
    ADD COLUMN website_html text;

CREATE INDEX screenfizz_prospects_unenriched_idx
    ON screenfizz_prospects (created_at)
    WHERE enriched = false;
