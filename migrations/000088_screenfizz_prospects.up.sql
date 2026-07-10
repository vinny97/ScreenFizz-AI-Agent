CREATE TABLE screenfizz_prospects (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    business_id uuid NOT NULL UNIQUE REFERENCES screenfizz_businesses(id),
    created_at timestamptz NOT NULL DEFAULT now()
);
