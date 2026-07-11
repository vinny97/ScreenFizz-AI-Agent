DROP INDEX IF EXISTS email_events_brevo_message_id_idx;
DROP INDEX IF EXISTS email_events_prospect_occurred_idx;
DROP TABLE IF EXISTS email_events;

ALTER TABLE screenfizz_prospects
    DROP COLUMN IF EXISTS last_event,
    DROP COLUMN IF EXISTS unsubscribed_at,
    DROP COLUMN IF EXISTS bounced_at,
    DROP COLUMN IF EXISTS clicked_at,
    DROP COLUMN IF EXISTS opened_at,
    DROP COLUMN IF EXISTS delivered_at;
