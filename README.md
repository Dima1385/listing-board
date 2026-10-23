# Listing board

A small board for real-estate listing sheets. An account signs in, creates a sheet for one address, attaches a brochure, and starts it. A worker then walks the sheet through a checklist. The page shows that progress while the worker runs.

The rules on this page are the spec. Where the code disagrees with this page, change the code. Two tests describe the code as it is today, and that behavior is wrong: `TestStartMarksTheJobSuccessful` and `TestUploadKeepsTheClientFilename`. Rewrite those tests so they describe this spec, then make the rewritten tests pass. Do not skip them, delete them, or weaken them to stay green. Leave `TestListHidesOtherUsersJobs` and `TestStartRequiresASession` in place.

Plan on about four hours. Fork this repository, do the work on your fork, and send the fork URL. Do not open a pull request.

Add `NOTES.md` at the repository root. In a few sentences, name one suggestion from Cursor or Claude that you rejected, and how you checked the result.

## Run

Docker is enough. From this directory:

```bash
docker compose up --build
```

Open http://localhost:8080. Postgres is published on `localhost:54329` (`board` / `board`, database `board`).

`go test ./...` needs Go 1.23 and does not need Postgres. The UI in `web/` is React and TypeScript. `npm run dev` serves it at http://localhost:5173 and proxies `/api` to port 8080.

## API

Keep these routes. JSON errors look like `{"error":"..."}`. The session cookie is `lb_session`.

| Method | Path | Status |
| --- | --- | --- |
| `POST` | `/api/register` | 201, body `{id, email}` |
| `POST` | `/api/login` | 200, body `{id, email}` |
| `POST` | `/api/logout` | 200 |
| `GET` | `/api/me` | 200, body `{id, email}` |
| `GET` | `/api/jobs` | 200, a JSON array |
| `POST` | `/api/jobs` | 201, body is the sheet. JSON `{title, address}` |
| `GET` | `/api/jobs/{id}` | 200, the sheet |
| `POST` | `/api/jobs/{id}/file` | 200, the sheet. Multipart field `file` |
| `POST` | `/api/jobs/{id}/start` | 200, the sheet |
| `GET` | `/api/jobs/{id}/file` | 200, the brochure bytes |
| `GET` | `/api/health` | 200, body `ok` |

A missing session is 401. An unknown sheet is 404. A sheet that belongs to someone else is also 404, on every route that takes an id, including the file download. The list shows only the caller's sheets.

A sheet looks like this:

```json
{
  "id": "…",
  "title": "Market loft",
  "address": "10 Market Street",
  "status": "draft",
  "error": "",
  "runId": "",
  "filename": "",
  "checklist": [
    {"label": "Reading the brochure", "state": "pending"},
    {"label": "Checking the address", "state": "pending"},
    {"label": "Publishing the sheet", "state": "pending"}
  ],
  "createdAt": "2026-01-01T00:00:00Z"
}
```

`status` is `draft`, `running`, `success`, or `failed`. Checklist labels stay exactly as above. A step state is `pending` or `done`.

## What to build

Today, `POST /start` marks the sheet `success` inside the HTTP handler and ticks every step. That is not the spec.

`POST /start` on a `draft` moves it to `running`, fills `runId` with a new id, and leaves every step `pending`. The handler does not set `success` or `failed`.

The worker is a second process, `cmd/worker`, in the same image. Add it to `docker-compose.yml` under the service name `worker`, with `restart: "no"`, and mount the same `boarddata` volume at `/data`. The API already uses that volume. Build both binaries:

```dockerfile
RUN CGO_ENABLED=0 go build -o /app/api ./cmd/api \
 && CGO_ENABLED=0 go build -o /app/worker ./cmd/worker
```

```yaml
  worker:
    build: .
    command: ["/app/worker"]
    environment:
      DATABASE_URL: postgres://board:board@postgres:5432/board?sslmode=disable
      DATA_DIR: /data
      STEP_PAUSE: 2s
    volumes:
      - boarddata:/data
    depends_on:
      postgres:
        condition: service_healthy
    restart: "no"
```

The worker retries its database connection for at least 30 seconds. It reads `STEP_PAUSE` as a Go duration and defaults to `2s`. Ship the compose file with `STEP_PAUSE` unset or set to `2s`.

The worker picks up `running` sheets. It marks the first `pending` step `done`, saves, then waits `STEP_PAUSE` before the next step. A step that is already `done` is not done again. After the third step is saved, the sheet becomes `success`. If the brochure file cannot be read, the sheet becomes `failed` and `error` is exactly `Brochure is missing`.

`runId` is chosen once, when the sheet leaves `draft`. Starting that same sheet again while it is `running` returns 200 and the same `runId`. Killing the worker leaves the sheet `running`. When the worker process starts again, it continues from the first pending step and keeps the same `runId`.

If this account already has a different sheet in `running`, `POST /start` on a draft returns 409. A `failed` sheet stays `failed`.

`POST /start` without a brochure returns 400 and the text `Attach a brochure first`.

## Brochure

One file per sheet. A second upload returns 400.

Reject a file larger than 1 MiB with 400, and do not leave it on disk. Decide the type from the bytes, not from the multipart `Content-Type` and not from the extension.

- PDF: the bytes start with `%PDF-`.
- Text: the whole file is valid UTF-8, contains no NUL byte, and does not start with `%PDF-`.
- Anything else, including an empty file, is 400.

Store the bytes at `{DATA_DIR}/{userID}/{jobID}/body`. Keep the original filename only in the sheet's `filename` field. A name such as `../../escaped.txt` must not create a file outside that directory.

## Page

The open sheet updates while the worker runs. Reloading the browser is not how the checklist moves. Show `error` when the sheet has failed.

A plain page is enough. Accounts, recovery, and cloud hosting are out of scope.
