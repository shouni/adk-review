// The delete button on the review detail page.
// The request itself is sent by App.deleteResource in app.js (the confirmation dialog,
// CSRF header and failure display are the same on every page, so they live there).
(() => {
    const btn = document.getElementById('delete-review-btn');
    if (!btn) return;

    btn.addEventListener('click', (event) => window.App.deleteResource({
        url: `/jobs/${btn.dataset.jobId}`,
        confirmMessage: 'このレビュー履歴を削除します。取り消せません。よろしいですか？',
        event,
        onSuccess: () => {
            window.location.href = '/jobs';
        }
    }));
})();
