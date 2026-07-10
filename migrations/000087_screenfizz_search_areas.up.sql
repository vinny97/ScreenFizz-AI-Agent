CREATE TABLE screenfizz_search_areas (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    county text NOT NULL UNIQUE,
    enabled boolean NOT NULL DEFAULT true
);

INSERT INTO screenfizz_search_areas (county, enabled) VALUES
    ('Buckinghamshire', true),
    ('Bedfordshire', true),
    ('Northamptonshire', true),
    ('Oxfordshire', true),
    ('Hertfordshire', true)
ON CONFLICT (county) DO NOTHING;
