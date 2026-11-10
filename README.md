# 🤖 ADK Review

[![CI](https://github.com/shouni/adk-review/actions/workflows/ci.yml/badge.svg)](https://github.com/shouni/adk-review/actions/workflows/ci.yml)
[![Status](https://img.shields.io/badge/Status-Active-brightgreen)](#)
[![Language](https://img.shields.io/badge/Language-Go-blue)](https://go.dev/)
[![Platform](https://img.shields.io/badge/Platform-Cloud%20Run-blue?logo=google-cloud)](https://cloud.google.com/run)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](https://opensource.org/licenses/MIT)

English | [日本語](README.ja.md)

## 🚀 About - People and agents request reviews at the same URL

**ADK Review** is a service on Cloud Run + Cloud Tasks that has an AI agent review the diff of a Git
repository.

Given a repository URL, the two branches to compare and a review mode, an [ADK for Go](https://github.com/google/adk-go)
agent investigates the working directory with head checked out using read-only tools (`read_file` / `list_files` /
`search_text`), checks the diff against what lies **beyond the diff**, and then writes up its findings. It catches
what the diff alone cannot show: "a contradiction with the previous chapter", "consistency of a character's
settings", "the callers of a changed function". The main subjects are article and novel manuscripts kept in Git
and source code; what to look at is switched by the modes in `assets/prompts/` (`article` / `novel` / `code`).

There are no separate routes for the screens and the API. Whether from the browser form or from an agent through
the MCP gateway, you request a review and read the result at the same URL. A result is a decision (`Blocker` /
`Major` / `Minor` / `None`) plus a list of findings, and it appears in the history. When the model's output is cut
off at its limit, only the completed findings are kept as `✂ 部分` (partial), and the list and detail pages say so.
The diff size, duration, token usage and number of tool calls can be read from the history, failed runs included.

One image is deployed as **two services by `SERVER_ROLE`: the web role (public) and the worker role (private)**.
The review procedure itself is delegated to [`go-review-kit`](https://github.com/shouni/go-review-kit), job
records and paging to [`go-job-kit`](https://github.com/shouni/go-job-kit); this repository handles accepting
requests, authentication, asynchronous execution, storing and serving results (screens and a JSON API), and the
implementation of the reviewer (the ADK agent).

---

## 📦 Usage

### 1. Configuration

`ValidateEssentialConfig` validates only what each role needs. Settings for a role the process does not serve are
not read (the worker role does not require OAuth settings). Durations or integers with an invalid format are an
error at startup rather than falling back to the default.

**Required for every role**

| Variable | Description |
| --- | --- |
| `SERVER_ROLE` | `web` / `worker` / `both` (`both` is for local development). **Unset or unknown values are an error at startup.** Only the roles served are built, and only their routes are registered. |
| `SERVICE_URL` | This service's own root URL (no trailing slash). **It has no default, and HTTPS is required in production** (only development host names such as `localhost` are exempt). It builds the OAuth redirect (`<SERVICE_URL>/auth/callback`) and the detail page URL put in notifications. |
| `GEMINI_MODELS` | Gemini model names. **Several can be given, comma-separated; the first is the default model.** With several, they can be chosen on the form. **There is no default; unset is an error at startup.** |
| `GCP_PROJECT_ID` | GCP project ID. **Gemini is called through Vertex AI only** (there is no API key path). Running locally needs ADC. |
| `GCS_REVIEW_BUCKET` | Where progress and results are stored. A bucket **name**; a leading `gs://` and trailing `/` are dropped if present. The web role uses it to show and delete history, the worker role to save, so **it is required for both roles**. |
| `TASK_DISPATCH_DEADLINE` | How long Cloud Tasks waits for the worker's response (in the form `10m`). **It is the effective limit on the worker's run time** and must be longer than `PIPELINE_TIMEOUT`. **It has no default.** gcp-kit checks the range Cloud Tasks accepts (15 seconds to 30 minutes) when the enqueuer is built. |

**Required for the web role (`web` / `both`)**

| Variable | Description |
| --- | --- |
| `CLOUD_TASKS_QUEUE_ID` | The name of the queue tasks are submitted to. |
| `WORKER_URL` | The URL of the worker **service**, without a path. Under `both`, unset falls back to `SERVICE_URL` (enqueueing to itself). |
| `GCP_LOCATION_ID` | **The region of the Cloud Tasks queue** (e.g. `asia-northeast1`). It is not Gemini's location, which is fixed to `global`. |
| `TASK_CALLER_SERVICE_ACCOUNT_EMAIL` | The caller SA put on tasks (the web role's runtime SA). **Cloud Tasks issues the token**; this process does not sign it. |
| `GOOGLE_CLIENT_ID` / `GOOGLE_CLIENT_SECRET` | The Google OAuth client. |
| `ALLOWED_EMAILS` / `ALLOWED_DOMAINS` | Who may log in (comma-separated). **If both are empty, startup fails.** |
| `ALLOWED_M2M_SERVICE_ACCOUNTS` | The SAs allowed when machines (such as the MCP gateway) call with an OIDC Bearer token (comma-separated). **If empty, the web role does not start.** |

**Required for the worker role (`worker` / `both`)**

| Variable | Description |
| --- | --- |
| `PIPELINE_TIMEOUT` | The run time limit of one review (clone to AI to save), in the form `5m`. **It has no default, and startup checks that it is shorter than `TASK_DISPATCH_DEADLINE`** (unlimited is not allowed). |
| `ALLOWED_TASK_SERVICE_ACCOUNTS` | The caller SAs accepted (comma-separated). Give the **submitting side's** SA. The web and worker roles run as separate SAs, so the worker lists "someone else's SA". |
| `TASK_AUDIENCE_URL` | The audience for OIDC verification (the worker's own URL). Unset falls back to `SERVICE_URL`. |

**Optional**

| Variable | Description |
| --- | --- |
| `PORT` | The listening port (Default: `8080`). |
| `AGENT_MAX_TOOL_CALLS` | The limit on tool calls per review. `0` means the default of `32`. Lower it for shallower, faster reviews. |
| `MAX_DIFF_BYTES` | The limit on the diff sent to the AI, in bytes (Default: `327680` = 320 KiB). Beyond it the review fails without running. `0` means unlimited; a negative value is an error at startup. |
| `SSH_KEY_PATH` | The path of the SSH private key used to clone repositories (Default: `~/.ssh/id_rsa`). On Cloud Run, give the Secret Manager mount path. |
| `SESSION_FIRESTORE_DATABASE` / `SESSION_FIRESTORE_COLLECTION` | The Firestore holding sessions (both default to `sessions`). Web role only. Job status lives in GCS, so this database is for sessions only. |
| `HTTP_TIMEOUT` | The limit on external HTTP calls such as Slack notifications (Default: `10s`). |
| `SLACK_WEBHOOK_URL` | Where success, failure and skip notifications go. Unset disables notifications. |
| `LOG_LEVEL` | The log level (`debug` / `info` / `warn` / `error`. Default: `info`). |

> **There is no switch to disable SSH host key verification.** The `Dockerfile` bakes GitHub's host keys into
> `/etc/ssh/ssh_known_hosts` at build time and points `SSH_KNOWN_HOSTS` at it. The only repository URL format
> accepted is `git@github.com:owner/repo.git`.

> Environment variables hold only **settings decided by the deployment**. Values that change per run, such as the
> repository, branches, mode and model, are passed in the task payload (JSON).

Each role runs as its own SA. The infrastructure's Terraform is the source of truth for IAM; the permissions
needed are:

| Role | Permissions needed |
| --- | --- |
| Web role SA | Read/write on `GCS_REVIEW_BUCKET` (acceptance records, showing and deleting history), submitting to the Cloud Tasks queue, issuing OIDC tokens as `TASK_CALLER_SERVICE_ACCOUNT_EMAIL` (ActAs), read/write on the session Firestore database |
| Worker role SA | Read/write on `GCS_REVIEW_BUCKET` (`status.json` / `report.json`), calling Vertex AI (`roles/aiplatform.user`) |
| Both | Reading the secrets used (SSH key, OAuth secret and so on) |

* GCS gets `objectUser` per bucket (not `objectAdmin`, and not at the project level).
* Secrets are granted per secret.
* The session Firestore needs a TTL policy on `expiresAt`.

Missing permissions result in `403 Forbidden`.

### 2. Startup

```bash
go run .        # SERVER_ROLE is required
```

Only the roles in `SERVER_ROLE` are built. A setup where roles and handlers do not match fails at startup, before
the router could return a 404 (`builder.AppHandlers.Validate`).

| Role | What is built | Routes exposed |
| --- | --- | --- |
| `web` | Request form, mode list, history, detail, deletion, submission to Cloud Tasks, session Firestore | `/`, `/modes`, `/jobs/*`, `/auth/*` |
| `worker` | Pipeline (Git + ADK agent + GCS + notifications) | `POST /tasks/execute-review` |
| `both` | Both (for local development) | All of the above |

Even started with `both`, Cloud Tasks cannot deliver to localhost, so **a submitted request never runs a review and
the history stays at `queued`** (the queue has `max_attempts = 1`, so no retry comes either). Verify the logic with
`go test ./... -race`.

### 3. HTTP endpoints

**There is a single authentication.** `auth.Protected` accepts both an OIDC Bearer token and a session, so people
and machines call the same URLs. Only `GET /health` and `/static/*` are outside authentication, and they are
registered regardless of the role.

| Method | Path | Purpose |
| --- | --- | --- |
| `GET` | `/health` | Health check. No authentication |
| `GET` | `/static/*` | The embedded CSS / JS and Bootstrap under `vendor/`. No authentication. No CDN is referenced, so the CSP is `default-src 'self'`. `vendor/`, whose version is in the path, is `public, max-age=31536000, immutable`; the app's own assets are `public, max-age=300, must-revalidate` |
| `GET` | `/auth/login` `/auth/callback` `/auth/logout` | Google OAuth login, callback and logout |
| `GET` | `/` | The review request form. `?from={jobID}` carries over that review's request (the detail page's 「同じ内容で再依頼」, resubmit with the same contents). If it cannot be loaded, the form opens with the defaults |
| `GET` | `/modes` | The selectable review modes (JSON only: `key` / `label` / `direction` / `use_when` / `excerpt`). The front matter of `assets/prompts/*.md` is the only source |
| `POST` | `/jobs` | Submit a review. A form body means the screen, a JSON body means a machine (fields in [4. Task payload](#4-task-payload)). Acceptance is `202` with `Location: /jobs/{jobID}`; the JSON carries `job_id` and `detail_url` |
| `GET` | `/jobs` | The history, newest first. `?page=` / `?per_page=` (default 20, max 100). JSON has `items` and `meta` |
| `GET` | `/jobs/{jobID}` | One job. The same URL from submission to deletion. Browsers get the detail page (full findings, decision, measurements); `Accept: application/json` gets the progress (`queued` / `running` / `succeeded` / `failed`) with `outcome`, `truncated`, `metrics`, and `report_url` once there is an artifact. Poll here to detect completion. **No record is a 404, an unreadable one a 502** |
| `GET` | `/jobs/{jobID}/report` | The full findings (JSON only). `409` while running, `404` when finished without an artifact (failed, skipped) |
| `DELETE` | `/jobs/{jobID}` | Deletes everything under the job's prefix and returns `204`. The delete button on screen also sends DELETE via fetch. **`409` while running (`queued` / `running`)** |
| `POST` | `/tasks/execute-review` | The worker, for Cloud Tasks only. A request failing OIDC verification gets 401; with `SERVER_ROLE=web` **the route is not registered at all**, so 404 |

**One resource, one route.** The representation is decided by `Accept`: send `application/json` and you get JSON;
a browser's `Accept` gets the screen. Error bodies follow the same rule and become `{"error": "..."}`. Paths follow
the public-docs URL naming convention.

**Methods with side effects need a CSRF token.** Forms send it in the hidden `csrf_token` field, and the screens'
JS in the `X-CSRF-Token` header. Machines authenticated by an OIDC Bearer token skip session and CSRF checks;
instead, callers are restricted by `ALLOWED_M2M_SERVICE_ACCOUNTS`.

Validation of a submission (whether the mode is in `/modes`, the model in `GEMINI_MODELS`, and the format of the
repository URL and branch names) goes through **the same function** for the screen and the API.

### 4. Task payload

There is one kind of job. The `command` recorded in `status.json` is `review` (`domain.CommandReview`), and the
task body is a `domain.ReviewRequest`.

| `command` | What it does | Required fields |
| --- | --- | --- |
| `review` | Gets the diff, has the agent review it, saves `report.json` and notifies | `job_id`, `repo_url`, `base_branch`, `feature_branch`, `mode`, `model_name`, `storage_uri`, `public_url` |

| Field | Description |
| --- | --- |
| `job_id` | The job's identifier. It corresponds to where the artifact and progress are stored and to one row of the history. Minted by the web role. |
| `repo_url` | The repository to review. Only the form `git@github.com:owner/repo.git`. |
| `base_branch` / `feature_branch` | The branches compared. `feature_branch` is head, and it is what is checked out in the working directory. |
| `mode` | The review mode. Placing `assets/prompts/<mode>.md` adds a mode. The display name and description come from the front matter at the top of the file (`label` / `direction` / `use_when` / `excerpt`). `GET /modes` lists them. |
| `model_name` | The Gemini model to use. Only those in `GEMINI_MODELS`. |
| `storage_uri` | Where `report.json` is saved. Derived from the job ID by the web role. |
| `public_url` | The detail page URL. The link target in Slack notifications. |

```json
{
  "job_id": "job-20260904-120000-a1b2c3d4e5f6",
  "repo_url": "git@github.com:owner/repo.git",
  "base_branch": "main",
  "feature_branch": "develop",
  "mode": "article",
  "model_name": "gemini-2.5-pro",
  "storage_uri": "gs://my-bucket/reviews/job-20260904-120000-a1b2c3d4e5f6/report.json",
  "public_url": "https://myapp.run.app/jobs/job-20260904-120000-a1b2c3d4e5f6"
}
```

**Only the first 5 fields are sent to `POST /jobs`.** The web role decides `job_id` / `storage_uri` / `public_url`,
so sending them is a `400` (unknown fields are not silently dropped).

```json
{
  "repo_url": "git@github.com:owner/repo.git",
  "base_branch": "main",
  "feature_branch": "develop",
  "mode": "article",
  "model_name": "gemini-2.5-pro"
}
```

A job's objects are gathered under its job ID prefix.

```text
gs://{GCS_REVIEW_BUCKET}/reviews/{jobID}/
├── status.json   # progress, list metadata, run measurements (created on submission)
└── report.json   # the full review result (on success only)
```

The history list is built by enumerating the prefixes directly under `reviews/`, so running, failed and skipped
jobs appear too. Results are stored as JSON, and `/jobs/{jobID}` renders them.

---

## 🔄 Sequence diagram

```mermaid
sequenceDiagram
    autonumber
    actor User as User
    participant Web as Web role (public)
    participant Tasks as Cloud Tasks
    participant Worker as Worker role (private)
    participant Git as GitHub (SSH)
    participant Agent as ADK agent (Vertex AI)
    participant Store as GCS
    participant Slack as Slack

    Note over User, Slack: 1. Request a review
    User->>Web: POST /jobs (repository, branches, mode, model)
    Web->>Web: Validate and mint a job ID. Derive storage_uri and public_url
    Web->>Store: Record queued in status.json
    Note right of Web: **Before enqueueing.** The other way round could overwrite the worker's running
    Web->>Tasks: enqueue(ReviewRequest)
    Note right of Web: If enqueueing fails, the queued record is rolled back
    Web-->>User: 202 Accepted + Location: /jobs/{jobID}

    Note over User, Slack: 2. Run the review
    Tasks->>Worker: POST /tasks/execute-review (OIDC)
    Worker->>Store: Read status.json
    Note right of Worker: If succeeded, treat it as a redelivery and stop
    Worker->>Store: Record running
    Worker->>Git: Clone and take the base..head diff
    Note right of Worker: No diff means skipped; over MAX_DIFF_BYTES means failed
    Worker->>Worker: Fill the mode's prompt with the diff
    Worker->>Git: Check out head (working directory)
    Worker->>Agent: Prompt + tools (read_file / list_files / search_text)
    loop Tool calls (up to AGENT_MAX_TOOL_CALLS)
        Agent->>Worker: Read the working directory
        Worker-->>Agent: Files, listings, search results
    end
    Agent-->>Worker: Report (JSON conforming to OutputSchema)
    Note right of Worker: If the output was cut, keep only the completed findings and set truncated
    Worker->>Store: Write report.json
    Worker->>Slack: Notify (success links the detail page, failure names the step)
    Worker->>Store: Record succeeded / failed (decision, measurements, truncated)

    Note over User, Slack: 3. Read the result
    User->>Web: GET /jobs/{jobID}
    Web->>Store: Read status.json (and report.json)
    Web-->>User: Progress and report_url (JSON) / detail page (HTML)
    User->>Web: GET /jobs/{jobID}/report
    Web-->>User: The full findings
```

## 🌳 Project structure

```text
adk-review/
├── main.go                  # Entry point (builds the logger and starts the server)
├── Dockerfile               # An image holding only the static binary and GitHub's host keys
├── cloudbuild.yaml          # Builds and deploys to the 2 services
├── assets/                  # Embedded files (prompts/*.md, partials/*.md, templates/*.html, static/)
└── internal/
    ├── adapters/            # Git / Slack / result storage / prompts / Vertex AI client config
    ├── adkagent/            # ADK agent (llmagent + tools + output schema)
    ├── app/                 # Container (holds dependencies and their lifecycle)
    ├── builder/             # Assembly by SERVER_ROLE
    ├── config/              # Loading environment variables and per-role validation
    ├── domain/              # Models, storage conventions, port definitions, the worker route constant
    ├── giturl/              # Extracts the display path from a repository URL
    ├── pipeline/            # The worker body. Runner puts go-review-kit on worker.Lifecycle
    ├── repository/          # Reading and deleting the history on GCS
    └── server/              # chi router, graceful shutdown
        └── handlers/        #   Web role (request form, history, detail, deletion, mode list)
```

---

## 📜 License

* The bundled [Bootstrap](https://getbootstrap.com/) and [Bootstrap Icons](https://icons.getbootstrap.com/) are
  under the MIT License, each with its `LICENSE` next to it in `assets/static/vendor/`.
* This project is released under the [MIT License](https://opensource.org/licenses/MIT).
