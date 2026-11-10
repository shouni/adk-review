# CLAUDE.md

This file collects the assumptions for working in this repository and the invariants that reading the code
alone does not reveal. It follows the same conventions as the sibling apps in the fleet. The infrastructure is
defined in a separate infrastructure repository (Terraform), which is the source of truth.

## What this is

A web app that has an AI agent review the diff of a Git repository. An ADK for Go agent loop uses tools
(`read_file` / `list_files` / `search_text`) to investigate **beyond the diff** on its own before writing up
its findings.

The main subjects are article and novel manuscripts kept in Git and source code; the target is switched by
the modes in `assets/prompts/` (`article` / `novel` / `code`). **This is not a "code review tool".** Do not
bring code-centric assumptions into the UI wording or the vocabulary in the code.

## Commands

```bash
go build ./...
go vet ./...
go test ./... -race -cover
golangci-lint run ./...
gofmt -l .              # CI requires the output to be empty
```

`SERVER_ROLE=both` gives a local process both roles, but **it does not get as far as running a review.**
Cloud Tasks cannot deliver to localhost (`buildTaskEnqueuer` has no local escape hatch), and with
`max_attempts = 1` the request just disappears while queued. The README does not describe how to start it
either. Verify with tests.

## Conventions

- **Code comments, doc comments, error and log messages, and test assertion messages are English** (moved
  2026-11-10; do not mix Japanese back in). Error strings follow Go style: lowercase, no trailing
  punctuation (ST1005). Subtest names (`t.Run` names, table `name` fields, map keys used as subtest names)
  are English too; Go test function names were left as they were.
- **These stay Japanese:** end-user UI text (visible template text, JS UI strings, the badge labels in
  `handlers/render.go`), messages returned to the user by the web UI and the JSON API (`validate.go`,
  `readSubmitRequest`, the `respond.Error*` bodies, form notices), the Slack notification text, and
  everything sent to the model: the prompts in `assets/prompts/` and `assets/partials/`, the agent's
  instruction (`instructionTemplate`), the tool descriptions, the schema descriptions, and the tool results
  returned to the model (`budgetExhaustedMsg`, the tools' `Error` field, `toolbox.resolve`'s errors).
  Translating them changes the agent's behaviour and the language of the review output. Test fixtures,
  expected outputs and strings compared at run time stay as they are. The one worker error worded for the
  user (`adkagent`'s "output cut off, nothing to salvage, narrow the range and rerun") also stays Japanese,
  because it is what the user reads in Slack and the history.
- **Do not hard-code go-review-kit's step names** (`review.Step*`) in either language; compare against the
  constants (as `internal/adapters/slack_test.go` does). The library owns their text.
- `README.md` is English and `README.ja.md` is its Japanese version. Update the two together.

## Key invariants

Rules that do not fit in a code comment, or that span several files and cannot be expressed in one place.
**When you change one, update its reasoning too.**

### The three-tier timeout

If the order `PIPELINE_TIMEOUT` < `TASK_DISPATCH_DEADLINE` <= Cloud Run's `timeout` breaks, **Cloud Tasks
cuts the request first, the process dies by SIGTERM, and neither a failure report nor a Slack notification is
left.** `review-queue` has `max_attempts = 1`, so no retry comes either and the task silently vanishes (the
history row stays queued, so to the user it looks like "the review never finishes").

- `config.validateTimeouts` checks the relation of the first two tiers at startup.
- The third tier is invisible to the app, so a `precondition` in the infrastructure repository covers it.
- **This is the only app in the fleet with all three tiers short.** A measured review is orders of magnitude
  shorter than video generation. The limit is not "the target for the normal case" but "a net that catches
  hangs".
- An agent review grows with the number of tool calls. When measurements approach the limit, raise
  `TASK_DISPATCH_DEADLINE` (**it is an env var, so no rebuild is needed**; gcp-kit owns the range check).
- **The app keeps no default for the tier numbers (`PIPELINE_TIMEOUT` / `TASK_DISPATCH_DEADLINE`).** Their
  single source is the infrastructure repository; a default in the app would put the same number in two
  places, and a missing setting would run on "a value nobody chose". **Do not copy the check of the range
  Cloud Tasks accepts (15 seconds to 30 minutes) into the app.** gcp-kit's `tasks.NewEnqueuer` checks it at
  construction; a copy puts the same constraint in two places (the copy that once existed actually lacked the
  lower bound).

### The diff limit has a default in the app

A diff over `MAX_DIFF_BYTES` (default 320 KiB) fails without being sent to the AI
(`pipeline.WithMaxDiffBytes` -> `review.ErrDiffTooLarge`). **Unlike the three tiers above, the app keeps a
default for this number.** The tiers have no default because the same number would appear in two places with
the infrastructure repository; this limit has no infrastructure counterpart. If unset meant unlimited, the
most expensive failure — failing on truncated output after the model call has finished — would become the
default.

- **Do not truncate and send.** The model returns a report *as a success* having seen only part of the diff,
  so a degraded review is kept as an artifact. That is harder to notice than a failure.
- **This is not a broad net.** Output balloons mainly from the model running away, not from the diff's size;
  a 10 KiB diff once ended mid-response at 217 KB. In measurements (30 days) this limit catches 1 of 4
  failures.
- To tighten or loosen it, the material is `metrics.diff_bytes` in the history.

### Keep a cut-off result as a partial

**Hitting the output limit (64Ki tokens) is not a total loss.** `review.ParseReport` picks up the completed
part and reports it in `ParseInfo.Truncated`. `adkagent` passes it to `review.RunInfo`, and `pipeline.Runner`
on to `JobStatus.Truncated`. **Do not turn `FinishReasonMaxTokens` into a failure there.** Going back would
lose a completed Blocker finding along with everything else (one was actually lost). It is an error only when
nothing could be picked up.

- **Do not remove the partial marking from the screen and the JSON API.** The result is saved, but the
  findings are not all there is. Without the marking, the reader takes it as "a review with 2 findings" and
  loses any way to know there was more (the `✂ 部分` badge and the note on the detail page).
- This is the same reasoning as opposing truncation of the diff. As long as the one rule **never present a
  partial as complete** holds, picking it up is a benefit.

### The web role's JSON API switches on Accept

**Screens and machines do not get separate routes.** The same handler returns JSON on
`Accept: application/json` (`negotiate.WantsJSON`). Separate routes would mean two copies of the same fetch,
and fixing only one would make the screen and the machine-readable result disagree.

- `POST /jobs` accepts a JSON body too, but **does not decode `domain.ReviewRequest` directly** (it goes
  through `submitInput`). Letting callers decide `StorageURI` would let them have artifacts written to any
  path in the bucket. Unknown fields are an error rather than silently dropped (dropped, the sender believes
  they took effect).
- **`jobstatus.ErrNotFound` is a 404, `ErrUnavailable` a 502** (`recordErrorStatus`). Conflated, a revoked
  permission or a storage outage would come back as "no such review", and callers would give up where they
  should retry.
- **A job is named by `/jobs/{jobID}` alone** (the public-docs URL naming convention). Its JSON is just the
  progress and `report_url`; the full findings are `GET /jobs/{jobID}/report`. Returning the full text on
  every completion check would be too heavy.
- **The session data lives in Firestore; the cookie carries only an opaque ID.** So there are no session keys
  (`SESSION_SECRET` / `SESSION_ENCRYPT_KEY`) any more. The store is `SESSION_FIRESTORE_DATABASE` (default
  `sessions`) and **names a database separate from the job status one.** A database name is an identifier
  that cannot be changed later, so sharing one would leave its name wrong for one of the two uses.
  - **A TTL policy (`expiresAt`) is a required infrastructure setting.** Unlike a cookie, stored data never
    removes itself, so without it sessions pile up forever. The read side checks the expiry too, so a delayed
    TTL never lets an expired session through (gcp-kit's responsibility).
  - Tests use `session.NewMemoryStore`. **It is in-process, so it cannot be used in production** (every Cloud
    Run instance change would log everyone out, and with one instance during development nobody would
    notice).
  - `loggedInCookie` has `Handler.IssueSession` bake the cookie. **Do not build a store by hand.** The data
    lives on the handler's side, so it could not be read (it used to rebuild a CookieStore with the same key,
    which was a copy of gcp-kit's internal format).
- Authentication is `auth.Protected(M2M, Auth)`: browsers use a session + CSRF, servers an OIDC Bearer token.
  **`CrossOriginProtection` lets calls without the headers through** (Go's behaviour: with neither
  `Sec-Fetch-Site` nor `Origin` it assumes a non-browser). If that changed, only submissions from the MCP
  gateway would get 403 while the screens keep working, which is hard to notice, so
  `TestCrossOriginProtection_AllowsHeaderlessPost` pins it.
  - The Bearer path skipping session and CSRF is deliberate. CSRF defends against attacks abusing cookies
    being sent automatically; it does not apply to server-to-server calls that attach a token explicitly.
    Callers are restricted by `ALLOWED_M2M_SERVICE_ACCOUNTS` instead.

### Record the acceptance before enqueueing; stop a redelivery at succeeded

`HandleJobCreate` writes queued to `status.json` and then enqueues to Cloud Tasks
(`internal/server/handlers/job_create.go`). **Do not swap the order.** Cloud Tasks delivers within tens of
milliseconds, so the other way round allows "the worker writes running -> web overwrites it with queued", and
a running job looks stuck as accepted in the history (the order that actually happened in a sibling app).
When enqueueing fails, `discardQueued` rolls the record back, so jobs that could not be enqueued do not stay
in the history.

- The worker's `Runner.begin` reads the previous record once; if it is succeeded, it treats the task as a
  redelivery, does not run, and returns success. Cloud Tasks delivers at least once, so returning an error,
  for example because a notification failed, would redeliver the same task and pay for the AI calls twice.
- **It does not run when the record cannot be read either.** It used to proceed, reasoning that "running
  twice is easier to recover from than stopping because the read failed", but then the guard itself paid for
  rebuilding finished work, so, following the convention, it is left to a person resubmitting.
- Failures caused by the input itself (`review.ErrInvalidRequest` / `ErrEmptyDiff` / `ErrDiffTooLarge` /
  `ErrRefNotFound` / `ErrUnsupportedRepoURL`) are wrapped in `worker.ErrPermanent` by `classifyForRetry`,
  which stops the redelivery with a 2xx. Unwrapped, a request that fails at the same place would be
  redelivered and the user would receive the same failure notification twice.

### Cloud Tasks OIDC

The issuer and the allowlist never share one variable. Even with the same value, the meaning inverts per role
and becomes unreadable (the infrastructure repository's "Cloud Tasks OIDC" convention).

- `ALLOWED_M2M_SERVICE_ACCOUNTS`: the SAs that may call **the web role's JSON API** (the MCP gateway). They are
  neither the issuer nor the target but "callers from other services", so they share nothing with the two
  below. **Empty means startup fails.** `auth.Protected` cannot disable M2M; unset, the path stays alive,
  verification always fails and falls back to session authentication. A missing setting therefore shows up
  only as "the browser works normally but the MCP gateway alone receives the login page's HTML". There is no
  way to tell deliberate disabling from a missing setting, so empty is treated as the latter (it used to be
  the one place that let it through, unlike the 4 sibling apps).
- `TASK_CALLER_SERVICE_ACCOUNT_EMAIL`: the caller SA set by **the submitting side (web)**. Cloud Tasks, not
  this process, generates and attaches the token.
- `ALLOWED_TASK_SERVICE_ACCOUNTS`: the allowlist of issuers accepted by **the receiving side (worker)**. web
  and worker run as separate SAs, so this lists "someone else's (the web role's) SA". Empty makes the verifier
  fail closed and every task fail, so it is required on the worker.

### Fail at startup

**A missing setting stops startup.** Let through, the failure comes after a user submits the form, and at the
save step after the Gemini call at that — failing after the most cost has been paid.

- **Do not put placeholder defaults on** `GCP_PROJECT_ID` / `GCS_REVIEW_BUCKET` / `SERVICE_URL` /
  `GEMINI_MODELS`. In particular, if `SERVICE_URL` went back to `http://localhost:8080`, localhost would be
  exempt from the HTTPS check as a development host name and a missing production setting would slip through.
- `GEMINI_MODELS` has no default in the app because model IDs age on Google's release cycle, not on this
  repository's. With a default, nobody would notice "it keeps pointing at an old model although nobody
  changed the deployment settings".
- The worker role does not require OAuth settings (`GOOGLE_CLIENT_*` / `ALLOWED_EMAILS` / `ALLOWED_DOMAINS` /
  `ALLOWED_M2M_SERVICE_ACCOUNTS`). Requiring them would defeat the point of separate services per role
  (keeping the worker's env minimal and not handing out access to credentials it does not use).
- `builder.AppHandlers.Validate` fails a setup where roles and handlers do not match. Without it `/health`
  still passes, so **the deploy counts as a success and only the submission path is broken** — the least
  noticeable shape.
- Format errors in Durations or integers are an error in `LoadConfig` rather than silently falling back to the
  default.

### What a tool returns is billed until the end of the run

The limits are gathered in `internal/adkagent/tools.go` (the body of each tool is in `tool_read.go` /
`tool_list.go` / `tool_search.go`). They are not only there to protect a single call. **What a tool returns
stays in the conversation and is re-read on every later turn.** 16 KB returned by the 4th call is billed for
the next 10 turns.

- That is why `read_file` has `from` / `lines` and `search_text` has `context`. **Encourage ranges both in the
  tool descriptions (the Description in `newTools`) and in the code of conduct.** With only one, the model
  goes back to reading whole files.
- `read_file` returns `total_lines` so a model reading a range can judge "is this everything". Without it, the
  model reads part of a file and writes findings as if it had read all of it.
- In a measurement (2026-08-25, 10.6 KiB diff, 13 tool calls), the 62 KB returned in total was about 70% of
  the final prompt's 30.6k tokens. Of the 209 seconds taken, tool execution itself accounted only for the
  3-4 seconds between calls. **The slowness is the model's turns, and their length tracks the size of the
  context.**

### Cut the tool budget by count, not time

`AGENT_MAX_TOOL_CALLS` (default `adkagent.DefaultMaxToolCalls` = 32) is the limit on the number of tool calls.
A review must fit within the Cloud Tasks dispatch deadline, but the model's thinking time cannot be predicted
while the number of tool calls can be estimated at design time, so it is cut by count rather than time. The
aim is to steer the model towards "wrap up the investigation and write the final answer" instead of failing on
a missed deadline.

- **At 80% of the limit (`wrapUpAfter`) the model is told "start no new investigation, wrap up with what you
  have".** Aiming at the limit itself means wrapping up only after the last call is spent, and the run is cut
  off holding a half-verified hypothesis. It returns at least 1 even for a tiny budget.
- `instructionFor` fills the numbers in the code of conduct (`instructionTemplate`) from the actual budget.
  **Do not write them in by hand.** If the budget changed while the instruction kept quoting old numbers, the
  model would keep investigating as if it had 3 calls left and be cut off unexpectedly by
  `budgetExhaustedMsg`.
- Running out is not a Go error but a normal return value carried in each tool's `Error` field. As an error,
  the framework treats it as an error event and the model cannot choose a recovery (try another path, give up
  and go to the final answer). Context cancellation is the one exception (`cancelErr`): the run itself is
  over, and returning partial results as a normal value could not be told apart from "searched and found
  nothing".
- How many were actually used stays in `metrics.tool_calls`. Judge whether the limit is right by how many each
  run used, not by whether it ran out.

### Tools never leave the working directory

The reviewed repository is external input. A symlink in it may point at any file on the host, so
`filepath.Clean` is not enough.

- `toolbox.resolve` resolves both the root (already `filepath.EvalSymlinks`ed at construction) and the
  requested path to their real paths and checks that the latter is under the root. Resolving only one side
  lets an OS-level symlink such as macOS's `/var` -> `/private/var` shift the base.
- `list_files` and `search_text` handle nothing but regular files (no symlinks, FIFOs or devices).
  `os.ReadFile` follows symlinks, so letting them through `search_text` would put the content of files outside
  the root into the model's input. Listing them in `list_files` would show the model "files that should be
  readable but are not".
- `.git` is excluded from walks.

### The app builds the output schema

`internal/adkagent/schema.go` holds the structured output schema.

- Enumerated values use `review.SeverityStrings` / `review.DecisionStrings` (from the library) directly.
  **Do not repack them into a `[]string` in the app.** With more copies, adding a value makes the schema and
  validation disagree, and the model returns a value valid by the schema only to be rejected on decode (the
  symptom: every review fails). A drift-detection test exists.
- **`findings`'s `MaxItems` (20) and `excerpt`'s `MaxLength` guard against the 64Ki-token output limit.**
  Hitting it loses everything, including JSON that was correct up to that point (a 10.7 KiB diff once got
  212 KiB written and was cut, losing a completed Blocker finding). 20 is not a target of "this many findings
  are fine" but a net against runaways (the most measured is 2). **Do not write the same number into the
  instruction by hand either;** it is filled from `maxFindings`. If they drift, the model trusts the
  instruction and starts writing more findings than the schema allows. Length constraints are not enforced as
  strictly as `Enum`, so check in `response_bytes` whether they work.
- `findings[].evidence` makes the agent declare "where it looked to decide", which means something because it
  actually investigates the working directory. **Do not have a reviewer that sees only the diff produce the
  same field.** Asking for grounds with no means to verify them invites fabrication (a prompt asking a
  reviewer without any means of checking for evidence was actually wired up once).

### Shared fragments are prompt-kit partials

`assets/partials/*.md` are not pasted in as strings; they join the same set as the bodies under `_`-prefixed
template names and are referenced with `{{template "_finding_policy" .}}` (`assets.PromptTemplates`).
`prompts.WithTrimPartials` drops the trailing newline. **Do not `TrimRight` by hand.** Fragments are inserted
in the middle of bullet lists, and a leftover trailing newline splits the list at that point.

### Only 3 files may import genai

Only these may use `google.golang.org/genai` directly (their tests aside). **Do not add more.**

- `internal/adkagent/reviewer.go` — the client config handed to ADK's model layer
- `internal/adkagent/schema.go` — the structured output schema
- `internal/adapters/ai.go` — building the `genai.ClientConfig` (the entry point to Vertex AI)

The fleet's other apps never touch the AI SDK directly and call through `go-gemini-client` (the ecosystem's
convention "keep the genai SDK inside `go-gemini-client`"). **This repository is an exception because ADK
requires genai directly, not because the convention was dropped.** Do not take genai anywhere ADK does not
require it. `go-review-kit` itself knows nothing of the AI SDK.

### AI only through Vertex AI

The API key path (`GEMINI_API_KEY`) is not wired. On Cloud Run the runtime SA's `roles/aiplatform.user`
authenticates, so handing out a key would hand out access to a secret nobody uses. Locally, ADC is needed.

Gemini's location is fixed in `adapters.geminiLocationID` (`global`) and is **a different thing** from
`GCP_LOCATION_ID` (the region of the Cloud Tasks queue). Confusing them either loses the queue or points
Vertex at a region that does not exist.

### The worker route is defined in one place

Both the submitter (`internal/builder`) and the receiver (`internal/server`) use `domain.WorkerTaskPath`.
With two literals, changing only one makes **every submitted task a 404, never retried and silently lost.**

### The library owns the per-review deadline

`internal/builder/pipeline.go` passes `PIPELINE_TIMEOUT` to `pipeline.WithRunTimeout`. **Do not put your own
deadline on the context passed to `Run`.** It would sit outside the detachment the library does for publishing
and notifying, and the failure notification would be lost together with the cut-off (do not go back to the
shape where the ACL did `context.WithTimeout`; `worker.Lifecycle.Timeout` stays 0 for the same reason).

### Logs ride on the context

Logging goes through `slogctx`, so the `job_id` / `mode` attached at the start of `Execute` appear on every
later output. Do not add `"job_id", req.JobID` to individual calls (it would be a duplicate key). `cloudlog`
maps the level to Cloud Logging's `severity`, so if the logger assembly (`main.go`) went back to a plain
`slog.NewJSONHandler`, **`slog.Error` would no longer reach alerting.**

### Measurements are kept for failed runs too

`Runner.buildOutcomeStatus` writes the diff size, duration, token usage and number of tool calls to
`status.json` for failed runs too. The material for judging whether a limit (`MAX_DIFF_BYTES` /
`AGENT_MAX_TOOL_CALLS` / output tokens) is too strict lies with the rejected runs rather than the ones that
passed. Before this existed, deciding a limit meant digging through Cloud Logging and matching log lines by
hand.

### One prefix per job; results stay JSON

`domain.StorageLayout` puts `status.json` and `report.json` under `gs://{GCS_REVIEW_BUCKET}/reviews/{jobID}/`.

- **The list metadata (`status.json`) and the full result (`report.json`) are separate.** The list reads
  `status.json` once per row, so putting the full findings in the same file would make the reads grow with the
  number of findings.
- **Keeping the status file next to the artifacts is deliberate.** The history list is built by enumerating
  the prefixes directly under `reviews/`, so running, failed and skipped jobs appear as they are. For reviews,
  "requested but missing from the history" is the worse problem.
- **No rendered HTML is stored.** Results are saved as JSON and `/jobs/{jobID}` displays them. Stored HTML
  served by a signed URL could be read bypassing the app's authentication, and the look of the same content
  would split from the detail page into two. Slack notifications link the detail page rather than a signed
  URL for the same reason.
- **Deletion is a walk of the prefix** (`remoteio.DeletePrefix`). The deleter need not know "what the job
  made", and new kinds of artifact need no change to deletion. The library rejects the bucket root (so a wide
  range of the bucket is never deleted). There used to be a hand-written copy here requiring a trailing `/`,
  but `List` normalises the prefix, so it is not needed.
- **queued / running cannot be deleted** (`JobStatus.Deletable`). After the deletion the worker would write
  `status.json` back, reviving the prefix and leaving an empty row in the history.

### SSH host keys are fetched at build time

The `Dockerfile` fetches the host keys from GitHub's API, bakes them into `/etc/ssh/ssh_known_hosts`, and
points `SSH_KNOWN_HOSTS` at it. **Do not create a switch that disables host key verification.** The keys are
not pinned in the repository because failing to follow a rotation would break every clone, and it would first
be noticed in production. To support hosts other than GitHub, add them in the same file, but the form and the
API accept only URLs of the form `git@github.com:owner/repo.git` (`handlers.repoURLPattern`).

### IAM: one SA per role, permissions per resource

web and worker run as separate SAs (the infrastructure repository's "one workload, one SA" convention). The
README does not list role names because the granularity varies by environment; it only describes how to
decide.

- GCS uses `objectUser` per bucket. `objectAdmin` also allows manipulating object ACLs, and granted at the
  project level it reaches unrelated buckets.
- Secrets are granted per secret. At the project level they would reach every secret.
- The web SA needs read/write on GCS because the web role itself lists, shows and deletes history and writes
  the acceptance records. Permission to enqueue alone is not enough.

## Architecture

Hexagonal (Ports and Adapters). The same image is split into web / worker by `SERVER_ROLE` and deployed as
separate Cloud Run services. `both` is for local development only.

```text
internal/
  adkagent/   ADK agent (llmagent + tools + output schema) * the only direct genai dependency
  adapters/   Git / Slack / storage / prompts
  app/        Container (holds dependencies and their lifecycle)
  builder/    Assembly by SERVER_ROLE
  config/     Environment variables, defaults, startup validation
  domain/     Models, storage conventions, port definitions, the worker route constant
  giturl/     Parsing repository URLs
  pipeline/   The worker's entry point. The ACL putting go-review-kit on gcp-kit/worker.Lifecycle (the public-docs worker convention)
  repository/ Reading the history on GCS
  server/     HTTP server, routing, handlers
```

Artifacts are `status.json` and `report.json` under `gs://{GCS_REVIEW_BUCKET}/reviews/{jobID}/`. The list is
built by enumerating prefixes, so running, failed and skipped jobs appear too.

## Notable external dependencies

Many are in-house kits, and **much of the behaviour lives in them.** When behaviour is unclear, check
`go doc` or the source in the module cache.

- `go-review-kit`: the review procedure (diff -> prompt -> review -> publish -> notify)
- `go-job-kit`: progress records (`jobstatus`), history paging (`paging`), the ID cache, prefix walks
  (`joblist`)
  - `jobstatus.ErrNotFound` (not recorded) and `ErrUnavailable` (unreadable) are **deliberately different.**
    Conflated, an outage would show up as "a 200 full of empty rows".
  - `paging.LoadPage` does not return load errors to the caller; it drops the row and continues. To surface
    an outage, catch it yourself, as `repository.loadFailure` does.
  - The numeric and boolean tags of `domain.JobStatus`, which embeds `jobstatus.Status`, are `omitzero`.
    `status.json` is written by `jobstatus.Store`, which uses `encoding/json/v2`, where `omitempty` does not
    drop 0 and false. Strings can stay `omitempty`.
  - Leave prefix walks to `joblist.Collect`. Writing the same walk by hand loses deduplication and the
    prefix's trailing-slash fix (it actually happened once).
- `gcp-kit`: OAuth, CSRF, Cloud Tasks submission and OIDC verification, `cloudlog`
  - `session.Handler.Authenticate` verifies CSRF for state-changing methods. Having no verification code in the
    app is normal.
- `go-utils`: job ID minting (`jobid`), JST formatting (`jst`), `slogctx`
- `netarmor/securenet`: SSRF protection and judging whether `SERVICE_URL` is safe
