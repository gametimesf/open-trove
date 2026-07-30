package main

import (
	"net/http"

	"github.com/labstack/echo/v4"
)

const userIdentityJS = `(() => {
  'use strict';

  const HEADER_NAME = 'X-Trove-User-Email';
  const COOKIE_NAME = 'trove_user_email';
  const STORAGE_PREFIX = 'trove.userEmail.';
  const MAX_LENGTH = 254;

  let promptPromise = null;
  let promptResolve = null;

  function normalizeEmail(value) {
    const email = String(value || '').trim().toLowerCase();
    if (!email || email.length > MAX_LENGTH) return '';
    if (!/^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(email)) return '';
    return email;
  }

  function storageKey() {
    return STORAGE_PREFIX + (window.location.host || 'local');
  }

  function readCookie() {
    const prefix = COOKIE_NAME + '=';
    for (const part of String(document.cookie || '').split(';')) {
      const value = part.trim();
      if (!value.startsWith(prefix)) continue;
      try {
        return normalizeEmail(decodeURIComponent(value.slice(prefix.length)));
      } catch (_err) {
        return normalizeEmail(value.slice(prefix.length));
      }
    }
    return '';
  }

  function writeCookie(email) {
    const secure = window.location.protocol === 'https:' ? '; Secure' : '';
    document.cookie = COOKIE_NAME + '=' + encodeURIComponent(email) +
      '; Path=/; Max-Age=31536000; SameSite=Lax' + secure;
  }

  function clearCookie() {
    document.cookie = COOKIE_NAME + '=; Path=/; Max-Age=0; SameSite=Lax';
  }

  function getEmail() {
    let email = '';
    try {
      email = normalizeEmail(window.localStorage.getItem(storageKey()));
    } catch (_err) {
      email = '';
    }
    if (!email) email = readCookie();
    if (email) setEmail(email, { silent: true });
    return email;
  }

  function setEmail(value, options) {
    const email = normalizeEmail(value);
    if (!email) return '';
    try {
      window.localStorage.setItem(storageKey(), email);
    } catch (_err) {
      // The cookie remains the browser-to-server transport.
    }
    writeCookie(email);
    if (!options || !options.silent) updateChip(email);
    return email;
  }

  function clearEmail() {
    try {
      window.localStorage.removeItem(storageKey());
    } catch (_err) {
      // Ignore storage failures; the cookie is still cleared below.
    }
    clearCookie();
    updateChip('');
  }

  function updateChip(email) {
    const chip = document.getElementById('trove-user-email-chip');
    if (!chip) return;
    chip.textContent = email || 'Set email';
    chip.title = email ? 'Change Trove audit email' : 'Set Trove audit email';
  }

  function injectUI() {
    if (!document.body || document.getElementById('trove-user-email-mask')) return;

    const style = document.createElement('style');
    style.textContent = [
      '.trove-user-email-chip{position:fixed;right:16px;bottom:16px;z-index:19999;border:1px solid #d1d5db;border-radius:999px;background:#fff;color:#374151;padding:7px 12px;font:12px -apple-system,BlinkMacSystemFont,"Segoe UI",sans-serif;box-shadow:0 2px 10px rgba(0,0,0,.12);cursor:pointer;max-width:min(320px,calc(100vw - 32px));overflow:hidden;text-overflow:ellipsis;white-space:nowrap}',
      '.trove-user-email-mask{position:fixed;inset:0;z-index:20000;background:rgba(17,24,39,.48);backdrop-filter:blur(5px);display:flex;align-items:center;justify-content:center;padding:24px}',
      '.trove-user-email-mask[hidden]{display:none}',
      '.trove-user-email-modal{width:min(520px,100%);background:#fff;border-radius:12px;box-shadow:0 24px 80px rgba(0,0,0,.3);padding:32px;font-family:-apple-system,BlinkMacSystemFont,"Segoe UI",sans-serif;color:#111827}',
      '.trove-user-email-modal h2{font-size:26px;line-height:1.2;margin:0 0 10px}',
      '.trove-user-email-modal p{font-size:15px;line-height:1.5;color:#6b7280;margin:0 0 22px}',
      '.trove-user-email-modal label{display:block;font-size:14px;font-weight:600;color:#374151;margin-bottom:7px}',
      '.trove-user-email-modal input{display:block;width:100%;border:1px solid #d1d5db;border-radius:8px;padding:12px 14px;font-size:16px;outline:none}',
      '.trove-user-email-modal input:focus{border-color:#4a90d9;box-shadow:0 0 0 3px rgba(74,144,217,.16)}',
      '.trove-user-email-error{font-size:13px;color:#b91c1c;margin-top:8px}',
      '.trove-user-email-actions{display:flex;justify-content:flex-end;gap:10px;margin-top:24px}',
      '.trove-user-email-actions button{width:auto;border:0;border-radius:8px;padding:10px 16px;font-size:14px;cursor:pointer}',
      '.trove-user-email-reset{background:#f3f4f6;color:#374151}',
      '.trove-user-email-save{background:#111827;color:#fff}'
    ].join('');
    document.head.appendChild(style);

    const chip = document.createElement('button');
    chip.id = 'trove-user-email-chip';
    chip.type = 'button';
    chip.className = 'trove-user-email-chip';
    chip.addEventListener('click', () => showPrompt({ force: true, reloadOnSave: true }));
    document.body.appendChild(chip);

    const mask = document.createElement('div');
    mask.id = 'trove-user-email-mask';
    mask.className = 'trove-user-email-mask';
    mask.hidden = true;
    mask.innerHTML =
      '<div class="trove-user-email-modal" role="dialog" aria-modal="true" aria-labelledby="trove-user-email-title">' +
        '<h2 id="trove-user-email-title">What’s your email?</h2>' +
        '<p>Enter your email. Trove uses it for audit trails only — it doesn’t grant access.</p>' +
        '<label for="trove-user-email-input">Email address</label>' +
        '<input id="trove-user-email-input" type="email" autocomplete="email" placeholder="you@example.com" maxlength="254">' +
        '<div class="trove-user-email-error" id="trove-user-email-error" hidden>Enter a valid email address.</div>' +
        '<div class="trove-user-email-actions">' +
          '<button class="trove-user-email-reset" id="trove-user-email-reset" type="button">Reset</button>' +
          '<button class="trove-user-email-save" id="trove-user-email-save" type="button">Continue</button>' +
        '</div>' +
      '</div>';
    document.body.appendChild(mask);

    const input = document.getElementById('trove-user-email-input');
    const error = document.getElementById('trove-user-email-error');

    function submit() {
      const email = setEmail(input.value);
      if (!email) {
        error.hidden = false;
        input.focus();
        return;
      }
      error.hidden = true;
      mask.hidden = true;
      const resolve = promptResolve;
      const reload = mask.dataset.reloadOnSave === 'true';
      promptPromise = null;
      promptResolve = null;
      if (resolve) resolve(email);
      if (reload) window.location.reload();
    }

    document.getElementById('trove-user-email-save').addEventListener('click', submit);
    document.getElementById('trove-user-email-reset').addEventListener('click', () => {
      clearEmail();
      input.value = '';
      error.hidden = true;
      input.focus();
    });
    input.addEventListener('keydown', event => {
      if (event.key === 'Enter') {
        event.preventDefault();
        submit();
      }
    });

    updateChip(getEmail());
  }

  function showPrompt(options) {
    injectUI();
    const current = getEmail();
    if (current && !(options && options.force)) return Promise.resolve(current);
    if (promptPromise) return promptPromise;

    const mask = document.getElementById('trove-user-email-mask');
    const input = document.getElementById('trove-user-email-input');
    const error = document.getElementById('trove-user-email-error');
    mask.dataset.reloadOnSave = options && options.reloadOnSave ? 'true' : 'false';
    input.value = current;
    error.hidden = true;
    mask.hidden = false;
    setTimeout(() => input.focus(), 0);

    promptPromise = new Promise(resolve => {
      promptResolve = resolve;
    });
    return promptPromise;
  }

  function shouldAttach(input) {
    try {
      const raw = input instanceof Request ? input.url : String(input);
      return new URL(raw, window.location.href).origin === window.location.origin;
    } catch (_err) {
      return false;
    }
  }

  function installFetchWrapper() {
    if (!window.fetch || window.fetch.__troveUserEmailWrapped) return;
    const originalFetch = window.fetch.bind(window);
    const wrappedFetch = async function(input, init) {
      if (!shouldAttach(input)) return originalFetch(input, init);
      const email = getEmail() || await showPrompt({ reloadOnSave: false });
      const nextInit = Object.assign({}, init || {});
      const headers = new Headers(
        nextInit.headers || (input instanceof Request ? input.headers : undefined)
      );
      headers.set(HEADER_NAME, email);
      nextInit.headers = headers;
      return originalFetch(input, nextInit);
    };
    wrappedFetch.__troveUserEmailWrapped = true;
    window.fetch = wrappedFetch;
  }

  function onReady(callback) {
    if (document.readyState === 'loading') {
      document.addEventListener('DOMContentLoaded', callback, { once: true });
    } else {
      callback();
    }
  }

  installFetchWrapper();
  window.TroveUserIdentity = {
    headerName: HEADER_NAME,
    getEmail,
    setEmail,
    clearEmail,
    ensureEmail: () => showPrompt({ reloadOnSave: false }),
    normalizeEmail
  };

  onReady(() => {
    injectUI();
    if (!getEmail()) showPrompt({ reloadOnSave: true });
  });
})();`

func handleUserIdentityJS(c echo.Context) error {
	return c.Blob(http.StatusOK, "application/javascript; charset=utf-8", []byte(userIdentityJS))
}
