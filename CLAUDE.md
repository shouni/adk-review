# CLAUDE.md

このファイルは、このリポジトリで作業するときの前提と、コードを読んだだけでは分からない
不変条件をまとめたものです。兄弟アプリ（`ap-voice` / `ap-comp` / `ap-mv` / `ap-story` /
`ap-mcp`）と同じ規約に従います。インフラの定義は別リポジトリ `ap-infra` が正です。

## What this is

Git リポジトリの差分を AI エージェントにレビューさせる Web アプリです。単発でモデルへ
差分を渡すのではなく、ADK for Go のエージェントループでツール（`read_file` / `list_files` /
`search_text`）を使わせ、**差分の外**を自分で調べさせてから指摘をまとめさせます。

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

ローカル起動は README の「3. ローカルでの起動」を参照。`SERVER_ROLE=both` です。

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
- 三段目はアプリから見えないため、`ap-infra/app_adk_review.tf` の `precondition` が受け持ちます。
- **フリートで唯一、三段とも短く取ってあります。** 単発レビューの実測が 10 秒未満で、
  動画生成とは桁が違うためです。上限は「正常系の目標」ではなく「ハングを捕まえる網」です。
- エージェントレビューはツール呼び出しの回数だけ伸びます。実測が近づいたら
  `TASK_DISPATCH_DEADLINE` を伸ばしてください（**env なので再ビルドは不要です**。
  Cloud Tasks の HTTP ターゲットは 30 分が上限）。

### Cloud Tasks の OIDC

発行元と許可リストを 1 つの変数で兼ねません。同じ値でも役割ごとに意味が反転して
読めなくなるためです（`ap-infra/docs/conventions.md`「Cloud Tasks の OIDC」）。

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

### エンジンは 2 種類あり、既定はモードが宣言する

モードは「何を見るか」、エンジンは「どこまで調べるか」を決めます。既定は
`assets/prompts/*.md` の front matter の `engine`（現在は全モード `agent`）で、依頼ごとに
フォームから上書きできます。

- 解決は `assets.ResolveEngine` の 1 箇所で、受付時（`validateReviewRequest`）と
  実行時（`EngineRouter.Run`）の両方が同じ関数を通ります。
- **解決できない指定は進行状況に記録しません。** 記録すると「存在しないエンジンで実行して
  失敗した」という、実際には起きていない記録が履歴に残ります。

### 出力スキーマは 2 つあり、意図的に別物

`internal/adapters/gemini_schema.go`（単発）と `internal/adkagent/schema.go`（エージェント）は
SDK の型が違うため別々に組み立てます。**統合しないでください。**

- 差分は `findings[].evidence` の有無だけです。エージェントは作業ディレクトリを実際に
  調べるので「どこを見て判断したか」を自己申告させる意味がありますが、差分しか見ない
  単発レビュアーに出させると**根拠の捏造を促します**。
- 列挙値だけは `domain.SeverityEnum` / `domain.DecisionEnum` に集約しています。
  食い違うと、モデルがスキーマ上は正当な値を返してデコードで弾かれ、**全レビューが失敗**します。
  両パッケージにドリフト検知テストがあります。

### genai SDK の隔離

「genai SDK は `go-gemini-client` の外に出さない」というエコシステムの規約に対し、
**ADK が `google.golang.org/genai` を直接要求するため例外が要ります。**
その例外は `internal/adkagent` に閉じ込めてください。`go-review-kit` 自体は AI SDK を知りません。

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

### ログは context に載せる

`slogctx` を通しているので、`Execute` の冒頭で載せた `job_id` / `mode` / `engine` が
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
  adapters/   単発レビュアー / Git / Slack / 保存 / EngineRouter / パイプライン ACL
  app/        Container（依存の保持とライフサイクル）
  builder/    SERVER_ROLE に応じた組み立て
  config/     環境変数・既定値・起動時検証
  domain/     モデル、保存先の規約、ポート定義、共有の列挙とルート定数
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
