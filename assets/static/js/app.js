window.App = window.App || {};
window.App.csrfToken = () => document.getElementById('csrf_token')?.value || '';

window.App.deleteResource = async ({
    url,
    confirmMessage,
    failureMessage = '削除に失敗しました。',
    event,
    onSuccess
}) => {
    if (event) {
        event.preventDefault();
    }

    if (!confirm(confirmMessage)) {
        return false;
    }

    try {
        const response = await fetch(url, {
            method: 'DELETE',
            headers: {
                'X-CSRF-Token': window.App.csrfToken()
            }
        });

        if (!response.ok) {
            const errorText = await response.text();
            alert(errorText ? `${failureMessage}: ${errorText}` : failureMessage);
            return false;
        }

        if (onSuccess) {
            onSuccess();
        }
        return true;
    } catch (error) {
        console.error('Delete Error:', error);
        alert('通信エラーが発生しました。');
        return false;
    }
};
