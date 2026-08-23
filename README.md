# 🤖 ADK Review

[![CI](https://github.com/shouni/adk-review/actions/workflows/ci.yml/badge.svg)](https://github.com/shouni/adk-review/actions/workflows/ci.yml)
[![Status](https://img.shields.io/badge/Status-Active-brightgreen)](#)
[![Language](https://img.shields.io/badge/Language-Go-blue)](https://golang.org/)
[![Platform](https://img.shields.io/badge/Platform-Cloud%20Run-blue?logo=google-cloud)](https://cloud.google.com/run)
[![Go Version](https://img.shields.io/github/go-mod/go-version/shouni/adk-review)](https://golang.org/)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](https://opensource.org/licenses/MIT)

## 🚀 概要 (About)

**ADK Review** は、Git リポジトリの差分を AI エージェントにレビューさせる Web アプリです。

レビューは差分を 1 回モデルへ渡して終わりではなく、[ADK for Go](https://github.com/google/adk-go)
のエージェントループです。エージェントはツール（ファイル読み・一覧・検索）で
リポジトリの **差分の外**を自分で調べてから指摘をまとめます。差分だけでは構造的に
見えない「前の章との矛盾」「登場人物の設定の一貫性」「変更した関数の呼び出し元」を
拾えるのが、この作りの狙いです。主用途は Git で管理している記事・小説の原稿と
ソースコードのレビューで、対象は `assets/prompts/` のモードで切り替えます。

レビューの手順そのものは [`go-review-kit`](https://github.com/shouni/go-review-kit) に、
非同期ジョブの記録とページングは [`go-job-kit`](https://github.com/shouni/go-job-kit) に委ね、
本リポジトリは**依頼の受付・認証・非同期実行・結果の保存と表示、そしてレビュアーの実装**を
担います。

---

## 🧠 エージェント設計 (Agent Design)

* **ループの中身**: ADK の LLM Agent に読み取り専用ツール 3 種（`read_file` / `list_files` /
  `search_text`）を持たせ、差分を起点に必要な文脈を自分で集めさせます。ツールが触れるのは
  head をチェックアウトした作業ディレクトリの中だけで、パスは実体解決して外への脱出を防ぎます。
* **出力は `OutputSchema` で固定**: `review.Report` に対応するスキーマを指定します。ADK は
  ツールと構造化出力を併用でき、最終応答としてスキーマに従う JSON が返ります。デコードと
  検証は `go-review-kit` の `ParseReport` / `Validate` をそのまま使います。
* **暴走はツール呼び出し回数の上限で止めます**: レビュー 1 件は Cloud Tasks の
  dispatch deadline（`TASK_DISPATCH_DEADLINE`、デプロイでは 10 分）内に収める必要があるため、
  時間ではなく回数で打ち切ります
  （`AGENT_MAX_TOOL_CALLS`、既定 32）。上限に達したら「調査を切り上げて結論を出せ」と
  モデルへ伝わるので、締切超過ではなくレビューの完了に倒れます。
* **依存の隔離**: ADK が `google.golang.org/genai` を直接要求するため、genai を import するのは
  `internal/adkagent`（レビュアーと出力スキーマ）と `internal/adapters/ai.go`（クライアント設定）
  だけに留めます。`go-review-kit` 自体は AI SDK を知りません。

---

## 🏗 アーキテクチャ (Architecture)

**ヘキサゴナルアーキテクチャ（Ports and Adapters）** を採用し、外部との接続はすべて
アダプターとして分離しています。

```text
フォーム受付          非同期ワーカー
─────────────        ────────────────────────────────────
ジョブID採番     →   Git 差分 → ADK エージェント → report.json 保存
Cloud Tasks 投入     ↘ status.json 記録 / Slack 通知
受付を記録
                     履歴 /history → /history/{jobID}
```

* **非同期実行**: 重い解析を Cloud Tasks へ逃がし、Web 側のタイムアウトを回避します。
* **依存性注入**: `internal/builder` が全コンポーネントを組み立てます。通知先や保存先を
  ロジックに触れずに差し替えられます。
* **1 イメージ 2 サービス**: 同じイメージを `SERVER_ROLE`（web / worker）で分け、別々の
  Cloud Run サービスとしてデプロイします（兄弟アプリと同じ方式）。Web 面は
  `WORKER_URL` の worker サービスへタスクを投入します。ローカル開発は `SERVER_ROLE=both` で
  1 プロセスに両面を持たせます。
* **調査の深さは回数で決めます**: レビューは常にエージェントループで走ります。どこまで
  調べるかは `AGENT_MAX_TOOL_CALLS`（ツール呼び出しの上限）で決まり、上限の 8 割を使うと
  まとめに入るようエージェントへ伝えます。速く浅く済ませたい場合はこの数を下げます。

### 成果物の置き場所

1 ジョブ分のオブジェクトは、ジョブ ID のプレフィックス配下にまとめます。

```text
gs://{GCS_REVIEW_BUCKET}/reviews/{jobID}/
├── status.json   # 進行状況と一覧用メタ（投入時に作成）
└── report.json   # レビュー結果の全文（成功時のみ）
```

* **履歴一覧は `reviews/` 直下のプレフィックスを列挙して作ります。** 状態ファイルを
  同じ配下に置いているため、実行中・失敗・スキップのジョブも一覧に並びます。
* **一覧用のメタと結果の全文を分けています。** 一覧は 1 行につき 1 回 `status.json` を
  読むため、指摘の全文を同じファイルへ入れると読み取り量が指摘件数に比例して増えます。
* **結果は JSON で保存し、表示は `/history/{jobID}` が行います。** 整形済みの HTML を
  置いて署名付き URL で配ると、アプリの認証を迂回できてしまううえ、同じ内容の見た目が
  詳細画面と 2 系統に分かれます。
* **削除はプレフィックスの一括走査で行います。** 消す側は「そのジョブが何を作ったか」を
  知る必要がなく、成果物の種類が増えても削除処理を直さずに済みます。実行中のジョブは
  削除できません（消してもワーカーが `status.json` を書き戻して復活するためです）。

---

## 📂 プロジェクト構造 (Project Structure)

```text
adk-review/
├── assets/            # 【資産】静的リソース（embed でバイナリに埋め込み）
│   ├── prompts/       #   - レビュー指示書（ファイル名がモード名。front matter に説明と excerpt）
│   ├── partials/      #   - 全モード共通の出力フォーマット説明（verdict / findings）
│   ├── templates/     #   - HTML テンプレート
│   ├── static/        #   - ブラウザへ配信する CSS / JS（/static/ で公開）
│   └── assets.go      #   - embed.FS の定義と front matter の解析
├── internal/
│   ├── adkagent/      # 【頭脳】ADK エージェント（llmagent + ツール + 出力スキーマ）
│   ├── adapters/      # 【接続】Git / Slack / 結果保存 / プロンプト / パイプライン ACL
│   ├── app/           # 【基盤】Container による依存の保持とライフサイクル管理
│   ├── builder/       # 【構築】役割（SERVER_ROLE）に応じた初期化と組み立て
│   ├── config/        # 【設定】環境変数・定数・バリデーション
│   ├── domain/        # 【中心】モデル、保存先の規約、ポート定義
│   ├── giturl/        # 【変換】リポジトリURLの解析と表示用パス
│   ├── repository/    # 【読み取り】GCS 上のレビュー履歴
│   └── server/        # 【玄関】HTTP サーバー、ルーティング、ハンドラ
└── main.go            # 【起点】起動とシグナルハンドリング
```

---

## ✨ 技術スタック (Technology Stack)

| 要素 | 技術 / ライブラリ |
| --- | --- |
| 言語 | Go |
| エージェント | [ADK for Go](https://github.com/google/adk-go)（`google.golang.org/adk/v2`） |
| レビュードメイン・パイプライン・Git 差分 | [`go-review-kit`](https://github.com/shouni/go-review-kit) |
| プロンプトテンプレート | [`go-prompt-kit`](https://github.com/shouni/go-prompt-kit) |
| ジョブ状態・履歴ページング | [`go-job-kit`](https://github.com/shouni/go-job-kit) |
| 実行基盤 | Cloud Run / Cloud Tasks |
| 認証・セッション | OAuth 2.0（[`gcp-kit`](https://github.com/shouni/gcp-kit)） |
| I/O 抽象化 | [`go-remote-io`](https://github.com/shouni/go-remote-io)（GCS 操作） |

**AI は Vertex AI 経由で呼びます。** ADK のモデル層は `GCP_PROJECT_ID` ベースの認証で、
API キー経路は配線していません。

---

## ⚙️ セットアップ

### 1. 必要な環境変数

**未設定だと起動時に落ちる**のは、全役割共通で `SERVER_ROLE`・`SERVICE_URL`（本番は HTTPS
必須）・`GEMINI_MODELS`・`GCP_PROJECT_ID`・`GCS_REVIEW_BUCKET`、web 面ではさらに
`CLOUD_TASKS_QUEUE_ID`・`WORKER_URL`・`TASK_CALLER_SERVICE_ACCOUNT_EMAIL`・
`GOOGLE_CLIENT_ID`・`GOOGLE_CLIENT_SECRET`・`SESSION_SECRET`・`SESSION_ENCRYPT_KEY`・
`ALLOWED_EMAILS` または `ALLOWED_DOMAINS`、worker 面では `TASK_AUDIENCE_URL`（未設定なら
`SERVICE_URL` へ落ちます）と `ALLOWED_TASK_SERVICE_ACCOUNTS` です。
`TASK_DISPATCH_DEADLINE` も全役割で必須、`PIPELINE_TIMEOUT` は worker 面で必須です（後述）。
worker 面は OAuth 系の設定を要求しません。残りは空でも起動します（機能しないだけです）。

**プロジェクト ID とバケット名に既定値を置かないのは意図的です。** プレースホルダを置くと
設定漏れのまま起動が成功し、失敗するのは利用者がフォームを送ったあと、しかも Gemini の
呼び出しを終えた保存の段階になります。いちばんコストを払ったあとで落ちる順序なので、
起動時に止めます。`SERVICE_URL` も同じ理由で既定値を持ちません（`http://localhost:8080` を
既定にすると、本番の設定漏れが「開発用ホスト名なので HTTPS 検査を免除」の経路で
素通りしてしまいます）。

`GEMINI_MODELS` にアプリ側の既定値を置かないのは意図的です。モデル ID が古くなるのは
Google のリリース周期であってこのリポジトリの都合ではないため、既定値があると
「デプロイ設定を変えていないのに古いモデルを指し続ける」状態に誰も気付けません。

**基本設定:**

| 環境変数 | 説明 | デフォルト値（例） |
| :--- | :--- | :--- |
| `SERVER_ROLE` | このプロセスの役割: `web` / `worker` / `both`（ローカル開発用）。**必須** | `web` |
| `SERVICE_URL` | このサービス自身のルート URL（末尾スラッシュなし）。**本番では HTTPS 必須。既定値は無く、未設定だと起動時に落ちます** | `https://myapp.run.app`（ローカルは `http://localhost:8080`） |
| `WORKER_URL` | タスクの投入先（worker サービス）のルート URL。web 面で使用。未設定なら `SERVICE_URL`（both 用） | `https://myapp-worker.run.app` |
| `AGENT_MAX_TOOL_CALLS` | エージェントレビュー 1 件あたりのツール呼び出し回数上限。0 で既定値（32） | `0` |
| `PORT` | サーバーがリッスンするポート | `8080` |
| `GCP_PROJECT_ID` | GCP のプロジェクト ID。**既定値は無く、未設定だと起動時に落ちます** | **必須** |
| `GCP_LOCATION_ID` | Cloud Tasks キューのリージョン。Gemini は `global` 固定（`internal/adapters/ai.go`）なので流用しません。**既定値は無く、web 面では未設定だと起動時に落ちます** | **web 面で必須**（例: `asia-northeast1`） |
| `CLOUD_TASKS_QUEUE_ID` | 使用する Cloud Tasks のキュー名。**既定値は無く、web 面では未設定だと起動時に落ちます** | **web 面で必須**（例: `review-queue`） |
| `TASK_CALLER_SERVICE_ACCOUNT_EMAIL` | 投入するタスクの OIDC に指定する caller SA（web 面のみ） | `adk-review-web-runner@...` |
| `ALLOWED_TASK_SERVICE_ACCOUNTS` | worker が受け付けるトークンの発行元 SA（カンマ区切り、worker 面のみ）。web 面の SA を並べる | `adk-review-web-runner@...` |
| `ALLOWED_M2M_SERVICE_ACCOUNTS` | web の JSON API をサーバー間で呼べる SA（カンマ区切り、web 面のみ）。空なら M2M 無効 | `ap-mcp-runner@...` |
| `GCS_REVIEW_BUCKET` | レビュー結果と進行状況を保存する GCS バケット**名**（`gs://` は付けても落とします）。**既定値は無く、未設定だと起動時に落ちます** | **必須** |
| `GEMINI_MODELS` | 使用する Gemini モデル名。カンマ区切りで複数指定するとフォームで選択可能（先頭がデフォルト）。**アプリ側に既定値は無く、未設定だと起動時に落ちます** | **必須**（Google の最新モデル ID を確認して設定） |
| `TASK_AUDIENCE_URL` | Cloud Tasks の OIDC トークン検証に使う audience。未設定なら `SERVICE_URL` | `https://myapp.run.app` |
| `PIPELINE_TIMEOUT` | レビュー 1 件の実行時間の上限（`5m` 形式）。`TASK_DISPATCH_DEADLINE` より短いこと。**既定値は無く、worker 面では未設定だと起動時に落ちます**（無制限は許しません） | **worker 面で必須** |
| `SSH_KEY_PATH` | SSH 形式のリポジトリ（`git@github.com:owner/repo.git`）のクローンに使う秘密鍵パス（Secret Manager マウント推奨。Cloud Run では `/secrets/ssh/id_rsa` を渡します） | `~/.ssh/id_rsa` |
| `TASK_DISPATCH_DEADLINE` | Cloud Tasks がワーカーの応答を待つ上限（`10m` 形式）。**ワーカーの実行時間の実効上限**で、`PIPELINE_TIMEOUT` より長いこと。範囲（15 秒〜30 分）の検査は gcp-kit が投入口の構築時に行います。**既定値は無く、未設定だと起動時に落ちます** | **必須** |
| `HTTP_TIMEOUT` | Slack 通知など外部 HTTP 呼び出しの上限 | `30s` |
| `LOG_LEVEL` | ログ出力レベル（`debug` / `info` / `warn` / `error`） | `info` |
| `SLACK_WEBHOOK_URL` | レビューの結末を通知する Slack Webhook URL。未設定なら通知をスキップ | `https://hooks.slack.com/services/T...` |

> **SSH ホストキー検証を無効化するスイッチはありません。** `Dockerfile` がビルド時に
> GitHub の API からホストキーを取得して `/etc/ssh/ssh_known_hosts` へ焼き込むため
> 通常は設定不要です。鍵をリポジトリに固定しないのは、ローテートに追従できないと
> clone が全滅し、しかも気付くのが本番になるためです。参照先は `SSH_KNOWN_HOSTS`
> （Dockerfile が設定済み）で変えられ、GitHub 以外を対象にする場合は同ファイルへ追記します。

**認証設定 (OAuth):**

| 環境変数 | 説明 | 設定例 |
| :--- | :--- | :--- |
| `GOOGLE_CLIENT_ID` | OAuth クライアント ID（リダイレクト URI は `<SERVICE_URL>/auth/callback`） | `xxxx.apps.googleusercontent.com` |
| `GOOGLE_CLIENT_SECRET` | OAuth シークレット | `GOCSPX-xxxx...` |
| `SESSION_SECRET` | セッションの HMAC 署名用シークレット | `openssl rand -base64 32` |
| `SESSION_ENCRYPT_KEY` | セッションの AES 暗号化用シークレット（16/24/32 バイト。長さが違うと起動時に落ちます） | `openssl rand -base64 24`（32 文字になります） |
| `ALLOWED_EMAILS` / `ALLOWED_DOMAINS` | アクセスを許可するメールまたはドメイン。**どちらか一方は必要**（両方空だと誰もログインできません） | `user@example.com,user2@example.com` / `example.com` |

### 2. 必要なIAMロールの設定

実行 SA は面ごとに分けます（インフラ管理リポジトリの「1 ワークロード 1 SA」規約）。Cloud Tasks の OIDC は
web 面が `TASK_CALLER_SERVICE_ACCOUNT_EMAIL`（署名者 = web SA）を指定し、worker 面が
`ALLOWED_TASK_SERVICE_ACCOUNTS`（許可する発行元 = web SA）で検証します。

IAM の定義はインフラ管理リポジトリ（Terraform）が正で、必要な権限は次のとおりです。

- **web 面の SA**: GCS バケットの読み書き（受付記録・履歴表示・削除）、Cloud Tasks キューへの
  タスク投入と `TASK_CALLER_SERVICE_ACCOUNT_EMAIL` を指定した OIDC トークンの発行（ActAs）
- **worker 面の SA**: GCS バケットの読み書き（結果保存・進行状況）、Vertex AI の呼び出し
- 共通: 使用するシークレットの読み取り

**ロール名を列挙していないのは、粒度が環境によって変わるためです。** 決め方だけ挙げておきます。

- **GCS はバケット単位で `objectUser` を使ってください。** `objectAdmin` はオブジェクト ACL の
  操作まで許します。プロジェクトレベルで付けると、無関係なバケットにも到達します
- **シークレットはシークレット単位で付けてください。** プロジェクトレベルだと全シークレットに
  到達します

設定が不足していると `403 Forbidden` になります。

---

### 3. 役割ごとに生えるルート

ローカルで両面を 1 プロセスに持たせる `SERVER_ROLE=both` は残していますが、**起動手順は
載せません。Cloud Tasks は localhost へ配送できないため、依頼を送ってもレビューは実行されず、
履歴が queued のまま残ります**（`review-queue` は `max_attempts = 1` で再試行も来ません）。
ロジックの確認は `go test ./... -race`、画面の確認はテンプレートを直接描くのが早いです。

| ルート | メソッド | web | worker | 説明 |
| :--- | :--- | :---: | :---: | :--- |
| `/health` | GET | ✅ | ✅ | ヘルスチェック（認証不要） |
| `/static/*` | GET | ✅ | ✅ | CSS / JS（認証の外側） |
| `/auth/login`, `/auth/callback` | GET | ✅ | — | Google OAuth |
| `/` | GET | ✅ | — | レビュー依頼フォーム |
| `/submit_review` | POST | ✅ | — | 依頼の受付とタスク投入 |
| `/modes` | GET | ✅ | — | 選べるレビューモード一覧（JSON のみ） |
| `/jobs/{jobID}` | GET | ✅ | — | 進行状況だけを返す（JSON のみ。完了検知用） |
| `/history` | GET | ✅ | — | 履歴一覧 |
| `/history/{jobID}` | GET / DELETE | ✅ | — | 詳細表示 / 削除（実行中は 409） |
| `/tasks/execute_review` | POST | — | ✅ | Cloud Tasks からの実行（OIDC 検証） |

担当しない面のルートは登録しません。役割とハンドラが噛み合わない構成は、
ルーターが 404 を返す前に起動時に落とします（`builder.AppHandlers.Validate`）。

**web 面のルートは、ブラウザと機械の両方から使えます。** `Accept: application/json` を
付けると同じルートが JSON を返し、`POST /submit_review` は JSON body も受け付けます
（項目名はフォームと同じで、`domain.ReviewRequest` の json タグに揃えてあります）。
別ルートを立てないのは、同じ取得処理を 2 本持つと画面と API が食い違うためです。

サーバー間から呼ぶ場合は OIDC Bearer トークンを付けます。許可する呼び出し元は
`ALLOWED_M2M_SERVICE_ACCOUNTS` で、**空なら M2M は無効**です（web 面は画面が使えれば
成立するため、worker 面のように必須にはしません）。Bearer 経路はセッションと CSRF を
バイパスします。CSRF はクッキーの自動送出を悪用する攻撃への対策であり、明示的に
トークンを付けるサーバー間呼び出しには当てはまらないためです。

---

## 📜 ライセンス (License)

このプロジェクトは [MIT License](https://opensource.org/licenses/MIT) の下で公開されています。
