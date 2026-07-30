(() => {
  "use strict";

  const body = document.body;
  const slug = body && body.dataset.troveSlug;
  const toggle = document.getElementById("trove-comments-toggle");
  const close = document.getElementById("trove-comments-close");
  const drawer = document.getElementById("trove-comments-drawer");
  const list = document.getElementById("trove-comments-list");
  const count = document.getElementById("trove-comments-count");
  const form = document.getElementById("trove-comment-form");
  const textarea = document.getElementById("trove-comment-body");
  const submit = document.getElementById("trove-comment-submit");
  const status = document.getElementById("trove-comment-status");
  const targetLabel = document.getElementById("trove-comment-target-label");
  const targetReset = document.getElementById("trove-comment-target-reset");
  const showResolved = document.getElementById("trove-comments-show-resolved");
  const content = document.getElementById("trove-content");
  const hoverBox = document.getElementById("trove-comment-hover-box");
  const anchorBox = document.getElementById("trove-comment-anchor-box");

  if (!body || !slug || !toggle || !drawer || !form || !content) return;

  let active = false;
  let currentVersion = "";
  let currentAnchor = fileAnchor();
  let currentTarget = null;
  let hoverTarget = null;
  let threads = [];
  let scheduled = false;
  const attachedDocuments = new WeakSet();
  const collapsedThreads = new Set();

  function resourcePath() {
    return String(body.dataset.troveResource || "").replace(/^\/+/, "");
  }

  function fileAnchor() {
    return {type: "file", resource: resourcePath()};
  }

  function apiURL(includeResolved) {
    const base = "/api/artifacts/" + encodeURIComponent(slug) + "/comments";
    const resource = resourcePath();
    const query = new URLSearchParams();
    if (resource) query.set("path", resource);
    if (includeResolved) query.set("resolved", "include");
    const encoded = query.toString();
    return encoded ? base + "?" + encoded : base;
  }

  function mutationURL(commentID, suffix) {
    const base = "/api/artifacts/" + encodeURIComponent(slug) + "/comments/" +
      encodeURIComponent(commentID) + (suffix || "");
    const resource = resourcePath();
    return resource ? base + "?path=" + encodeURIComponent(resource) : base;
  }

  function normalizeText(value, limit) {
    const text = String(value || "").replace(/\s+/g, " ").trim();
    return text.length > limit ? text.slice(0, limit - 1) + "…" : text;
  }

  function trimText(value, limit) {
    const text = String(value || "").trim();
    return text.length > limit ? text.slice(0, limit - 1) + "…" : text;
  }

  function cssEscape(value) {
    if (window.CSS && typeof window.CSS.escape === "function") {
      return window.CSS.escape(value);
    }
    return String(value).replace(/[^a-zA-Z0-9_-]/g, "\\$&");
  }

  function isCommentUI(node) {
    return !!(node && node.closest && node.closest("[data-trove-comments-ui]"));
  }

  function isIgnored(node) {
    return !!(node && node.closest && node.closest("[data-trove-comment-ignore]"));
  }

  function withinSurface(node, doc) {
    if (doc !== document) return true;
    return !!(node && node.closest && node.closest("#trove-content"));
  }

  function selectableTarget(node, doc) {
    if (!node || node.nodeType !== 1 || isCommentUI(node) || isIgnored(node) || !withinSurface(node, doc)) {
      return null;
    }
    if (node.matches("html, body, script, style, link, meta")) {
      return doc.querySelector("main, article, [data-trove-id]") || doc.body;
    }
    return node;
  }

  function stableID(element) {
    const owner = element.closest("[data-trove-id]");
    return owner ? owner.getAttribute("data-trove-id") || "" : "";
  }

  function selectorFor(element) {
    const stableOwner = element.closest("[data-trove-id]");
    if (stableOwner) {
      return '[data-trove-id="' + stableOwner.getAttribute("data-trove-id").replace(/"/g, '\\"') + '"]';
    }
    if (element.id) return "#" + cssEscape(element.id);
    if (element.getAttribute("name")) {
      return element.tagName.toLowerCase() + '[name="' +
        element.getAttribute("name").replace(/"/g, '\\"') + '"]';
    }

    const parts = [];
    let current = element;
    while (current && current.nodeType === 1 && current !== current.ownerDocument.body) {
      if (current.id) {
        parts.unshift("#" + cssEscape(current.id));
        break;
      }
      let part = current.tagName.toLowerCase();
      const parent = current.parentElement;
      if (parent) {
        const sameTag = Array.from(parent.children).filter(child => child.tagName === current.tagName);
        if (sameTag.length > 1) part += ":nth-of-type(" + (sameTag.indexOf(current) + 1) + ")";
      }
      parts.unshift(part);
      current = parent;
    }
    return parts.join(" > ");
  }

  function humanLabel(element) {
    const labeled = element.closest("[data-trove-label]");
    if (labeled) return normalizeText(labeled.getAttribute("data-trove-label"), 120);
    return normalizeText(
      element.getAttribute("aria-label") ||
      element.getAttribute("title") ||
      element.getAttribute("alt") ||
      element.textContent ||
      element.tagName.toLowerCase(),
      120
    );
  }

  function frameForDocument(doc) {
    const frames = Array.from(document.querySelectorAll("#trove-content iframe"));
    return frames.find(frame => {
      try {
        return frame.contentDocument === doc;
      } catch (_error) {
        return false;
      }
    }) || null;
  }

  function viewportRect(rect, doc) {
    const frame = frameForDocument(doc);
    if (!frame) return rect;
    const frameRect = frame.getBoundingClientRect();
    return {
      left: frameRect.left + rect.left,
      top: frameRect.top + rect.top,
      width: rect.width,
      height: rect.height,
      right: frameRect.left + rect.right,
      bottom: frameRect.top + rect.bottom
    };
  }

  function placeBox(box, rect, doc) {
    if (!box || !rect) return;
    const converted = viewportRect(rect, doc);
    if (!converted.width && !converted.height) {
      box.hidden = true;
      return;
    }
    box.style.left = Math.round(converted.left) + "px";
    box.style.top = Math.round(converted.top) + "px";
    box.style.width = Math.round(converted.width) + "px";
    box.style.height = Math.round(converted.height) + "px";
    box.hidden = false;
  }

  function targetRect(target) {
    if (!target) return null;
    if (target.range) return target.range.getBoundingClientRect();
    if (target.element && target.element.isConnected) return target.element.getBoundingClientRect();
    return null;
  }

  function renderBoxes() {
    if (!active) {
      hoverBox.hidden = true;
      anchorBox.hidden = true;
      return;
    }
    if (hoverTarget) {
      placeBox(hoverBox, targetRect(hoverTarget), hoverTarget.doc);
    } else {
      hoverBox.hidden = true;
    }
    if (currentTarget) {
      placeBox(anchorBox, targetRect(currentTarget), currentTarget.doc);
    } else {
      anchorBox.hidden = true;
    }
  }

  function scheduleBoxes() {
    if (scheduled) return;
    scheduled = true;
    window.requestAnimationFrame(() => {
      scheduled = false;
      renderBoxes();
    });
  }

  function setStatus(message, kind) {
    status.textContent = message || "";
    status.dataset.kind = kind || "";
  }

  function targetDescription(anchor) {
    if (!anchor || anchor.type === "file") return resourcePath() ? "This page" : "Whole file";
    if (anchor.type === "text") return "Text: “" + normalizeText(anchor.quote && anchor.quote.exact, 70) + "”";
    return anchor.label || anchor.stable_id || anchor.selector || "Selected element";
  }

  function renderTarget() {
    targetLabel.textContent = targetDescription(currentAnchor);
    targetReset.hidden = currentAnchor.type === "file";
  }

  function selectFile() {
    currentAnchor = fileAnchor();
    currentTarget = null;
    renderTarget();
    renderBoxes();
  }

  function elementAnchor(element, doc) {
    const rect = element.getBoundingClientRect();
    return {
      type: "element",
      resource: resourcePath(),
      stable_id: stableID(element),
      selector: selectorFor(element),
      label: humanLabel(element),
      tag: element.tagName.toLowerCase(),
      role: element.getAttribute("role") || "",
      visible_text: normalizeText(element.textContent, 500),
      rect: {
        x: Math.round(rect.left),
        y: Math.round(rect.top),
        width: Math.round(rect.width),
        height: Math.round(rect.height)
      }
    };
  }

  function nearestTextScope(range) {
    let node = range.commonAncestorContainer;
    if (node.nodeType !== 1) node = node.parentElement;
    return node && node.closest ? node.closest("[data-trove-id], p, li, td, th, pre, code, blockquote, h1, h2, h3, h4, h5, h6, article, section") || node : null;
  }

  function textAnchor(range, doc) {
    const exact = trimText(range.toString(), 1000);
    if (!exact) return null;
    const scope = nearestTextScope(range);
    const scopeText = trimText(scope ? scope.textContent : doc.body.textContent, 50000);
    const index = scopeText.indexOf(exact);
    const rect = range.getBoundingClientRect();
    return {
      anchor: {
        type: "text",
        resource: resourcePath(),
        stable_id: scope ? stableID(scope) : "",
        selector: scope ? selectorFor(scope) : "",
        label: "Selected text",
        visible_text: exact,
        quote: {
          exact: exact,
          prefix: index >= 0 ? scopeText.slice(Math.max(0, index - 48), index) : "",
          suffix: index >= 0 ? scopeText.slice(index + exact.length, index + exact.length + 48) : ""
        },
        rect: {
          x: Math.round(rect.left),
          y: Math.round(rect.top),
          width: Math.round(rect.width),
          height: Math.round(rect.height)
        }
      },
      target: {range: range.cloneRange(), doc: doc}
    };
  }

  function handlePointerMove(event) {
    if (!active) return;
    const doc = event.currentTarget;
    const element = selectableTarget(event.target, doc);
    hoverTarget = element ? {element: element, doc: doc} : null;
    scheduleBoxes();
  }

  function handleClick(event) {
    if (!active || isCommentUI(event.target)) return;
    const doc = event.currentTarget;
    const selection = doc.getSelection && doc.getSelection();
    if (selection && !selection.isCollapsed && normalizeText(selection.toString(), 2)) {
      event.preventDefault();
      event.stopImmediatePropagation();
      return;
    }
    const element = selectableTarget(event.target, doc);
    if (!element) return;
    event.preventDefault();
    event.stopImmediatePropagation();
    if (currentTarget && currentTarget.element && currentAnchor.type === "element" &&
        (currentTarget.element === element || currentTarget.element.contains(element))) {
      selectFile();
      textarea.focus();
      return;
    }
    currentAnchor = elementAnchor(element, doc);
    currentTarget = {element: element, doc: doc};
    renderTarget();
    renderBoxes();
    textarea.focus();
  }

  function handleMouseUp(event) {
    if (!active || isCommentUI(event.target)) return;
    const doc = event.currentTarget;
    window.setTimeout(() => {
      const selection = doc.getSelection && doc.getSelection();
      if (!selection || selection.isCollapsed || selection.rangeCount === 0) return;
      const captured = textAnchor(selection.getRangeAt(0), doc);
      if (!captured) return;
      currentAnchor = captured.anchor;
      currentTarget = captured.target;
      renderTarget();
      renderBoxes();
      textarea.focus();
    }, 0);
  }

  function attachDocument(doc) {
    if (!doc || attachedDocuments.has(doc)) return;
    attachedDocuments.add(doc);
    doc.addEventListener("pointermove", handlePointerMove, true);
    doc.addEventListener("click", handleClick, true);
    doc.addEventListener("mouseup", handleMouseUp, true);
    if (doc.defaultView) doc.defaultView.addEventListener("scroll", scheduleBoxes, true);
  }

  function attachFrames() {
    attachDocument(document);
    document.querySelectorAll("#trove-content iframe").forEach(frame => {
      const attach = () => {
        try {
          attachDocument(frame.contentDocument);
        } catch (_error) {
          // A page that navigates cross-origin remains file-commentable.
        }
        scheduleBoxes();
      };
      frame.addEventListener("load", attach);
      attach();
    });
  }

  function openComments() {
    active = true;
    body.classList.add("trove-comments-open", "trove-comments-mode");
    toggle.setAttribute("aria-expanded", "true");
    drawer.setAttribute("aria-hidden", "false");
    attachFrames();
    renderTarget();
    renderBoxes();
    window.setTimeout(scheduleBoxes, 220);
    textarea.focus();
  }

  function closeComments() {
    active = false;
    body.classList.remove("trove-comments-open", "trove-comments-mode");
    toggle.setAttribute("aria-expanded", "false");
    drawer.setAttribute("aria-hidden", "true");
    hoverTarget = null;
    selectFile();
    window.setTimeout(scheduleBoxes, 220);
  }

  function commentTargetLabel(comment) {
    const anchor = comment.anchor || {};
    if (anchor.type === "text") return "Text: “" + normalizeText(anchor.quote && anchor.quote.exact, 80) + "”";
    if (anchor.type === "element") return anchor.label || anchor.stable_id || "Element";
    return anchor.resource ? "Page: " + anchor.resource : "Whole file";
  }

  function currentUserEmail() {
    return window.TroveUserIdentity && window.TroveUserIdentity.getEmail ?
      window.TroveUserIdentity.getEmail() : "";
  }

  async function requestJSON(url, options) {
    const response = await window.fetch(url, options);
    const payload = await response.json().catch(() => ({}));
    if (!response.ok) throw new Error(payload.error || "Comment request failed");
    return payload;
  }

  function actionButton(label, className, action) {
    const button = document.createElement("button");
    button.type = "button";
    button.className = className;
    button.textContent = label;
    button.addEventListener("click", event => {
      event.stopPropagation();
      action();
    });
    return button;
  }

  function messageElement(comment) {
    const message = document.createElement("div");
    message.className = "trove-comment-message";
    if (comment.deleted_at) message.classList.add("trove-comment-message--deleted");

    const meta = document.createElement("div");
    meta.className = "trove-comment-card__meta";
    const author = document.createElement("span");
    author.className = "trove-comment-card__author";
    author.textContent = comment.author_email;
    const at = document.createElement("time");
    at.dateTime = comment.created_at;
    at.textContent = new Date(comment.created_at).toLocaleString();
    meta.append(author, at);

    const text = document.createElement("div");
    text.className = "trove-comment-card__body";
    text.textContent = comment.deleted_at ? "Comment deleted" : comment.body;
    message.append(meta, text);

    if (comment.edited_at && !comment.deleted_at) {
      const edited = document.createElement("div");
      edited.className = "trove-comment-card__edited";
      edited.textContent = "Edited " + new Date(comment.edited_at).toLocaleString();
      message.appendChild(edited);
    }
    if (comment.artifact_version && currentVersion && comment.artifact_version !== currentVersion) {
      const stale = document.createElement("div");
      stale.className = "trove-comment-card__stale";
      stale.textContent = "Created on an earlier version";
      message.appendChild(stale);
    }

    if (!comment.deleted_at && currentUserEmail().toLowerCase() === String(comment.author_email || "").toLowerCase()) {
      const actions = document.createElement("div");
      actions.className = "trove-comment-message__actions";
      actions.append(
        actionButton("Edit", "trove-comment-action", async () => {
          const replacement = window.prompt("Edit comment", comment.body);
          if (replacement === null || replacement.trim() === comment.body) return;
          setStatus("Saving edit…", "");
          try {
            await requestJSON(mutationURL(comment.id, ""), {
              method: "PATCH",
              credentials: "same-origin",
              headers: {"Content-Type": "application/json"},
              body: JSON.stringify({body: replacement})
            });
            await loadThreads();
            setStatus("Comment edited.", "");
          } catch (error) {
            setStatus(error.message || "Comment could not be edited", "error");
          }
        }),
        actionButton("Delete", "trove-comment-action trove-comment-action--danger", async () => {
          if (!window.confirm("Delete this comment?")) return;
          setStatus("Deleting comment…", "");
          try {
            await requestJSON(mutationURL(comment.id, ""), {
              method: "DELETE",
              credentials: "same-origin"
            });
            await loadThreads();
            setStatus("Comment deleted.", "");
          } catch (error) {
            setStatus(error.message || "Comment could not be deleted", "error");
          }
        })
      );
      message.appendChild(actions);
    }
    return message;
  }

  function threadElement(thread) {
    const root = thread.root;
    const replies = Array.isArray(thread.replies) ? thread.replies : [];
    const resolved = !!root.resolved_at;
    const collapsed = collapsedThreads.has(root.id);

    const item = document.createElement("li");
    item.className = "trove-comment-thread";
    if (resolved) item.classList.add("trove-comment-thread--resolved");
    item.addEventListener("click", event => {
      if (event.target.closest && event.target.closest("button, a, input, textarea, select, form")) return;
      focusComment(root);
    });

    const header = document.createElement("div");
    header.className = "trove-comment-thread__header";
    const collapse = actionButton(
      collapsed ? "▸" : "▾",
      "trove-comment-thread__collapse",
      () => {
        if (collapsedThreads.has(root.id)) collapsedThreads.delete(root.id);
        else collapsedThreads.add(root.id);
        renderThreads();
      }
    );
    collapse.setAttribute("aria-expanded", String(!collapsed));
    collapse.setAttribute("aria-label", collapsed ? "Expand thread" : "Collapse thread");
    const summary = document.createElement("div");
    summary.className = "trove-comment-thread__summary";
    summary.textContent = normalizeText(root.deleted_at ? "Deleted comment" : root.body, 68);
    const messageCount = document.createElement("span");
    messageCount.className = "trove-comment-thread__count";
    messageCount.textContent = String(1 + replies.length);
    header.append(collapse, summary, messageCount);
    if (resolved) {
      const badge = document.createElement("span");
      badge.className = "trove-comment-thread__resolved";
      badge.textContent = "Resolved";
      header.appendChild(badge);
    }

    const detail = document.createElement("div");
    detail.className = "trove-comment-thread__detail";
    detail.hidden = collapsed;

    const target = actionButton(commentTargetLabel(root), "trove-comment-thread__target", () => focusComment(root));
    detail.append(target, messageElement(root));
    replies.forEach(reply => {
      const replyWrap = document.createElement("div");
      replyWrap.className = "trove-comment-reply";
      replyWrap.appendChild(messageElement(reply));
      detail.appendChild(replyWrap);
    });

    const actions = document.createElement("div");
    actions.className = "trove-comment-thread__actions";
    let replyForm = null;
    if (!resolved && !root.deleted_at) {
      const replyButton = actionButton("Reply", "trove-comment-action", () => {
        replyForm.hidden = !replyForm.hidden;
        if (!replyForm.hidden) replyForm.querySelector("textarea").focus();
      });
      actions.appendChild(replyButton);
    }
    if (!root.deleted_at) {
      actions.appendChild(actionButton(
        resolved ? "Reopen" : "Resolve",
        "trove-comment-action" + (resolved ? "" : " trove-comment-action--resolve"),
        async () => {
          setStatus(resolved ? "Reopening thread…" : "Resolving thread…", "");
          try {
            await requestJSON(mutationURL(root.id, "/resolution"), {
              method: "PATCH",
              credentials: "same-origin",
              headers: {"Content-Type": "application/json"},
              body: JSON.stringify({resolved: !resolved})
            });
            await loadThreads();
            setStatus(resolved ? "Thread reopened." : "Thread resolved.", "");
          } catch (error) {
            setStatus(error.message || "Thread resolution could not be updated", "error");
          }
        }
      ));
      detail.appendChild(actions);
    }

    if (!resolved && !root.deleted_at) {
      replyForm = document.createElement("form");
      replyForm.className = "trove-comment-reply-form";
      replyForm.hidden = true;
      const replyBody = document.createElement("textarea");
      replyBody.maxLength = 5000;
      replyBody.required = true;
      replyBody.placeholder = "Write a reply…";
      replyBody.setAttribute("aria-label", "Reply");
      const replySubmit = document.createElement("button");
      replySubmit.type = "submit";
      replySubmit.className = "trove-comment-submit";
      replySubmit.textContent = "Post reply";
      replyForm.append(replyBody, replySubmit);
      replyForm.addEventListener("submit", async event => {
        event.preventDefault();
        if (!replyBody.value.trim()) return;
        replySubmit.disabled = true;
        setStatus("Posting reply…", "");
        try {
          await requestJSON(mutationURL(root.id, "/replies"), {
            method: "POST",
            credentials: "same-origin",
            headers: {"Content-Type": "application/json"},
            body: JSON.stringify({body: replyBody.value})
          });
          collapsedThreads.delete(root.id);
          await loadThreads();
          setStatus("Reply posted.", "");
        } catch (error) {
          setStatus(error.message || "Reply could not be posted", "error");
        } finally {
          replySubmit.disabled = false;
        }
      });
      detail.appendChild(replyForm);
    }

    item.append(header, detail);
    return item;
  }

  function renderThreads() {
    list.replaceChildren();
    if (!threads.length) {
      const empty = document.createElement("li");
      empty.className = "trove-comment-empty";
      empty.textContent = showResolved && showResolved.checked ?
        "No comment threads yet." :
        "No open comments. Select something or comment on the whole file.";
      list.appendChild(empty);
      return;
    }
    threads.forEach(thread => list.appendChild(threadElement(thread)));
  }

  async function loadThreads() {
    setStatus("Loading comments…", "");
    try {
      const payload = await requestJSON(apiURL(showResolved && showResolved.checked), {
        credentials: "same-origin"
      });
      threads = Array.isArray(payload.threads) ? payload.threads : [];
      currentVersion = payload.current_version || "";
      count.textContent = String(payload.open_thread_count || 0);
      renderThreads();
      setStatus("", "");
    } catch (error) {
      setStatus(error.message || "Comments could not be loaded", "error");
    }
  }

  function documentForAnchor() {
    const frame = document.querySelector("#trove-content iframe");
    if (frame) {
      try {
        if (frame.contentDocument) return frame.contentDocument;
      } catch (_error) {}
    }
    return document;
  }

  function elementForAnchor(anchor, doc) {
    if (anchor.stable_id) {
      try {
        const stable = doc.querySelector('[data-trove-id="' + cssEscape(anchor.stable_id) + '"]');
        if (stable) return stable;
      } catch (_error) {}
    }
    if (anchor.selector) {
      try {
        return doc.querySelector(anchor.selector);
      } catch (_error) {
        return null;
      }
    }
    return null;
  }

  function rangeForQuote(anchor, doc) {
    const exact = anchor.quote && anchor.quote.exact;
    if (!exact) return null;
    const scope = elementForAnchor(anchor, doc) || doc.body;
    const walker = doc.createTreeWalker(scope, NodeFilter.SHOW_TEXT);
    const nodes = [];
    let combined = "";
    while (walker.nextNode()) {
      nodes.push({node: walker.currentNode, start: combined.length});
      combined += walker.currentNode.nodeValue || "";
    }
    const prefix = anchor.quote.prefix || "";
    const suffix = anchor.quote.suffix || "";
    const candidates = [];
    let candidate = combined.indexOf(exact);
    while (candidate >= 0) {
      candidates.push(candidate);
      candidate = combined.indexOf(exact, candidate + 1);
    }
    const start = candidates.find(index => {
      const before = combined.slice(Math.max(0, index - prefix.length), index);
      const after = combined.slice(index + exact.length, index + exact.length + suffix.length);
      return (!prefix || before === prefix) && (!suffix || after === suffix);
    }) ?? candidates[0] ?? -1;
    if (start < 0) return null;
    const end = start + exact.length;
    const reversedNodes = nodes.slice().reverse();
    const startEntry = reversedNodes.find(entry => entry.start <= start);
    const endEntry = reversedNodes.find(entry => entry.start < end);
    if (!startEntry || !endEntry) return null;
    const range = doc.createRange();
    range.setStart(startEntry.node, Math.min(start - startEntry.start, startEntry.node.nodeValue.length));
    range.setEnd(endEntry.node, Math.min(end - endEntry.start, endEntry.node.nodeValue.length));
    return range;
  }

  function focusComment(comment) {
    const anchor = comment.anchor || {type: "file"};
    if (anchor.type === "file") {
      content.scrollTo({top: 0, behavior: "smooth"});
      return;
    }
    const doc = documentForAnchor();
    if (!doc) return;
    if (anchor.type === "text") {
      const range = rangeForQuote(anchor, doc);
      if (!range) {
        setStatus("That text is not present in this version.", "error");
        return;
      }
      currentTarget = {range: range, doc: doc};
      const selection = doc.getSelection();
      selection.removeAllRanges();
      selection.addRange(range);
      range.startContainer.parentElement?.scrollIntoView({behavior: "smooth", block: "center"});
      renderBoxes();
      return;
    }
    const element = elementForAnchor(anchor, doc);
    if (!element) {
      setStatus("That element is not present in this version.", "error");
      return;
    }
    currentTarget = {element: element, doc: doc};
    element.scrollIntoView({behavior: "smooth", block: "center"});
    renderBoxes();
  }

  form.addEventListener("submit", async event => {
    event.preventDefault();
    const bodyText = textarea.value.trim();
    if (!bodyText) {
      setStatus("Write a comment before posting.", "error");
      textarea.focus();
      return;
    }
    submit.disabled = true;
    setStatus("Posting…", "");
    try {
      const response = await window.fetch(
        "/api/artifacts/" + encodeURIComponent(slug) + "/comments",
        {
          method: "POST",
          credentials: "same-origin",
          headers: {"Content-Type": "application/json"},
          body: JSON.stringify({body: bodyText, anchor: currentAnchor})
        }
      );
      const payload = await response.json().catch(() => ({}));
      if (!response.ok) throw new Error(payload.error || "Comment could not be posted");
      textarea.value = "";
      selectFile();
      await loadThreads();
      setStatus("Comment posted.", "");
    } catch (error) {
      setStatus(error.message || "Comment could not be posted", "error");
    } finally {
      submit.disabled = false;
    }
  });

  toggle.addEventListener("click", () => active ? closeComments() : openComments());
  close.addEventListener("click", closeComments);
  targetReset.addEventListener("click", selectFile);
  if (showResolved) showResolved.addEventListener("change", loadThreads);
  window.addEventListener("resize", scheduleBoxes);
  window.addEventListener("scroll", scheduleBoxes, true);
  window.addEventListener("trove:resource-change", () => {
    selectFile();
    loadThreads();
    attachFrames();
  });
  document.addEventListener("keydown", event => {
    if (active && event.key === "Escape") closeComments();
  });

  attachFrames();
  renderTarget();
  loadThreads();
})();
