package domain

// WorkerTaskPath は、ワーカーがタスクを受け取るパスです。
// 投入側（tasks.Config.WorkerPath）と受信側（internal/server のルート登録）の両方が使います。
// 二重に持つと片方だけ変えてもビルドは通り、投入したタスクが全件 404 になります。
// サービス URL への継ぎ足しと正規化は gcp-kit の tasks が行います。
const WorkerTaskPath = "/tasks/execute-review"

// LegacyWorkerTaskPath は、kebab-case に改める前のワーカーのパスです。
//
// 投入側はもう使いません。受信側だけが残すのは、改名前に積まれたタスクがこのパスで
// 届くためです。review-queue は再試行しないので、消すのは改名後のデプロイが済み、
// キューに旧パスのタスクが無くなってからです。
const LegacyWorkerTaskPath = "/tasks/execute_review"
