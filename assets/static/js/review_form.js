// The submitting state of the review submission form. Prevents double submission and
// restores the form on the back button.
//
// This used to be written directly in review_form.html. Left inline, CSP's script-src
// would need 'unsafe-inline', which carries the same risk as CDN-sourced scripts.
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
     * Validates a branch name
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
     * Helper that switches the button's display state
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

        // setTimeout avoids cancelling the submission by disabling the button immediately
        setTimeout(() => {
            setSubmittingState(true);
        }, 0);
    });

    /**
     * Handles a restore from the bfcache, such as by the browser's back button
     */
    window.addEventListener('pageshow', function(event) {
        if (event.persisted) {
            setSubmittingState(false);
        }
    });
})();
