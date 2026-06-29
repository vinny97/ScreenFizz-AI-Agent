# Chatwoot AgentBot adapter

GoClaw can receive Chatwoot AgentBot webhooks, run incoming customer text through
the OpenAI-compatible chat completions endpoint, and reply to the same Chatwoot
conversation.

## Configuration

Set these variables before starting the gateway:

```bash
CHATWOOT_BASE_URL=https://chatwoot.example.com
CHATWOOT_API_ACCESS_TOKEN=your-chatwoot-user-token
GOCLAW_BASE_URL=http://127.0.0.1:18790
GOCLAW_API_KEY=your-goclaw-api-key
GOCLAW_MODEL=your-agent-key
```

Configure the AgentBot webhook URL in Chatwoot as:

```text
https://your-goclaw-host/chatwoot/webhook
```

`GET /chatwoot/health` returns `200` when all five variables are present, or
`503` with the missing variable names otherwise.

The webhook accepts `message_created` events. It ignores outgoing messages,
private notes, empty content, bot-authored messages, non-message events, and
message IDs already completed within the last 24 hours. Failed upstream calls
return `502` and release the ID so Chatwoot can retry it.

Replies use Chatwoot's account message endpoint:
`POST /api/v1/accounts/{account_id}/conversations/{conversation_id}/messages`.
