// レビュー投入フォームの送信中表示。二重送信を防ぎ、戻るボタンで復帰させます。
//
// もとは review_form.html に直接書かれていました。インラインのままだと CSP の
// script-src に 'unsafe-inline' が必要になり、CDN 由来の危険と同じ扱いになります。
(function () {
    'use strict';

    const submitBtn = document.getElementById('submitBtn');
    const btnIcon = document.getElementById('btnIcon');
    const btnSpinner = document.getElementById('btnSpinner');
    const btnText = document.getElementById('btnText');

    const reviewForm = document.getElementById('reviewForm');
    const baseBranchInput = document.getElementById('base_branch');
    const featureBranchInput = document.getElementById('feature_branch');

    /**
     * ブランチ名のバリデーション
     */
    function validateBranchInput(input) {
        const value = input.value || '';
        if (value.includes('..') || value.includes('//')) {
            input.setCustomValidity("'..' または '//' は使用できません。");
            return false;
        }
        if (value.endsWith('/') || value.endsWith('.')) {
            input.setCustomValidity("末尾に '/' や '.' は使用できません。");
            return false;
        }
        input.setCustomValidity('');
        return true;
    }

    /**
     * ボタンの表示状態を切り替えるヘルパー
     */
    function setSubmittingState(isSubmitting) {
        submitBtn.disabled = isSubmitting;
        if (isSubmitting) {
            btnIcon.classList.add('d-none');
            btnSpinner.classList.remove('d-none');
            btnText.textContent = '送信中...';
        } else {
            btnIcon.classList.remove('d-none');
            btnSpinner.classList.add('d-none');
            btnText.textContent = 'レビュー開始 (Queue Job)';
        }
    }

    baseBranchInput.addEventListener('input', () => validateBranchInput(baseBranchInput));
    featureBranchInput.addEventListener('input', () => validateBranchInput(featureBranchInput));

    reviewForm.addEventListener('submit', function(event) {
        const baseOK = validateBranchInput(baseBranchInput);
        const featureOK = validateBranchInput(featureBranchInput);

        if (!baseOK || !featureOK || !reviewForm.checkValidity()) {
            event.preventDefault();
            reviewForm.reportValidity();
            return;
        }

        // 即時無効化による送信キャンセルを避けるためsetTimeoutを使用
        setTimeout(() => {
            setSubmittingState(true);
        }, 0);
    });

    /**
     * ブラウザの「戻る」ボタン等でBFキャッシュから復元された際の処理
     */
    window.addEventListener('pageshow', function(event) {
        if (event.persisted) {
            setSubmittingState(false);
        }
    });
})();
