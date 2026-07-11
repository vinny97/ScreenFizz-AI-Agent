ALTER TABLE screenfizz_prospects
    ADD COLUMN IF NOT EXISTS delivered_at timestamptz,
    ADD COLUMN IF NOT EXISTS opened_at timestamptz,
    ADD COLUMN IF NOT EXISTS clicked_at timestamptz,
    ADD COLUMN IF NOT EXISTS bounced_at timestamptz,
    ADD COLUMN IF NOT EXISTS unsubscribed_at timestamptz,
    ADD COLUMN IF NOT EXISTS last_event text;

CREATE TABLE email_events (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    prospect_id uuid REFERENCES screenfizz_prospects(id) ON DELETE SET NULL,
    event_type text NOT NULL,
    recipient_email text,
    brevo_message_id text,
    occurred_at timestamptz NOT NULL,
    payload jsonb NOT NULL,
    received_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX email_events_prospect_occurred_idx
    ON email_events (prospect_id, occurred_at DESC);

CREATE INDEX email_events_brevo_message_id_idx
    ON email_events (brevo_message_id);
