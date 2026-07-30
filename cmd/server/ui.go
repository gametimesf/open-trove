package main

import (
	"fmt"
	"html/template"
	"time"

	"github.com/gametimesf/open-trove/storage"
)

const uploadPageHTML = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>trove — upload</title>
<style>
  * { margin: 0; padding: 0; box-sizing: border-box; }
  body { font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif; background: #f5f5f5; color: #333; display: flex; align-items: center; justify-content: center; min-height: 100vh; }
  .container { background: #fff; border-radius: 12px; box-shadow: 0 2px 12px rgba(0,0,0,0.08); padding: 40px; max-width: 480px; width: 100%; }
  h1 { font-size: 24px; }
  .drop-zone { border: 2px dashed #ccc; border-radius: 8px; padding: 40px 20px; text-align: center; cursor: pointer; transition: border-color 0.2s, background 0.2s; margin-bottom: 16px; }
  .drop-zone:hover, .drop-zone.drag-over { border-color: #4a90d9; background: #f0f7ff; }
  .drop-zone p { color: #888; font-size: 14px; }
  .file-info { font-size: 13px; color: #666; margin-bottom: 16px; padding: 8px 12px; background: #f9f9f9; border-radius: 6px; display: none; }
  .file-info.visible { display: block; }
  label { display: block; font-size: 13px; font-weight: 500; margin-bottom: 4px; color: #555; }
  input[type="text"] { width: 100%; padding: 8px 12px; border: 1px solid #ddd; border-radius: 6px; font-size: 14px; margin-bottom: 16px; }
  button { background: #4a90d9; color: #fff; border: none; padding: 10px 20px; border-radius: 6px; font-size: 14px; cursor: pointer; width: 100%; transition: background 0.2s; }
  button:hover { background: #357abd; }
  button:disabled { background: #ccc; cursor: not-allowed; }
  .result { margin-top: 20px; display: none; }
  .result.visible { display: block; }
  .result-url { display: flex; gap: 8px; align-items: center; }
  .result-url a { flex: 1; word-break: break-all; font-size: 14px; color: #4a90d9; }
  .copy-btn { background: #eee; color: #333; padding: 8px 12px; width: auto; font-size: 13px; }
  .copy-btn:hover { background: #ddd; }
  .error { color: #d32f2f; font-size: 13px; margin-top: 8px; display: none; }
  .error.visible { display: block; }
</style>
</head>
<body>
<div class="container">
  <div style="display:flex;align-items:center;justify-content:space-between;margin-bottom:24px;">
    <h1 style="margin-bottom:0;">trove</h1>
    <a href="/mine" style="font-size:13px;color:#4a90d9;text-decoration:none;">My Trove</a>
  </div>
  <form id="upload-form" enctype="multipart/form-data">
    <div class="drop-zone" id="drop-zone">
      <p>Drop a file or ZIP website here, or click to browse</p>
      <input type="file" name="file" id="file-input" style="display:none">
    </div>
    <div class="file-info" id="file-info"></div>
    <label for="slug">Friendly name (optional)</label>
    <input type="text" id="slug" name="slug" placeholder="e.g. my-report">
    <label style="display:none;font-weight:400;cursor:pointer;user-select:none;" id="overwrite-label"><input type="checkbox" id="overwrite"> Replace existing file with this name</label>
    <button type="submit" id="submit-btn" disabled>Upload</button>
  </form>
  <div class="error" id="error"></div>
  <div class="result" id="result">
    <div class="result-url">
      <a id="result-link" href="#" target="_blank"></a>
      <button class="copy-btn" id="copy-btn">Copy</button>
    </div>
  </div>
</div>
<script src="/identity.js"></script>
<script>
  const dropZone = document.getElementById('drop-zone');
  const fileInput = document.getElementById('file-input');
  const fileInfo = document.getElementById('file-info');
  const form = document.getElementById('upload-form');
  const submitBtn = document.getElementById('submit-btn');
  const errorEl = document.getElementById('error');
  const resultEl = document.getElementById('result');
  const resultLink = document.getElementById('result-link');
  const copyBtn = document.getElementById('copy-btn');

  let selectedFile = null;

  dropZone.addEventListener('click', () => fileInput.click());
  dropZone.addEventListener('dragover', e => { e.preventDefault(); dropZone.classList.add('drag-over'); });
  dropZone.addEventListener('dragleave', () => dropZone.classList.remove('drag-over'));
  dropZone.addEventListener('drop', e => {
    e.preventDefault();
    dropZone.classList.remove('drag-over');
    if (e.dataTransfer.files.length) selectFile(e.dataTransfer.files[0]);
  });
  fileInput.addEventListener('change', () => { if (fileInput.files.length) selectFile(fileInput.files[0]); });

  function selectFile(f) {
    selectedFile = f;
    const size = f.size < 1024 ? f.size + ' B' : f.size < 1048576 ? (f.size/1024).toFixed(1) + ' KB' : (f.size/1048576).toFixed(1) + ' MB';
    fileInfo.textContent = f.name + ' (' + size + ', ' + (f.type || 'unknown type') + ')';
    fileInfo.classList.add('visible');
    submitBtn.disabled = false;
  }

  form.addEventListener('submit', async e => {
    e.preventDefault();
    if (!selectedFile) return;
    errorEl.classList.remove('visible');
    resultEl.classList.remove('visible');
    submitBtn.disabled = true;
    submitBtn.textContent = 'Uploading…';

    const fd = new FormData();
    fd.append('file', selectedFile);
    const slug = document.getElementById('slug').value.trim();
    if (slug) fd.append('slug', slug);
    if (document.getElementById('overwrite').checked) fd.append('overwrite', 'true');

    try {
      const res = await fetch('/upload', { method: 'POST', body: fd });
      const data = await res.json();
      if (!res.ok) {
        errorEl.textContent = data.error || 'Upload failed';
        errorEl.classList.add('visible');
        return;
      }
      resultLink.href = data.url;
      resultLink.textContent = data.url;
      resultEl.classList.add('visible');
    } catch (err) {
      errorEl.textContent = 'Upload failed: ' + err.message;
      errorEl.classList.add('visible');
    } finally {
      submitBtn.disabled = false;
      submitBtn.textContent = 'Upload';
    }
  });

  const slugInput = document.getElementById('slug');
  const overwriteLabel = document.getElementById('overwrite-label');
  slugInput.addEventListener('input', () => {
    overwriteLabel.style.display = slugInput.value.trim() ? 'block' : 'none';
    if (!slugInput.value.trim()) document.getElementById('overwrite').checked = false;
  });

  copyBtn.addEventListener('click', () => {
    navigator.clipboard.writeText(resultLink.href).then(() => {
      copyBtn.textContent = 'Copied!';
      setTimeout(() => copyBtn.textContent = 'Copy', 1500);
    });
  });
</script>
</body>
</html>`

var viewerTemplate = template.Must(template.New("viewer").Parse(`<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>{{.Filename}} — trove</title>
<meta property="og:title" content="{{.Filename}}">
<meta property="og:description" content="{{.ContentType}}">
<meta property="og:site_name" content="trove">
<meta property="og:type" content="website">
<meta property="og:url" content="{{.BaseURL}}/{{.Slug}}">
<link rel="stylesheet" href="/_trove/comments.css">
{{if eq .ViewMode "code"}}<link rel="stylesheet" href="https://cdnjs.cloudflare.com/ajax/libs/highlight.js/11.11.1/styles/github.min.css">{{end}}
{{if eq .ViewMode "markdown"}}<link rel="stylesheet" href="https://cdnjs.cloudflare.com/ajax/libs/github-markdown-css/5.8.1/github-markdown-light.min.css">{{end}}
<style>
  * { margin: 0; padding: 0; box-sizing: border-box; }
  body { font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif; background: #f5f5f5; display: flex; flex-direction: column; height: 100vh; }
  .topbar { height: 48px; background: #fff; border-bottom: 1px solid #e0e0e0; display: flex; align-items: center; justify-content: space-between; padding: 0 16px; flex-shrink: 0; }
  .topbar .filename { font-size: 14px; font-weight: 500; color: #333; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .topbar a { background: #4a90d9; color: #fff; text-decoration: none; padding: 6px 14px; border-radius: 6px; font-size: 13px; }
  .topbar a:hover { background: #357abd; }
  .content { flex: 1; overflow: hidden; display: flex; align-items: center; justify-content: center; }
  iframe { width: 100%; height: 100%; border: none; }
  .content img { max-width: 90%; max-height: 90%; object-fit: contain; }
  .file-card { text-align: center; padding: 40px; }
  .file-card .type { font-size: 13px; color: #888; margin-bottom: 16px; }
  .file-card a { display: inline-block; background: #4a90d9; color: #fff; text-decoration: none; padding: 10px 24px; border-radius: 6px; font-size: 14px; }
  .file-card a:hover { background: #357abd; }
  .code-container { width: 100%; height: 100%; overflow: auto; }
  .code-container pre { margin: 0; height: 100%; }
  .code-container code { font-family: "SF Mono", "Fira Code", Menlo, Consolas, monospace; font-size: 13px; line-height: 1.5; tab-size: 4; }
  .csv-container { width: 100%; height: 100%; overflow: auto; padding: 0; }
  .csv-container table { border-collapse: collapse; font-family: "SF Mono", "Fira Code", Menlo, Consolas, monospace; font-size: 13px; width: max-content; min-width: 100%; }
  .csv-container thead { position: sticky; top: 0; z-index: 1; }
  .csv-container th { background: #2d2d2d; color: #fff; font-weight: 600; padding: 8px 12px; text-align: left; white-space: nowrap; border-bottom: 2px solid #444; }
  .csv-container td { padding: 6px 12px; white-space: nowrap; border-bottom: 1px solid #eee; }
  .csv-container tbody tr:hover { background: #f0f7ff; }
  .csv-col-0 { background-color: rgba(74,144,217,0.08); }
  .csv-col-1 { background-color: rgba(80,184,72,0.08); }
  .csv-col-2 { background-color: rgba(228,129,0,0.08); }
  .csv-col-3 { background-color: rgba(176,82,204,0.08); }
  .csv-col-4 { background-color: rgba(214,73,73,0.08); }
  .csv-col-5 { background-color: rgba(0,172,172,0.08); }
  .csv-container .csv-loading { padding: 40px; text-align: center; color: #888; font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif; }
  .markdown-container { width: 100%; height: 100%; overflow: auto; background: #fff; display: flex; justify-content: center; }
  .markdown-container .markdown-body { max-width: 980px; width: 100%; padding: 32px 40px; }
  .markdown-container .md-loading { padding: 40px; text-align: center; color: #888; }
  .docx-container { width: 100%; height: 100%; overflow: auto; background: #fff; display: flex; justify-content: center; }
  .docx-container .docx-wrapper { background: #fff; box-shadow: 0 2px 8px rgba(0,0,0,0.15); margin: 20px auto; padding: 0; }
  .docx-container .docx-loading { padding: 40px; text-align: center; color: #888; }
  .video-container { width: 100%; height: 100%; display: flex; align-items: center; justify-content: center; background: #000; }
  .video-container video { max-width: 100%; max-height: 100%; }
  .intake-veil { position: fixed; inset: 0; z-index: 9999; backdrop-filter: blur(28px); -webkit-backdrop-filter: blur(28px); background: rgba(183, 28, 28, 0.18); display: flex; align-items: center; justify-content: center; padding: 24px; }
  .intake-modal { background: #fff; border: 3px solid #b71c1c; border-radius: 16px; padding: 40px 44px; max-width: 600px; width: 100%; text-align: center; box-shadow: 0 12px 60px rgba(0,0,0,0.35); }
  .intake-modal .siren { font-size: 56px; line-height: 1; margin-bottom: 14px; }
  .intake-modal h1 { font-size: 24px; color: #b71c1c; margin: 0 0 12px 0; font-weight: 700; }
  .intake-modal .lede { color: #333; font-size: 15px; line-height: 1.45; margin-bottom: 14px; }
  .intake-modal .reassure { color: #555; font-size: 13px; line-height: 1.45; margin: 16px 0 20px 0; font-style: italic; }
  .intake-modal blockquote { font-size: 13px; color: #555; border-left: 4px solid #b71c1c; background: #fff5f5; padding: 12px 14px; margin: 16px 0; text-align: left; border-radius: 0 6px 6px 0; }
  .intake-modal .cta { display: inline-block; background: #b71c1c; color: #fff; padding: 14px 28px; border-radius: 8px; text-decoration: none; font-size: 15px; font-weight: 600; margin-top: 8px; box-shadow: 0 2px 8px rgba(183,28,28,0.3); }
  .intake-modal .cta:hover { background: #8e1414; }
  .intake-modal .escape { display: block; margin-top: 22px; font-size: 12px; color: #999; text-decoration: underline; cursor: pointer; }
  .intake-modal .escape:hover { color: #666; }
</style>
</head>
<body data-trove-slug="{{.Slug}}" data-trove-resource="" data-trove-view-mode="{{.ViewMode}}">
<script src="/identity.js"></script>
<div class="topbar">
  <span class="filename">{{.Filename}}</span>
  <div class="trove-topbar-actions" data-trove-comments-ui>
    <button type="button" id="trove-comments-toggle" class="trove-comments-toggle" aria-controls="trove-comments-drawer" aria-expanded="false">
      Comments <span id="trove-comments-count" class="trove-comments-count">0</span>
    </button>
    <a href="{{.RawURL}}" download="{{.DownloadName}}">Download</a>
  </div>
</div>
{{if .Flagged}}
<div class="intake-veil" id="intake-veil">
  <div class="intake-modal">
    <div class="siren">🚨</div>
    <h1>Sensitive content detected</h1>
    <p class="lede">This file was flagged by automated review and <strong>should not be in Trove</strong>.</p>
    {{if .FlagReason}}<blockquote>{{.FlagReason}}</blockquote>{{end}}
    <p class="lede">Please <strong>report this to {{.ReviewName}}</strong> so it can be reviewed or removed.</p>
    <p class="reassure">The attribution recorded with the upload can help the administrator contact its author and suggest a safer place to share it.</p>
    {{if .ReviewMailto}}<a class="cta" href="{{.ReviewMailto}}">📧 Report to {{.ReviewName}}</a>{{end}}
    <a class="escape" id="intake-escape" href="#">acknowledge risk and view anyway →</a>
  </div>
</div>
<script>
(function() {
  var esc = document.getElementById('intake-escape');
  esc.addEventListener('click', function(e) {
    e.preventDefault();
    if (!confirm("This file was flagged as sensitive content that should not be in Trove. Are you sure you want to view it?")) return;
    if (!confirm("Final confirmation: by viewing this content, you acknowledge it may contain HR, executive, customer-PII, or otherwise restricted information. Continue?")) return;
    document.getElementById('intake-veil').style.display = 'none';
  });
})();
</script>
{{end}}
<div class="trove-workspace">
<div class="content" id="trove-content">
{{if eq .ViewMode "iframe"}}
  <iframe src="{{.RawURL}}" sandbox="allow-scripts allow-popups allow-same-origin allow-top-navigation-by-user-activation"></iframe>
{{else if eq .ViewMode "image"}}
  <img src="{{.RawURL}}" alt="{{.Filename}}">
{{else if eq .ViewMode "video"}}
  <div class="video-container">
    <video controls preload="metadata" src="{{.RawURL}}">
      Your browser does not support HTML5 video.
    </video>
  </div>
{{else if eq .ViewMode "csv"}}
  <div class="csv-container">
    <div class="csv-loading" id="csv-loading">Loading...</div>
    <table id="csv-table" style="display:none"></table>
  </div>
{{else if eq .ViewMode "docx"}}
  <div class="docx-container">
    <div class="docx-loading" id="docx-loading">Loading document...</div>
    <div id="docx-wrapper"></div>
  </div>
{{else if eq .ViewMode "markdown"}}
  <div class="markdown-container">
    <div class="md-loading" id="md-loading">Loading...</div>
    <article class="markdown-body" id="md-body" style="display:none"></article>
  </div>
{{else if eq .ViewMode "code"}}
  <div class="code-container">
    <pre><code id="code-block" class="language-{{.Language}}">Loading...</code></pre>
  </div>
{{else}}
  <div class="file-card">
    <div class="type">{{.ContentType}}</div>
    <a href="{{.RawURL}}" download="{{.DownloadName}}">Download {{.Filename}}</a>
  </div>
{{end}}
</div>
<aside id="trove-comments-drawer" class="trove-comments-drawer" aria-label="Artifact comments" aria-hidden="true" data-trove-comments-ui>
  <div class="trove-comments-drawer__inner">
    <div class="trove-comments-drawer__header">
      <span class="trove-comments-drawer__title">Comments</span>
      <button type="button" id="trove-comments-close" class="trove-comments-close" aria-label="Close comments">×</button>
    </div>
    <p class="trove-comment-mode-note">Select an element or highlight text, then leave a comment. Clear the selection to comment on the whole file.</p>
    <form id="trove-comment-form" class="trove-comment-form">
      <div class="trove-comment-target">
        <span id="trove-comment-target-label" class="trove-comment-target-label">Whole file</span>
        <button type="button" id="trove-comment-target-reset" class="trove-comment-target-reset" hidden>Clear</button>
      </div>
      <textarea id="trove-comment-body" class="trove-comment-body" maxlength="5000" required placeholder="Leave a comment…" aria-label="Comment"></textarea>
      <div class="trove-comment-form__actions">
        <span id="trove-comment-status" class="trove-comment-status" aria-live="polite"></span>
        <button type="submit" id="trove-comment-submit" class="trove-comment-submit">Post comment</button>
      </div>
    </form>
    <div class="trove-comments-filter">
      <label><input type="checkbox" id="trove-comments-show-resolved"> Show resolved threads</label>
    </div>
    <ol id="trove-comments-list" class="trove-comments-list"></ol>
  </div>
</aside>
</div>
<div id="trove-comment-hover-box" class="trove-comment-hover-box" data-trove-comments-ui hidden></div>
<div id="trove-comment-anchor-box" class="trove-comment-anchor-box" data-trove-comments-ui hidden></div>
{{if eq .ViewMode "csv"}}
<script src="https://cdnjs.cloudflare.com/ajax/libs/PapaParse/5.4.1/papaparse.min.js"></script>
<script>
(function() {
  var loading = document.getElementById('csv-loading');
  var table = document.getElementById('csv-table');
  fetch('{{.RawURL}}')
    .then(function(res) { return res.text(); })
    .then(function(text) {
      var result = Papa.parse(text.trim(), {skipEmptyLines: true});
      var rows = result.data;
      if (rows.length === 0) { loading.textContent = 'Empty file.'; return; }
      var thead = document.createElement('thead');
      var headerRow = document.createElement('tr');
      var headers = rows[0];
      headers.forEach(function(cell, i) {
        var th = document.createElement('th');
        th.textContent = cell;
        th.className = 'csv-col-' + (i % 6);
        headerRow.appendChild(th);
      });
      thead.appendChild(headerRow);
      table.appendChild(thead);
      var tbody = document.createElement('tbody');
      for (var r = 1; r < rows.length; r++) {
        var tr = document.createElement('tr');
        for (var c = 0; c < headers.length; c++) {
          var td = document.createElement('td');
          td.textContent = rows[r][c] || '';
          td.className = 'csv-col-' + (c % 6);
          tr.appendChild(td);
        }
        tbody.appendChild(tr);
      }
      table.appendChild(tbody);
      loading.style.display = 'none';
      table.style.display = 'table';
    })
    .catch(function() { loading.textContent = 'Failed to load CSV.'; });
})();
</script>
{{else if eq .ViewMode "docx"}}
<script src="https://unpkg.com/jszip/dist/jszip.min.js"></script>
<script src="https://cdn.jsdelivr.net/npm/docx-preview@0.3.7/dist/docx-preview.min.js"></script>
<script>
(function() {
  var loading = document.getElementById('docx-loading');
  var wrapper = document.getElementById('docx-wrapper');
  fetch('{{.RawURL}}')
    .then(function(res) { return res.arrayBuffer(); })
    .then(function(buf) {
      loading.style.display = 'none';
      return docx.renderAsync(buf, wrapper, null, {
        inWrapper: false,
        ignoreWidth: false,
        ignoreHeight: true
      });
    })
    .catch(function() {
      loading.textContent = 'Failed to render document.';
    });
})();
</script>
{{else if eq .ViewMode "markdown"}}
<script src="https://cdn.jsdelivr.net/npm/marked/marked.min.js"></script>
<script src="https://cdn.jsdelivr.net/npm/mermaid/dist/mermaid.min.js"></script>
<script>
(function() {
  var loading = document.getElementById('md-loading');
  var body = document.getElementById('md-body');
  mermaid.initialize({startOnLoad: false, theme: 'default'});

  var renderer = new marked.Renderer();
  renderer.code = function(token) {
    if (token.lang === 'mermaid') {
      return '<pre class="mermaid">' + token.text + '</pre>';
    }
    var escaped = token.text.replace(/&/g,'&amp;').replace(/</g,'&lt;').replace(/>/g,'&gt;');
    return '<pre><code class="language-' + (token.lang || '') + '">' + escaped + '</code></pre>';
  };
  marked.use({renderer: renderer});

  fetch('{{.RawURL}}')
    .then(function(res) { return res.text(); })
    .then(function(text) {
      body.innerHTML = marked.parse(text);
      loading.style.display = 'none';
      body.style.display = 'block';
      mermaid.run({nodes: body.querySelectorAll('.mermaid')});
    })
    .catch(function() { loading.textContent = 'Failed to load markdown.'; });
})();
</script>
{{else if eq .ViewMode "code"}}
<script src="https://cdnjs.cloudflare.com/ajax/libs/highlight.js/11.11.1/highlight.min.js"></script>
<script>
(function() {
  var el = document.getElementById('code-block');
  fetch('{{.RawURL}}')
    .then(function(res) { return res.text(); })
    .then(function(text) {
      el.textContent = text;
      if (text.length < 500 * 1024) {
        hljs.highlightElement(el);
      }
    })
    .catch(function() {
      el.textContent = 'Failed to load file content.';
    });
})();
</script>
{{end}}
<script src="/_trove/comments.js"></script>
</body>
</html>`))

var myTroveTemplate = template.Must(template.New("mytrove").Funcs(template.FuncMap{
	"reltime": func(at string) string {
		t, err := time.Parse(time.RFC3339, at)
		if err != nil {
			return at
		}
		d := time.Since(t)
		switch {
		case d < time.Minute:
			return "just now"
		case d < time.Hour:
			m := int(d.Minutes())
			if m == 1 {
				return "1 minute ago"
			}
			return fmt.Sprintf("%d minutes ago", m)
		case d < 24*time.Hour:
			h := int(d.Hours())
			if h == 1 {
				return "1 hour ago"
			}
			return fmt.Sprintf("%d hours ago", h)
		case d < 7*24*time.Hour:
			days := int(d.Hours() / 24)
			if days == 1 {
				return "1 day ago"
			}
			return fmt.Sprintf("%d days ago", days)
		default:
			return t.Format("Jan 2, 2006")
		}
	},
	"reversed": func(records []storage.ActivityRecord) []storage.ActivityRecord {
		out := make([]storage.ActivityRecord, len(records))
		for i, r := range records {
			out[len(records)-1-i] = r
		}
		return out
	},
}).Parse(`<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>My Trove</title>
<style>
  * { margin: 0; padding: 0; box-sizing: border-box; }
  body { font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif; background: #f5f5f5; color: #333; min-height: 100vh; padding: 40px 20px; }
  .container { max-width: 960px; margin: 0 auto; }
  .header { display: flex; align-items: center; justify-content: space-between; margin-bottom: 32px; }
  .header h1 { font-size: 24px; }
  .header a { font-size: 13px; color: #4a90d9; text-decoration: none; }
  .columns { display: grid; grid-template-columns: 1fr 1fr; gap: 24px; }
  @media (max-width: 640px) { .columns { grid-template-columns: 1fr; } }
  .column { background: #fff; border-radius: 12px; box-shadow: 0 2px 12px rgba(0,0,0,0.08); padding: 24px; }
  .column h2 { font-size: 16px; margin-bottom: 16px; color: #555; }
  .empty { color: #aaa; font-size: 14px; text-align: center; padding: 32px 0; }
  .item { display: flex; align-items: center; justify-content: space-between; padding: 10px 0; border-bottom: 1px solid #f0f0f0; }
  .item:last-child { border-bottom: none; }
  .item-left a { color: #4a90d9; text-decoration: none; font-size: 14px; font-weight: 500; word-break: break-all; }
  .item-left a:hover { text-decoration: underline; }
  .item-left .meta { font-size: 12px; color: #999; margin-top: 2px; }
  .item-right { font-size: 12px; color: #aaa; white-space: nowrap; margin-left: 12px; }
</style>
</head>
<body>
<div class="container">
  <div class="header">
    <h1>My Trove</h1>
    <a href="/">Upload</a>
  </div>
  <div class="columns">
    <div class="column">
      <h2>My Uploads</h2>
      {{if not .Uploads}}
        <div class="empty">No uploads yet</div>
      {{else}}
        {{range reversed .Uploads}}
        <div class="item">
          <div class="item-left">
            <a href="/{{.Slug}}">{{.Filename}}</a>
            <div class="meta">{{.ContentType}}</div>
          </div>
          <div class="item-right">{{reltime .At}}</div>
        </div>
        {{end}}
      {{end}}
    </div>
    <div class="column">
      <h2>Recently Viewed</h2>
      {{if not .Views}}
        <div class="empty">No views yet</div>
      {{else}}
        {{range reversed .Views}}
        <div class="item">
          <div class="item-left">
            <a href="/{{.Slug}}">{{.Filename}}</a>
            <div class="meta">{{.ContentType}}</div>
          </div>
          <div class="item-right">{{reltime .At}}</div>
        </div>
        {{end}}
      {{end}}
    </div>
  </div>
</div>
<script src="/identity.js"></script>
</body>
</html>`))

var siteViewerTemplate = template.Must(template.New("siteviewer").Parse(`<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>{{.Slug}} — trove</title>
<link rel="stylesheet" href="/_trove/comments.css">
<style>
  * { margin: 0; padding: 0; box-sizing: border-box; }
  body { font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif; background: #f5f5f5; display: flex; flex-direction: column; height: 100vh; }
  .topbar { height: 48px; background: #fff; border-bottom: 1px solid #e0e0e0; display: flex; align-items: center; justify-content: space-between; padding: 0 16px; flex-shrink: 0; }
  .topbar .sitename { font-size: 14px; font-weight: 500; color: #333; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .topbar a { background: #4a90d9; color: #fff; text-decoration: none; padding: 6px 14px; border-radius: 6px; font-size: 13px; }
  .topbar a:hover { background: #357abd; }
  .content { flex: 1; overflow: hidden; }
  iframe { width: 100%; height: 100%; border: none; }
</style>
</head>
<body data-trove-slug="{{.Slug}}" data-trove-resource="index.html" data-trove-view-mode="site">
<div class="topbar">
  <span class="sitename">{{.Slug}}</span>
  <div class="trove-topbar-actions" data-trove-comments-ui>
    <button type="button" id="trove-comments-toggle" class="trove-comments-toggle" aria-controls="trove-comments-drawer" aria-expanded="false">
      Comments <span id="trove-comments-count" class="trove-comments-count">0</span>
    </button>
    <a id="open-link" href="{{.BaseURL}}/{{.Slug}}/index.html" target="_blank">Open</a>
  </div>
</div>
<div class="trove-workspace">
<div class="content" id="trove-content">
  <iframe id="site-frame" sandbox="allow-scripts allow-popups allow-same-origin allow-top-navigation-by-user-activation"></iframe>
</div>
<aside id="trove-comments-drawer" class="trove-comments-drawer" aria-label="Artifact comments" aria-hidden="true" data-trove-comments-ui>
  <div class="trove-comments-drawer__inner">
    <div class="trove-comments-drawer__header">
      <span class="trove-comments-drawer__title">Comments</span>
      <button type="button" id="trove-comments-close" class="trove-comments-close" aria-label="Close comments">×</button>
    </div>
    <p class="trove-comment-mode-note">Select an element or highlight text, then leave a comment. Clear the selection to comment on this page.</p>
    <form id="trove-comment-form" class="trove-comment-form">
      <div class="trove-comment-target">
        <span id="trove-comment-target-label" class="trove-comment-target-label">This page</span>
        <button type="button" id="trove-comment-target-reset" class="trove-comment-target-reset" hidden>Clear</button>
      </div>
      <textarea id="trove-comment-body" class="trove-comment-body" maxlength="5000" required placeholder="Leave a comment…" aria-label="Comment"></textarea>
      <div class="trove-comment-form__actions">
        <span id="trove-comment-status" class="trove-comment-status" aria-live="polite"></span>
        <button type="submit" id="trove-comment-submit" class="trove-comment-submit">Post comment</button>
      </div>
    </form>
    <div class="trove-comments-filter">
      <label><input type="checkbox" id="trove-comments-show-resolved"> Show resolved threads</label>
    </div>
    <ol id="trove-comments-list" class="trove-comments-list"></ol>
  </div>
</aside>
</div>
<div id="trove-comment-hover-box" class="trove-comment-hover-box" data-trove-comments-ui hidden></div>
<div id="trove-comment-anchor-box" class="trove-comment-anchor-box" data-trove-comments-ui hidden></div>
<script src="/identity.js"></script>
<script>
(function() {
  var frame = document.getElementById('site-frame');
  var openLink = document.getElementById('open-link');
  var slug = '{{.Slug}}';
  var base = '{{.BaseURL}}';

  var params = new URLSearchParams(window.location.search);
  var page = params.get('page') || 'index.html';
  frame.src = '/' + slug + '/' + page;

  frame.addEventListener('load', function() {
    try {
      var path = frame.contentWindow.location.pathname;
      var currentPage = path.replace('/' + slug + '/', '');
      var newQuery = currentPage === 'index.html' ? '' : '?page=' + encodeURIComponent(currentPage);
      history.replaceState(null, '', window.location.pathname + newQuery);
      openLink.href = base + '/' + slug + '/' + currentPage;
      document.body.dataset.troveResource = currentPage;
      window.dispatchEvent(new CustomEvent('trove:resource-change', {detail: {resource: currentPage}}));
    } catch(e) {}
  });
})();
</script>
<script src="/_trove/comments.js"></script>
</body>
</html>`))

var errorTemplate = template.Must(template.New("error").Parse(`<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>{{.Code}} — trove</title>
<style>
  * { margin: 0; padding: 0; box-sizing: border-box; }
  body { font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif; background: #f5f5f5; display: flex; align-items: center; justify-content: center; min-height: 100vh; }
  .error { text-align: center; }
  .error h1 { font-size: 48px; color: #ccc; }
  .error p { font-size: 16px; color: #888; margin-top: 8px; }
</style>
</head>
<body>
<div class="error">
  <h1>{{.Code}}</h1>
  <p>{{.Message}}</p>
</div>
<script src="/identity.js"></script>
</body>
</html>`))
