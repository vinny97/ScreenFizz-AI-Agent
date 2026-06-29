# Chatwoot AgentBot adapter

GoClaw can receive Chatwoot AgentBot webhooks, run incoming customer text through
the OpenAI-compatible chat completions endpoint, and reply to the same Chatwoot
conversation.

## Configuration

Set these variables before starting the gateway:

```bash
CHATWOOT_BASE_URL=https://chatwoot.example.com
CHATWOOT_API_ACCESS_TOKEN=your-chatwoot-user-token
CHATWOOT_WEBHOOK_SECRET=your-agentbot-webhook-secret
CHATWOOT_REQUIRE_WEBHOOK_SIGNATURE=true
GOCLAW_BASE_URL=http://127.0.0.1:18790
GOCLAW_API_KEY=your-goclaw-api-key
GOCLAW_MODEL=your-agent-key
```

Configure the AgentBot webhook URL in Chatwoot as:

```text
https://your-goclaw-host/chatwoot/webhook
```

`CHATWOOT_API_ACCESS_TOKEN` is sent only as the `api_access_token` header when
posting replies to Chatwoot. `CHATWOOT_WEBHOOK_SECRET` is used only to verify
incoming webhook deliveries.

`GET /chatwoot/health` returns `200` when the required variables are present, or
`503` with the missing variable names otherwise.

## Webhook signatures

Chatwoot 4.13 and later sends these headers for signed AgentBot webhooks:

- `X-Chatwoot-Timestamp`: the signing timestamp.
- `X-Chatwoot-Signature`: `sha256=<hex digest>`.

Verification computes HMAC-SHA256 using `CHATWOOT_WEBHOOK_SECRET` as the key and
the exact bytes `<X-Chatwoot-Timestamp>.<raw request body>` as the signed value.
The expected and supplied signatures are compared in constant time.

An invalid signature, or a signature without its timestamp, is always rejected
with HTTP `401`. When no signature header is present, the default development
behavior logs `security.chatwoot.signature_missing` and continues. Set
`CHATWOOT_REQUIRE_WEBHOOK_SIGNATURE=true` in production to reject unsigned
requests. Strict mode also makes a missing `CHATWOOT_WEBHOOK_SECRET` fail the
health check.

The webhook accepts `message_created` events. It ignores outgoing messages,
private notes, empty content, bot-authored messages, non-message events, and
message IDs already completed within the last 24 hours. Failed upstream calls
return `502` and release the ID so Chatwoot can retry it.

Replies use Chatwoot's account message endpoint:
`POST /api/v1/accounts/{account_id}/conversations/{conversation_id}/messages`.
