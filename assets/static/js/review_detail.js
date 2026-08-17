// レビュー詳細ページの削除ボタン。
// 実際の送信は app.js の App.deleteResource が行います（確認ダイアログ・CSRF ヘッダ・
// 失敗時の表示はどのページでも同じなので、そちらへ寄せています）。
(() => {
    const btn = document.getElementById('delete-review-btn');
    if (!btn) return;

    btn.addEventListener('click', (event) => window.App.deleteResource({
        url: `/history/${btn.dataset.jobId}`,
        confirmMessage: 'このレビュー履歴を削除します。取り消せません。よろしいですか？',
        event,
        onSuccess: () => {
            window.location.href = '/history';
        }
    }));
})();
