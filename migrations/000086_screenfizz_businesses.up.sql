CREATE TABLE screenfizz_businesses (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    business_name text,
    category text,
    website text,
    email text,
    phone text,
    address text,
    town text,
    postcode text,
    latitude double precision,
    longitude double precision,
    google_maps_url text,
    rating numeric,
    review_count integer,
    source text,
    contacted boolean NOT NULL DEFAULT false,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX screenfizz_businesses_website_unique
    ON screenfizz_businesses (website)
    WHERE website IS NOT NULL;
