ALTER TABLE screenfizz_prospects
    ADD COLUMN IF NOT EXISTS analysed boolean NOT NULL DEFAULT false,
    ADD COLUMN IF NOT EXISTS business_summary text,
    ADD COLUMN IF NOT EXISTS business_type text,
    ADD COLUMN IF NOT EXISTS uses_promotions boolean,
    ADD COLUMN IF NOT EXISTS uses_events boolean,
    ADD COLUMN IF NOT EXISTS has_menu boolean,
    ADD COLUMN IF NOT EXISTS has_multiple_locations boolean,
    ADD COLUMN IF NOT EXISTS likely_needs_digital_signage integer,
    ADD COLUMN IF NOT EXISTS reason text,
    ADD COLUMN IF NOT EXISTS recommended_use_case text;

CREATE INDEX IF NOT EXISTS screenfizz_prospects_unanalysed_idx
    ON screenfizz_prospects (created_at)
    WHERE parsed = true AND analysed = false;
