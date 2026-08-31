package domain

// TaskExecuteReviewPath は、ワーカーがレビュー実行タスクを受け取るパスです。
//
// 投入側と受信側の両方が使います。投入側（internal/builder）は WORKER_URL に
// このパスを継ぎ足してタスクの宛先を組み立て、受信側（internal/server）は同じパスで
// ハンドラーを登録します。リテラルを二重に持つと、片方だけ変えたときに
// 投入したタスクが全部 404 になり、review-queue は max_attempts = 1 なので
// 再試行もされず黙って消えます（履歴の行は queued のまま残るため、利用者からは
// 「レビューが一生終わらない」ように見えます）。ここ 1 箇所に集約します。
const TaskExecuteReviewPath = "/tasks/execute_review"
