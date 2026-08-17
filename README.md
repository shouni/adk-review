# 🤖 ADK Review

[![Language](https://img.shields.io/badge/Language-Go-blue)](https://golang.org/)
[![Platform](https://img.shields.io/badge/Platform-Cloud%20Run-blue?logo=google-cloud)](https://cloud.google.com/run)
[![Go Version](https://img.shields.io/github/go-mod/go-version/shouni/adk-review)](https://golang.org/)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](https://opensource.org/licenses/MIT)

## 🚀 概要 (About)

**ADK Review** は、Git リポジトリの差分を AI エージェントにレビューさせる Web アプリです。
[`git-gemini-web`](https://github.com/shouni/git-gemini-web) の後継で、機能を本リポジトリへ
移管したのち、旧リポジトリはアーカイブします。

旧版との違いは、レビューが**単発の diff → LLM 1 回呼び出し**ではなく、
[ADK for Go](https://github.com/google/adk-go) のエージェントループになることです。
エージェントはツール（ファイル読み・検索）でリポジトリの **diff の外**を自分で調べてから
指摘をまとめます。主用途は Git で管理している記事や小説の原稿のレビューで、
diff 単発では構造的に見えなかった「前の章との矛盾」「登場人物の設定の一貫性」を
拾えるようにするのが移行の動機です。`assets/prompts/` のプロンプトを差し替えれば、
コードレビューにも切り替えられます。

レビューの手順そのものは [`go-review-kit`](https://github.com/shouni/go-review-kit)（v2）に、
非同期ジョブの記録とページングは [`go-job-kit`](https://github.com/shouni/go-job-kit) に委ね、
本リポジトリは**依頼の受付・認証・非同期実行・結果の保存と表示、そして ADK エージェントの実装**を
担います。

> **📌 開発状況:** コードの移植は完了し、デプロイ待ちです。
>
> 1. ~~`go-review-kit` v1.2.0 — workspace reviewer ポートの追加~~ ✅
> 2. ~~ADK エージェント版 Reviewer の PoC（ループ・ツール・レポート出力の検証）~~ ✅
> 3. ~~`git-gemini-web` の web/worker 骨格（受付・履歴・GCS 保存・Slack 通知）を移植~~ ✅
> 4. GCP リソース作成（バケット・キュー・Cloud Run x2）とデプロイ
> 5. 旧リポジトリをアーカイブ

---

## 🧠 エージェント設計 (Agent Design)

* **ループの中身**: ADK の LLM Agent に読み取り専用ツール（対象ファイルの全文読み、
  リポジトリ内検索）を持たせ、diff を起点に必要な文脈を自分で集めさせます。
* **終端は `submit_report` ツール**: ADK は構造化出力の指定とツール使用が同居しにくいため、
  レビュー完了時に `submit_report` ツールを呼ばせ、その引数を `go-review-kit` の
  `Report` スキーマで受けます。検証も既存の `ParseReport` / `Validate` をそのまま使います。
* **暴走はツール呼び出し回数の上限で止めます**: レビュー 1 件は Cloud Tasks の
  dispatch deadline（10 分）内に収める必要があるため、時間ではなく回数で打ち切ります。
* **単発モードも残します**: `go-review-kit` の従来の単発 Gemini reviewer は v2 でも残すので、
  軽いレビューは単発・重いレビューはエージェント、と使い分けられます。
* **依存の隔離**: ADK は `google.golang.org/genai` を直接使うため、
  「genai SDK は `go-gemini-client` の外に出さない」というエコシステムの規約の
  例外は本リポジトリに閉じ込めます。`go-review-kit` 自体は ADK を知りません。

---

## 🏗 アーキテクチャ (Architecture)

**ヘキサゴナルアーキテクチャ（Ports and Adapters）** を採用し、外部との接続はすべて
アダプターとして分離しています。この骨格は `git-gemini-web` から引き継ぎます。

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
  Cloud Run サービスとしてデプロイします（ap-* 兄弟アプリと同じ方式）。旧 git-gemini-web の
  「1 サービスが self-invoke」ではなく、Web 面は `WORKER_URL` の worker サービスへタスクを
  投入します。ローカル開発は `SERVER_ROLE=both` で 1 プロセスに両面を持たせます。
* **エンジンの使い分け**: プロンプト冒頭の `<!-- engine: agent -->` メタデータで、モードごとに
  単発レビューとエージェントレビューを切り替えます（既定は single。現在は novel のみ agent）。
  フォームにエンジンの選択肢は出しません。

### 成果物の置き場所

1 ジョブ分のオブジェクトは、ジョブ ID のプレフィックス配下にまとめます。
バケットは本アプリ専用に新規作成します（`git-gemini-web` のバケットは引き継ぎません）。

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

`git-gemini-web` との違いは、ADK エージェント一式（`internal/adkagent/`）と、単発／エージェントの
2 本のパイプラインを使い分ける `EngineRouter`（`internal/adapters/`）が加わったことです。

```text
adk-review/
├── assets/            # 【資産】静的リソース（embed でバイナリに埋め込み）
│   ├── prompts/       #   - レビュー指示書（ファイル名がモード名。engine メタデータ付き）
│   ├── partials/      #   - 全モード共通の出力フォーマット説明
│   ├── templates/     #   - HTML テンプレート
│   ├── static/        #   - ブラウザへ配信する CSS / JS（/static/ で公開）
│   └── assets.go      #   - embed.FS の定義とメタデータ解析
├── internal/
│   ├── adkagent/      # 【頭脳】ADK エージェント（llmagent + ツール + 出力スキーマ）
│   ├── adapters/      # 【接続】Gemini / Git / Slack / 結果保存 / EngineRouter / パイプライン ACL
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
| エージェント | [ADK for Go](https://github.com/google/adk-go)（`google.golang.org/adk`） |
| レビュードメイン・Git 差分 | [`go-review-kit`](https://github.com/shouni/go-review-kit) v2 |
| ジョブ状態・履歴ページング | [`go-job-kit`](https://github.com/shouni/go-job-kit) |
| 実行基盤 | Cloud Run / Cloud Tasks |
| 認証・セッション | OAuth 2.0（[`gcp-kit`](https://github.com/shouni/gcp-kit)） |
| I/O 抽象化 | [`go-remote-io`](https://github.com/shouni/go-remote-io)（GCS 操作） |

**AI は Vertex AI 経由で呼びます。** ADK のモデル層は `google.golang.org/genai` を使うため、
旧版と同じく `ProjectID` ベースの Vertex AI 認証で配線する予定です。

---

## ⚙️ セットアップ

環境変数と IAM は `git-gemini-web` の設計をそのまま引き継ぎます。移植中に変わる可能性が
ありますが、現時点の想定は次の通りです。

### 1. 必要な環境変数

**未設定だと起動時に落ちる**のは、全役割共通で `SERVER_ROLE`・`SERVICE_URL`（本番は HTTPS
必須）・`GEMINI_MODELS`、web 面ではさらに `GOOGLE_CLIENT_ID`・`GOOGLE_CLIENT_SECRET`・
`SESSION_SECRET`・`SESSION_ENCRYPT_KEY`・`ALLOWED_EMAILS` または `ALLOWED_DOMAINS` です。
worker 面は OAuth 系の設定を要求しません。残りは空でも起動します（機能しないだけです）。

`GEMINI_MODELS` にアプリ側の既定値を置かないのは意図的です。モデル ID が古くなるのは
Google のリリース周期であってこのリポジトリの都合ではないため、既定値があると
「デプロイ設定を変えていないのに古いモデルを指し続ける」状態に誰も気付けません。

**基本設定:**

| 環境変数 | 説明 | デフォルト値（例） |
| :--- | :--- | :--- |
| `SERVER_ROLE` | このプロセスの役割: `web` / `worker` / `both`（ローカル開発用）。**必須** | `web` |
| `SERVICE_URL` | このサービス自身のルート URL（末尾スラッシュなし）。**本番では HTTPS 必須** | `https://myapp.run.app` または `http://localhost:8080` |
| `WORKER_URL` | タスクの投入先（worker サービス）のルート URL。web 面で使用。未設定なら `SERVICE_URL`（both 用） | `https://myapp-worker.run.app` |
| `AGENT_MAX_TOOL_CALLS` | エージェントレビュー 1 件あたりのツール呼び出し回数上限。0 で既定値（32） | `0` |
| `PORT` | サーバーがリッスンするポート | `8080` |
| `GCP_PROJECT_ID` | GCP のプロジェクト ID | `your-gcp-project` |
| `GCP_LOCATION_ID` | Cloud Tasks キューのリージョン | `asia-northeast1` |
| `CLOUD_TASKS_QUEUE_ID` | 使用する Cloud Tasks のキュー名 | `review-queue` |
| `SERVICE_ACCOUNT_EMAIL` | タスク発行に使用するサービスアカウント | - |
| `GCS_REVIEW_BUCKET` | レビュー結果と進行状況を保存する GCS バケット名 | `your-review-archive-bucket` |
| `GEMINI_MODELS` | 使用する Gemini モデル名。カンマ区切りで複数指定するとフォームで選択可能（先頭がデフォルト）。**アプリ側に既定値は無く、未設定だと起動時に落ちます** | **必須**（Google の最新モデル ID を確認して設定） |
| `TASK_AUDIENCE_URL` | Cloud Tasks の OIDC トークン検証に使う audience。未設定なら `SERVICE_URL` | `https://myapp.run.app` |
| `PIPELINE_TIMEOUT` | レビュー 1 件の実行時間の上限（`5m` 形式）。Cloud Tasks の dispatch deadline より短いこと。超えると起動時エラー | `5m` |
| `SSH_KEY_PATH` | SSH 形式のリポジトリ（`git@github.com:owner/repo.git`）のクローンに使う秘密鍵パス（Secret Manager マウント推奨） | `/secrets/ssh/id_rsa` |
| `SLACK_WEBHOOK_URL` | レビューの結末を通知する Slack Webhook URL。未設定なら通知をスキップ | `https://hooks.slack.com/services/T...` |

> **SSH ホストキー検証を無効化するスイッチはありません。** `Dockerfile` が GitHub の
> ホストキーを `/etc/ssh/ssh_known_hosts` へ焼き込むため通常は設定不要で、GitHub 以外を
> 対象にする場合のみ同ファイルへ追記します。

**認証設定 (OAuth):**

| 環境変数 | 説明 | 設定例 |
| :--- | :--- | :--- |
| `GOOGLE_CLIENT_ID` | OAuth クライアント ID（リダイレクト URI は `<SERVICE_URL>/auth/callback`） | `xxxx.apps.googleusercontent.com` |
| `GOOGLE_CLIENT_SECRET` | OAuth シークレット | `GOCSPX-xxxx...` |
| `SESSION_SECRET` | セッションの HMAC 署名用シークレット | `openssl rand -base64 32` |
| `SESSION_ENCRYPT_KEY` | セッションの AES 暗号化用シークレット（16/24/32 バイト） | `openssl rand -base64 32` |
| `ALLOWED_EMAILS` / `ALLOWED_DOMAINS` | アクセスを許可するメールまたはドメイン。**どちらか一方は必要**（両方空だと誰もログインできません） | `user@example.com,user2@example.com` / `example.com` |

### 2. 必要なIAMロールの設定

web / worker を別サービスに分けたため、実行 SA も面ごとに分けられます（1 つを共用しても
動きます）。`SERVICE_ACCOUNT_EMAIL` は Cloud Tasks が発行する OIDC トークンの**発行者**
（web 面がタスクに載せる SA）であると同時に、worker 面の受信側**許可リスト**でもあるため、
両サービスに同じ値を設定してください。

必要な権限は次のとおりです。

- **web 面の SA**: GCS バケットの読み書き（受付記録・履歴表示・削除）、Cloud Tasks キューへの
  タスク投入と `SERVICE_ACCOUNT_EMAIL` を指定した OIDC トークンの発行（ActAs）
- **worker 面の SA**: GCS バケットの読み書き（結果保存・進行状況）、Vertex AI の呼び出し
  （単発・エージェントの両方がここを通ります）
- 共通: 使用するシークレットの読み取り

**ロール名を列挙していないのは、粒度が環境によって変わるためです。** 決め方だけ挙げておきます。

- **GCS はバケット単位で `objectUser` を使ってください。** `objectAdmin` はオブジェクト ACL の
  操作まで許します。プロジェクトレベルで付けると、無関係なバケットにも到達します
- **シークレットはシークレット単位で付けてください。** プロジェクトレベルだと全シークレットに
  到達します

設定が不足していると `403 Forbidden` になります。

---

## 📜 ライセンス (License)

このプロジェクトは [MIT License](https://opensource.org/licenses/MIT) の下で公開されています。
