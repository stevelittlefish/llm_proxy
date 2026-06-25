# JSON Logs API

The JSON logs API exposes the same request records shown in the HTML log UI.
It is read-only and unauthenticated, matching the local-network assumption used
by the rest of the proxy.

All endpoints return JSON.

## Endpoints

| Method | Path | Description |
|---|---|---|
| `GET` | `/api/logs` | List logged requests with optional filters and pagination. |
| `GET` | `/api/logs/{id}` | Return one log entry by ID, including raw request/response bodies. |
| `GET` | `/api/logs?id={id}` | Query-parameter form of the single-entry endpoint. |

## List Logs

```bash
curl 'http://localhost:11435/api/logs?limit=50&offset=0'
```

### Query Parameters

| Parameter | Type | Default | Description |
|---|---:|---:|---|
| `limit` | integer | `50` | Page size. Values above `1000` are capped to `1000`; values below `1` use the default. |
| `offset` | integer | `0` | Number of matching rows to skip. Negative values are treated as `0`. |
| `model` | string | | Exact model match. |
| `endpoint` | string | | Exact endpoint match, for example `/v1/chat/completions`. |
| `backend_type` | string | | Exact backend type match, usually `openai` or `ollama`. |
| `status` | integer | | Exact HTTP status code match. |
| `errors_only` | boolean | `false` | When `true`, only rows with a non-empty error or `status_code >= 400` are returned. |
| `since` | RFC3339 timestamp | | Inclusive lower bound on `timestamp`. |
| `until` | RFC3339 timestamp | | Inclusive upper bound on `timestamp`. |
| `q` | string | | Case-insensitive substring search across `model`, `last_message`, and `error`. |
| `order` | string | `desc` | Timestamp order. Allowed values: `asc`, `desc`. |
| `bodies` | boolean | `false` | When `true`, include raw frontend/backend request and response body fields in list entries. |

Boolean values use Go's standard boolean parser, so values such as `true`,
`false`, `1`, and `0` are accepted.

### Response

```json
{
  "total": 1284,
  "limit": 50,
  "offset": 0,
  "entries": [
    {
      "id": 1284,
      "timestamp": "2026-06-26T22:14:05Z",
      "endpoint": "/v1/chat/completions",
      "method": "POST",
      "model": "gemma4-31b",
      "status_code": 200,
      "latency_ms": 8123,
      "stream": true,
      "backend_type": "openai",
      "error": "",
      "frontend_url": "http://localhost:11435/v1/chat/completions",
      "backend_url": "http://ai.example:8008/v1/chat/completions",
      "last_message": "hello"
    }
  ]
}
```

When `bodies=true`, each entry may also include:

```json
{
  "frontend_request": "{\"model\":\"gemma4-31b\",...}",
  "frontend_response": "data: {...}\n\n",
  "backend_request": "{\"model\":\"gemma4-31b\",...}",
  "backend_response": "data: {...}\n\n"
}
```

The body fields are returned as raw strings exactly as stored. They may contain
JSON, newline-delimited JSON, SSE text, or error text.

## Get One Log Entry

```bash
curl 'http://localhost:11435/api/logs/1284'
curl 'http://localhost:11435/api/logs?id=1284'
```

The single-entry endpoint returns one log entry object, not a wrapper. It always
includes the raw body fields.

```json
{
  "id": 1284,
  "timestamp": "2026-06-26T22:14:05Z",
  "endpoint": "/v1/chat/completions",
  "method": "POST",
  "model": "gemma4-31b",
  "status_code": 200,
  "latency_ms": 8123,
  "stream": true,
  "backend_type": "openai",
  "error": "",
  "frontend_url": "http://localhost:11435/v1/chat/completions",
  "backend_url": "http://ai.example:8008/v1/chat/completions",
  "last_message": "hello",
  "frontend_request": "{\"model\":\"gemma4-31b\",...}",
  "frontend_response": "data: {...}\n\n",
  "backend_request": "{\"model\":\"gemma4-31b\",...}",
  "backend_response": "data: {...}\n\n"
}
```

## Errors

Errors use this shape:

```json
{"error":"message"}
```

Common status codes:

| Status | Meaning |
|---:|---|
| `400` | Invalid ID, timestamp, boolean, integer, or `order` query value. |
| `404` | The requested log entry ID does not exist. |
| `405` | Method other than `GET`. |
| `500` | Database query failure. |

## Examples

Last 10 OpenAI frontend chat requests:

```bash
curl 'http://localhost:11435/api/logs?endpoint=/v1/chat/completions&backend_type=openai&limit=10'
```

Find recent failures:

```bash
curl 'http://localhost:11435/api/logs?errors_only=true&limit=20'
```

Search for requests mentioning a model or prompt fragment:

```bash
curl 'http://localhost:11435/api/logs?q=gemma4&limit=20'
```

Fetch one full record including the exact body sent to the backend:

```bash
curl 'http://localhost:11435/api/logs/1284' | jq '.backend_request'
```
