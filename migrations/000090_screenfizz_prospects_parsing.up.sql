ALTER TABLE screenfizz_prospects
    ADD COLUMN parsed boolean NOT NULL DEFAULT false,
    ADD COLUMN page_title text,
    ADD COLUMN meta_description text,
    ADD COLUMN h1 text,
    ADD COLUMN body_text text;

CREATE INDEX screenfizz_prospects_unparsed_idx
    ON screenfizz_prospects (created_at)
    WHERE parsed = false AND website_html IS NOT NULL;
