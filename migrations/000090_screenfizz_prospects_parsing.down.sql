DROP INDEX IF EXISTS screenfizz_prospects_unparsed_idx;

ALTER TABLE screenfizz_prospects
    DROP COLUMN IF EXISTS body_text,
    DROP COLUMN IF EXISTS h1,
    DROP COLUMN IF EXISTS meta_description,
    DROP COLUMN IF EXISTS page_title,
    DROP COLUMN IF EXISTS parsed;
