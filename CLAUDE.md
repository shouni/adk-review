# CLAUDE.md

このファイルは、このリポジトリで作業するときの前提と、コードを読んだだけでは分からない
不変条件をまとめたものです。同じフリートの兄弟アプリと同じ規約に従います。
インフラの定義は別のインフラ管理リポジトリ（Terraform）が正です。

## What this is

Git リポジトリの差分を AI エージェントにレビューさせる Web アプリです。ADK for Go の
エージェントループでツール（`read_file` / `list_files` / `search_text`）を使わせ、
**差分の外**を自分で調べさせてから指摘をまとめさせます。

主用途は Git 管理下の記事・小説の原稿とソースコードで、対象は `assets/prompts/` の
モード（`article` / `novel` / `code`）で切り替えます。**「コードレビューツール」ではありません。**
UI の文言やコード中の語彙に code 前提のものを持ち込まないでください。

## Commands

```bash
go build ./...
go vet ./...
go test ./... -race -cover
golangci-lint run ./...
gofmt -l .              # CI は出力が空であることを要求します
```

`SERVER_ROLE=both` でローカルに両面を持たせられますが、**レビューの実行までは通りません。**
Cloud Tasks は localhost へ配送できず（`buildTaskEnqueuer` にローカル用の抜け道はありません）、
`max_attempts = 1` なので依頼は queued のまま消えます。README にも起動手順は載せていません。
確認はテストで行ってください。

## Key invariants

コードコメントに書ききれない、あるいは複数ファイルにまたがるため 1 箇所では表現できない
決まりごとです。**変更するときは理由ごと更新してください。**

### タイムアウトの三段

`PIPELINE_TIMEOUT` < `TASK_DISPATCH_DEADLINE` <= Cloud Run の `timeout` の順序が崩れると、
**Cloud Tasks が先にリクエストを打ち切り、プロセスは SIGTERM で落ちて失敗レポートも
Slack 通知も残りません。** `review-queue` は `max_attempts = 1` なので再試行も来ず、
タスクは黙って消えます（履歴の行は queued のまま残るので、利用者からは
「レビューが一生終わらない」ように見えます）。

- 上二段の関係は `config.validateTimeouts` が起動時に検査します。
- 三段目はアプリから見えないため、インフラ管理リポジトリの `precondition` が受け持ちます。
- **フリートで唯一、三段とも短く取ってあります。** レビュー 1 件の実測が動画生成とは
  桁違いに短いためです。上限は「正常系の目標」ではなく「ハングを捕まえる網」です。
- エージェントレビューはツール呼び出しの回数だけ伸びます。実測が近づいたら
  `TASK_DISPATCH_DEADLINE` を伸ばしてください（**env なので再ビルドは不要です**。
  範囲の検査は gcp-kit が持っています）。
- **三段の数字（`PIPELINE_TIMEOUT` / `TASK_DISPATCH_DEADLINE`）はアプリに既定値を置きません。** 出どころはインフラ管理リポジトリ 1 箇所で、
  アプリが既定を持つと同じ数字が 2 箇所に現れ、設定漏れが「誰も選んでいない値」で
  動いてしまいます。**Cloud Tasks が受け付ける範囲（15 秒〜30 分）の検査を
  アプリ側へ写さないでください。** gcp-kit の `tasks.NewEnqueuer` が構築時に見ており、
  写すと同じ制約が 2 箇所に現れます（実際、写した側は下限が抜けていました）。

### 差分の上限はアプリが既定値を持つ

`MAX_DIFF_BYTES`（既定 320 KiB）を超える差分は、AI へ送らずに失敗させます
（`pipeline.WithMaxDiffBytes` → `review.ErrDiffTooLarge`）。**上の三段と違い、この数字は
アプリに既定値を置きます。** 三段が既定を持たないのは同じ数字がインフラ管理リポジトリと
2 箇所に現れるからで、この上限にインフラ側の対応物はありません。未設定を無制限にすると、
いちばん高くつく壊れ方——モデルを呼び終えてから出力の途中切れで失敗する——が既定になります。

- **切り詰めて送らないでください。** モデルは差分の一部だけを見たまま *成功として* レポートを
  返すので、質の落ちたレビューが成果物として残ります。落ちるより気付きにくい形です。
- **これは効き目の広い網ではありません。** 出力が膨らむ主因は差分量ではなくモデルの暴走で、
  10 KiB の差分が 217 KB の応答で途中終了した実例があります。実測（30 日）では失敗 4 件のうち
  この上限が捕まえるのは 1 件です。`adkagent` の `FinishReasonMaxTokens` 判定と両方が要ります。
- 締めるにも緩めるにも、材料はログの `diff_bytes` です（`AIレビューを実行します` の行）。

### web 面の JSON API は Accept で切り替える

画面と機械で**ルートを分けません。** `Accept: application/json` を見て同じハンドラが
JSON を返します（`handlers.wantsJSON`）。分けると同じ取得処理が 2 本になり、片方だけ
直したときに画面の表示と機械可読な結果が食い違います。

- `POST /submit_review` は JSON body も受け付けますが、**`domain.ReviewRequest` を直接
  デコードしません**（`submitInput` を挟みます）。呼び出し元に `StorageURI` を決めさせると、
  成果物をバケット内の任意のパスへ書かせられます。知らない項目は黙って捨てず
  エラーにします（捨てると、送った側が効いたと思い込みます）。
- **`jobstatus.ErrNotFound` は 404、`ErrUnavailable` は 502 です**（`recordErrorStatus`）。
  同一視すると、権限剥奪やストレージ障害が「そんなレビューはありません」として返り、
  呼び出し元は再試行すべき場面で諦めます。
- `GET /jobs/{jobID}` と `GET /history/{jobID}` を分けているのは、後者が指摘の全文を
  含むためです。完了検知のたびに全文を返すのは重すぎます。
- 認証は `Auth.ProtectedMiddleware(M2M)` で、ブラウザはセッション + CSRF、サーバー間は
  OIDC Bearer です。**`CrossOriginProtection` はヘッダーを持たない呼び出しを素通しします**
  （Go の仕様。`Sec-Fetch-Site` も `Origin` も無ければ非ブラウザとみなす）。ここが変わると
  ap-mcp からの投入だけが 403 になり、画面は動いたままなので気付きにくいため、
  `TestCrossOriginProtection_AllowsHeaderlessPost` で固定しています。

### Cloud Tasks の OIDC

発行元と許可リストを 1 つの変数で兼ねません。同じ値でも役割ごとに意味が反転して
読めなくなるためです（インフラ管理リポジトリの規約「Cloud Tasks の OIDC」）。

- `ALLOWED_M2M_SERVICE_ACCOUNTS`: **web 面の JSON API** を呼べる SA（ap-mcp）。
  発行元でも投入先でもなく「他サービスからの呼び出し元」なので、上の 2 つと兼ねません。
  空なら M2M 無効です（web 面は画面が使えれば成立するため、fail-closed にしません）。
- `TASK_CALLER_SERVICE_ACCOUNT_EMAIL`: **投入側（web）** が指定する caller SA。
  トークンを生成して付与するのは Cloud Tasks であって、このプロセスではありません。
- `ALLOWED_TASK_SERVICE_ACCOUNTS`: **受信側（worker）** が受け付ける発行元の許可リスト。
  web と worker で実行 SA を分けているので、ここには「他人（web 面）の SA」が並びます。
  空だと検証器が fail-closed になり全タスクが失敗するため、worker では必須です。

### 起動時に落とす、を守る

**設定漏れは起動時に止めます。** 通してしまうと、失敗するのは利用者がフォームを送ったあと、
しかも Gemini の呼び出しを終えた保存の段階になります。いちばんコストを払ったあとで
落ちる順序です。

- `GCP_PROJECT_ID` / `GCS_REVIEW_BUCKET` / `SERVICE_URL` / `GEMINI_MODELS` に
  **プレースホルダの既定値を置かないでください。** 特に `SERVICE_URL` を
  `http://localhost:8080` に戻すと、localhost は開発用ホスト名として HTTPS 検査を
  免除されるため、本番の設定漏れが素通りします。
- 役割とハンドラが噛み合わない構成は `builder.AppHandlers.Validate` が落とします。
  ここを外すと `/health` は通るので**デプロイは成功扱いになり、壊れているのは投入経路だけ**
  という、いちばん気付きにくい形になります。
- Duration や整数の書式エラーは既定値へ黙って落とさず、`LoadConfig` がエラーにします。

### 出力スキーマはアプリ側で組み立てる

`internal/adkagent/schema.go` が構造化出力のスキーマを持ちます。

- 列挙値は `review.SeverityStrings` / `review.DecisionStrings`（ライブラリ）を直接使います。
  **アプリ側で `[]string` へ詰め替え直さないでください。** 写しが増えると、値を足したときに
  スキーマと検証が食い違い、モデルはスキーマ上正当な値を返すのにデコードで弾かれます
  （症状は全レビューの失敗）。ドリフト検知テストがあります。
- **`findings` の `MaxItems`（20）と `excerpt` の `MaxLength` は、出力の 64Ki トークン対策です。**
  上限に当たると、途中まで正しく書けていた JSON ごと全損になります（10.7 KiB の差分に
  212 KiB を書いて切れ、完成していた Blocker の指摘ごと失われた実例があります）。
  20 は「そこまで指摘してよい」という目標ではなく、暴走を止める網です（実測の最大は 2 件）。
  **同じ数字を instruction にも直書きしないでください。** `maxFindings` から埋めます。
  ずれると、モデルは指示のほうを信じてスキーマを超える件数を書き始めます。
  長さの制約は `Enum` ほど硬く強制されないので、効いているかは `response_bytes` で見ます。
- `findings[].evidence` は、エージェントが作業ディレクトリを実際に調べるため
  「どこを見て判断したか」を自己申告させるものです。**差分しか見ないレビュアーに
  同じ項目を出させないでください。** 確認手段が無いまま根拠を求めると捏造を促します
  （確認する手段の無いレビュアーへ evidence を求めるプロンプトを、実際に配線していた
  ことがあります）。

### 共有断片は prompt-kit の partial として持つ

`assets/partials/*.md` は文字列として流し込まず、`_` 付きのテンプレート名で本文と同じ
集合に入れて `{{template "_finding_policy" .}}` で参照します（`assets.PromptTemplates`）。
末尾改行は `prompts.WithTrimPartials` が落とします。**自前で `TrimRight` しないでください。**
断片は箇条書きの途中に差し込まれるので、末尾改行が残るとそこだけリストが分かれます。

### genai を import してよい場所は 3 ファイルだけ

`google.golang.org/genai` を直接使ってよいのは次だけです。**増やさないでください。**

- `internal/adkagent/reviewer.go` — ADK のモデル層へ渡すクライアント設定
- `internal/adkagent/schema.go` — 構造化出力スキーマ
- `internal/adapters/ai.go` — `genai.ClientConfig` の組み立て（Vertex AI の投入口）

フリートの他のアプリは AI SDK を直接触らず `go-gemini-client` 経由で呼びます
（エコシステムの規約「genai SDK は `go-gemini-client` の外に出さない」）。
**このリポジトリが例外なのは ADK が genai を直接要求するからで、規約を捨てたからでは
ありません。** ADK が要求しない場所へ genai を持ち出さないでください。
`go-review-kit` 自体は AI SDK を知りません。

### AI は Vertex AI 経由のみ

API キー経路（`GEMINI_API_KEY`）は配線していません。Cloud Run では実行 SA の
`roles/aiplatform.user` で認証できるため、キーを配ると使われないシークレットへの
アクセス権を配ることになります。ローカルでは ADC が要ります。

Gemini のロケーションは `adapters.geminiLocationID`（`global`）に固定で、
`GCP_LOCATION_ID`（Cloud Tasks のキューのリージョン）とは**別物です**。混同すると、
キューを見失うか Vertex が存在しないリージョンを指すかのどちらかになります。

### ワーカーのルートは 1 箇所で定義する

`domain.TaskExecuteReviewPath` を投入側（`internal/builder`）と受信側（`internal/server`）の
両方が使います。リテラルを二重に持つと、片方だけ変えたときに**投入したタスクが全部 404 に
なり、再試行もされず黙って消えます。**

### レビュー 1 件の締切はライブラリに持たせる

`internal/builder/pipeline.go` が `pipeline.WithRunTimeout` へ `PIPELINE_TIMEOUT` を渡します。
**`Run` へ渡す context に自前で締切を被せないでください。** ライブラリが公開・通知のために
行う切り離しより外側に掛かるため、打ち切りと同時に失敗通知まで落ちます
（ACL 側で `context.WithTimeout` していた頃の形には戻さないこと）。

### ログは context に載せる

`slogctx` を通しているので、`Execute` の冒頭で載せた `job_id` / `mode` が
以降のすべての出力に付きます。個々の呼び出しで `"job_id", req.JobID` を書き足さないでください
（重複キーになります）。`cloudlog` が level を Cloud Logging の `severity` へマップするため、
ロガーの組み立て（`main.go`）を素の `slog.NewJSONHandler` に戻すと
**`slog.Error` がアラートに乗らなくなります。**

## Architecture

ヘキサゴナル（Ports and Adapters）。同じイメージを `SERVER_ROLE` で web / worker に分け、
別々の Cloud Run サービスとしてデプロイします。`both` はローカル開発専用です。

```text
internal/
  adkagent/   ADK エージェント（llmagent + ツール + 出力スキーマ）※ genai 直接依存はここだけ
  adapters/   Git / Slack / 保存 / プロンプト / パイプライン ACL
  app/        Container（依存の保持とライフサイクル）
  builder/    SERVER_ROLE に応じた組み立て
  config/     環境変数・既定値・起動時検証
  domain/     モデル、保存先の規約、ポート定義、ワーカーのルート定数
  giturl/     リポジトリ URL の解析
  repository/ GCS 上の履歴の読み取り
  server/     HTTP サーバー、ルーティング、ハンドラ
```

成果物は `gs://{GCS_REVIEW_BUCKET}/reviews/{jobID}/` 配下に `status.json` と `report.json`。
一覧はプレフィックスの列挙で作るため、実行中・失敗・スキップも並びます。

## Notable external dependencies

自作キットが多く、**振る舞いの多くはそちら側にあります。** 挙動が読めないときは
`go doc` かモジュールキャッシュのソースを当たってください。

- `go-review-kit`: レビューの手順（差分取得 → プロンプト → レビュー → 公開 → 通知）
- `go-job-kit`: 進行状況の記録（`jobstatus`）、履歴のページング（`paging`）、ID キャッシュ
  - `jobstatus.ErrNotFound`（未記録）と `ErrUnavailable`（読めない）は**意図的に別物**です。
    同一視すると障害が「空行が並ぶ 200」として出ます。
  - `paging.LoadPage` は load のエラーを呼び出し元へ返さず、その行を落として続行します。
    障害を surface したい場合は `repository.loadFailure` のように自分で捕まえてください。
- `gcp-kit`: OAuth・CSRF・Cloud Tasks の投入と OIDC 検証・`cloudlog`
  - CSRF の検証は `auth.Handler.Middleware` が状態変更メソッドに対して行います。
    アプリ側に検証コードが無いのは正常です。
- `go-utils`: ジョブ ID 採番（`jobid`）、JST 整形（`jst`）、`slogctx`
- `netarmor/securenet`: SSRF 対策と `SERVICE_URL` の安全性判定
