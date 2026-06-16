# Codex Router

`cmd/codex-router` is an optional stateless sidecar for converting OpenAI
Responses requests into Chat Completions requests.

It is intended for domestic or OpenAI-compatible upstreams that expose
`/v1/chat/completions` but do not expose OpenAI's server-side
`/v1/responses` state service.

## Run

```bash
cd backend
CODEX_ROUTER_LISTEN=:8089 \
CODEX_ROUTER_UPSTREAM_BASE_URL=https://api.deepseek.com/v1 \
CODEX_ROUTER_API_KEY=sk-... \
go run ./cmd/codex-router
```

Docker usage can wrap the same command in a small image:

```bash
docker build -f backend/Dockerfile -t sub2api-codex-router .
docker run --rm -p 8089:8089 \
  -e CODEX_ROUTER_LISTEN=:8089 \
  -e CODEX_ROUTER_UPSTREAM_BASE_URL=https://api.deepseek.com/v1 \
  -e CODEX_ROUTER_API_KEY=sk-... \
  sub2api-codex-router ./codex-router
```

## Features

- Buffered and streaming conversion.
- SSE output for streaming Responses clients.
- Usage mapping from Chat Completions token usage.
- `reasoning_content` mapping into Responses reasoning events.
- `<think>...</think>` cleanup from visible output.
- Basic tools and tool calls conversion.
- Optional model mapping with `CODEX_ROUTER_MODEL_MAPPING`, for example:

```bash
CODEX_ROUTER_MODEL_MAPPING='{"gpt-5.1-codex":"deepseek-reasoner"}'
```

## Stateless Previous Response Handling

This router does not store conversation state. It is not equivalent to the
OpenAI Responses server.

When a request contains `previous_response_id`:

- If the request also contains a full non-empty `input`, the router drops
  `previous_response_id` and continues, because the upstream Chat Completions
  request already has the needed history.
- If `input` is empty or missing, the router returns `400` because it cannot
  reconstruct the conversation from an id alone.
