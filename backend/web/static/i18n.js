class I18nManager {
  constructor() {
    this.currentLang = localStorage.getItem('admin_lang') || 'zh-CN';
    this.translations = {};
  }

  async init() {
    try {
      const res = await fetch(`/static/i18n/${this.currentLang}.json`);
      this.translations = await res.json();
      this.translatePage();
      
      // Update selector UI if present
      const selector = document.getElementById('lang-selector');
      if (selector) {
        selector.value = this.currentLang;
      }
    } catch (e) {
      console.error('Failed to load translations:', e);
    }
  }

  async switchLanguage(lang) {
    this.currentLang = lang;
    localStorage.setItem('admin_lang', lang);
    await this.init();
    
    // Also update sub-iframes if they exist and are loaded
    const logsIframe = document.getElementById('logs-iframe');
    if (logsIframe && logsIframe.contentWindow && logsIframe.contentWindow.location.href !== 'about:blank') {
      logsIframe.contentWindow.location.reload();
    }
    const manualIframe = document.getElementById('manual-iframe');
    if (manualIframe && manualIframe.contentWindow && manualIframe.contentWindow.location.href !== 'about:blank') {
      const targetSrc = (lang === 'zh-CN' || lang === 'zh-TW') ? '/admin/manual.html' : '/admin/manual_en.html';
      manualIframe.src = targetSrc;
    }
  }

  getNestedValue(path) {
    return path.split('.').reduce((obj, key) => (obj && obj[key] !== undefined) ? obj[key] : null, this.translations);
  }

  translatePage() {
    // Translate text content
    document.querySelectorAll('[data-i18n]').forEach(el => {
      const key = el.getAttribute('data-i18n');
      const text = this.getNestedValue(key);
      if (text) {
        if (/<[a-z][\s\S]*>/i.test(text)) {
          el.innerHTML = text;
        } else {
          el.textContent = text;
        }
      }
    });

    // Translate placeholders
    document.querySelectorAll('[data-i18n-placeholder]').forEach(el => {
      const key = el.getAttribute('data-i18n-placeholder');
      const text = this.getNestedValue(key);
      if (text) el.setAttribute('placeholder', text);
    });

    // Update document title
    const docTitle = this.getNestedValue('sidebar.logo');
    if (docTitle) {
      document.title = docTitle + ' - Admin';
    }
  }
}

window.i18n = new I18nManager();
window.t = function(key, fallback) {
  return window.i18n ? window.i18n.getNestedValue(key) || fallback : fallback;
};
document.addEventListener('DOMContentLoaded', () => window.i18n.init());
