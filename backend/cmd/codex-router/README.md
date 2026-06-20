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
CODEX_ROUTER_UPSTREAM_BASE_URL=http://127.0.0.1:8080 \
go run ./cmd/codex-router
```

The incoming `Authorization` header is forwarded to sub2api, so Codex can use
the same sub2api key. Set `CODEX_ROUTER_API_KEY` only when the sidecar should
always use one fixed upstream key.

Build the independent image:

```bash
docker build -f backend/cmd/codex-router/Dockerfile \
  -t sub2api-codex-router backend
docker run --rm -p 8089:8089 \
  -e CODEX_ROUTER_UPSTREAM_BASE_URL=http://host.docker.internal:8080 \
  sub2api-codex-router
```

Or attach it to an existing sub2api Docker network:

```bash
docker compose -f deploy/docker-compose.codex-router.yml up -d --build
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

The main sub2api service does not convert domestic `/v1/responses` requests.
That endpoint returns `501` for domestic groups, keeping protocol conversion
inside this optional sidecar.

See `THIRD_PARTY_NOTICES.md` for the cc-switch reference and license notice.

## Stateless Previous Response Handling

This router does not store conversation state. It is not equivalent to the
OpenAI Responses server.

When a request contains `previous_response_id`:

- If the request also contains a full non-empty `input`, the router drops
  `previous_response_id` and continues, because the upstream Chat Completions
  request already has the needed history.
- If `input` is empty or missing, the router returns `400` because it cannot
  reconstruct the conversation from an id alone.
