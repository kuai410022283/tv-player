// ═══════════════════════════════════════════════════════
// MediaPlayer Admin - login.js (独立登录页脚本)
// ═══════════════════════════════════════════════════════

const API = '/api/v1';

function toast(msg, type = 'success') {
  const el = document.getElementById('toast');
  el.textContent = msg;
  el.className = 'toast toast-' + type;
  el.style.display = 'block';
  setTimeout(function () { el.style.display = 'none'; }, 3000);
}

async function doLogin() {
  const password = document.getElementById('login-password').value;
  if (!password) { toast(t('login.error_enter_password', '请输入密码'), 'error'); return; }
  try {
    const res = await fetch(API + '/admin/login', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ password })
    });
    const data = await res.json();
    if (data.code === 0 && data.data && data.data.token) {
      localStorage.setItem('admin_token', data.data.token);
      toast(t('login.success', '登录成功'), 'success');
      setTimeout(function () { window.location.href = '/admin/'; }, 500);
    } else {
      toast(data.message || t('login.error_wrong_password', '密码错误'), 'error');
    }
  } catch (e) {
    toast(t('login.failed', '登录失败') + ': ' + e.message, 'error');
  }
}