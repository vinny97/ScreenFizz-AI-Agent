DROP INDEX IF EXISTS screenfizz_prospects_unanalysed_idx;

ALTER TABLE screenfizz_prospects
    DROP COLUMN IF EXISTS recommended_use_case,
    DROP COLUMN IF EXISTS reason,
    DROP COLUMN IF EXISTS likely_needs_digital_signage,
    DROP COLUMN IF EXISTS has_multiple_locations,
    DROP COLUMN IF EXISTS has_menu,
    DROP COLUMN IF EXISTS uses_events,
    DROP COLUMN IF EXISTS uses_promotions,
    DROP COLUMN IF EXISTS business_type,
    DROP COLUMN IF EXISTS business_summary,
    DROP COLUMN IF EXISTS analysed;
