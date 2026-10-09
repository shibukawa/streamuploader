// Drive web UI: a client of /api/drive and of streamuploader's upload API.
import { Viewer } from "@bdfkit/viewer";

const $ = (id) => document.getElementById(id);
const esc = (s) => String(s ?? "").replace(/[&<>"']/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c]));

const api = {
  async call(method, url, body, headers = {}) {
    const init = { method, headers: { ...headers } };
    if (body !== undefined) {
      init.headers["Content-Type"] = "application/json";
      init.body = JSON.stringify(body);
    }
    const res = await fetch(url, init);
    if (res.status === 204) return null;
    const text = await res.text();
    let data = null;
    try { data = text ? JSON.parse(text) : null; } catch { data = { message: text }; }
    if (!res.ok) {
      const err = new Error(data?.detail || data?.message || res.statusText);
      err.status = res.status; err.code = data?.code; err.data = data;
      throw err;
    }
    return data;
  },
  search(params) { return this.call("GET", "/api/drive/search?" + new URLSearchParams(params)); },
  facets(params) { return this.call("GET", "/api/drive/facets?" + new URLSearchParams(params)); },
  file(id) { return this.call("GET", `/api/drive/files/${id}`); },
  info() { return this.call("GET", "/api/drive/info"); },
  patch(id, body) { return this.call("PATCH", `/api/drive/files/${id}`, body); },
  remove(id) { return this.call("DELETE", `/api/drive/files/${id}`); },
  versions(id) { return this.call("GET", `/api/drive/files/${id}/versions`); },
  newVersion(id, upload, revision) { return this.call("POST", `/api/drive/files/${id}/versions`, { upload, expected_revision: revision }); },
  createUploadKey(file) {
    return this.call("POST", "/api/upload/keys", { file_name: file.name, content_type: file.type || "application/octet-stream", size_bytes: file.size });
  },
  wait(keys) { return this.call("POST", "/api/upload/wait", { upload_keys: keys, timeout_seconds: 60 }); },
  register(upload, tags) { return this.call("POST", "/api/drive/files", { upload, tags }); },
};

const state = {
  view: { kind: "all" },
  sort: "name",
  hits: [],
  selectedId: null,
  expanded: new Set(),
  // worm is "off", "append_only" or "strict"; the server refuses what the
  // UI hides, this only keeps the panel honest.
  worm: "off",
};
const WORM_LABEL = { append_only: "WORM: 追記のみ", strict: "WORM: 厳格" };
const WORM_HINT = {
  append_only: "監査モード（追記のみ）: タグの追加と、未設定の著者・位置の記入はできますが、名前の変更、タグの削除、ファイルの削除はできません。",
  strict: "監査モード（厳格）: 登録後のメタデータ変更と削除はできません。内容の更新は「新しい版」としてアップロードし、以前の版も残ります。",
};

async function loadInfo() {
  try {
    const info = await api.info();
    state.worm = info?.worm?.mode || "off";
  } catch { state.worm = "off"; }
  const badge = $("wormBadge");
  if (!badge) return;
  if (state.worm === "off") { badge.hidden = true; return; }
  badge.hidden = false;
  badge.textContent = WORM_LABEL[state.worm] || "WORM";
  badge.title = WORM_HINT[state.worm] || "";
}

const ROOTS = [
  { id: "tagTree", path: "/tags", exact: true },
  { id: "typeTree", path: "/type", exact: false },
  { id: "dateTree", path: "/date", exact: false },
  { id: "authorTree", path: "/author", exact: false },
];

function viewFromHash() {
  const h = decodeURIComponent(location.hash.slice(1));
  if (!h) return { kind: "all" };
  if (h.startsWith("q=")) return { kind: "search", q: h.slice(2) };
  if (h.startsWith("/")) return { kind: "folder", path: h };
  return { kind: "all" };
}

function setView(view, push = true) {
  state.view = view;
  if (view.kind !== "search") $("searchInput").value = "";
  if (view.kind === "search" && state.sort === "name") state.sort = "score";
  if (view.kind !== "search" && state.sort === "score") state.sort = "name";
  $("sortSelect").value = state.sort;
  const hash = view.kind === "search" ? "q=" + view.q : view.kind === "folder" ? view.path : "";
  if (push) history.pushState(null, "", hash ? "#" + encodeURIComponent(hash) : location.pathname);
  refresh();
  renderTrees();
}

function currentTag() {
  const v = state.view;
  if (v.kind === "folder" && v.path.startsWith("/tags/")) return v.path.slice("/tags".length);
  return null;
}

function searchParams() {
  const v = state.view;
  const p = { sort: state.sort, limit: 200 };
  if (v.kind === "search") p.q = v.q;
  if (v.kind === "folder") {
    p.facet = v.path;
    p.exact = v.path.startsWith("/tags/") ? "1" : "0";
  }
  return p;
}

async function refresh() {
  try {
    const res = await api.search(searchParams());
    state.hits = res.hits;
    renderBreadcrumb();
    renderGrid(res);
    await renderSubfolders();
  } catch (e) {
    console.error(e);
    $("resultCount").textContent = "読み込みに失敗しました: " + e.message;
  }
}

function fmtSize(n) {
  if (!n) return "";
  const u = ["B", "KB", "MB", "GB", "TB"];
  let i = 0; let v = n;
  while (v >= 1024 && i < u.length - 1) { v /= 1024; i++; }
  return (i === 0 ? v : v.toFixed(1)) + " " + u[i];
}
function fmtDate(s) {
  if (!s) return "";
  const d = new Date(s);
  return isNaN(d) ? "" : d.toLocaleDateString("ja-JP", { year: "numeric", month: "2-digit", day: "2-digit" });
}
function extOf(f) {
  const m = /\.([^.]+)$/.exec(f.name || "");
  return (m ? m[1] : (f.content_type || "").split("/").pop() || "file").toUpperCase().slice(0, 5);
}
function isImage(f) { return (f.content_type || "").startsWith("image/"); }

function renderBreadcrumb() {
  const el = $("breadcrumb");
  const v = state.view;
  if (v.kind === "all") { el.innerHTML = "<span>すべてのファイル</span>"; return; }
  if (v.kind === "search") { el.innerHTML = `<span>検索: ${esc(v.q)}</span>`; return; }
  const parts = v.path.split("/").filter(Boolean);
  const labels = { tags: "タグ", type: "種類", date: "日付", author: "著者", geo: "場所" };
  let acc = "";
  const html = parts.map((seg, i) => {
    acc += "/" + seg;
    const label = i === 0 ? (labels[seg] || seg) : seg;
    const last = i === parts.length - 1;
    return last ? `<span>${esc(label)}</span>` : `<a href="#${encodeURIComponent(acc)}" data-path="${esc(acc)}">${esc(label)}</a>`;
  }).join('<span class="sep">›</span>');
  el.innerHTML = html;
  el.querySelectorAll("a[data-path]").forEach((a) => a.addEventListener("click", (ev) => {
    ev.preventDefault();
    setView({ kind: "folder", path: a.dataset.path });
  }));
}

async function renderSubfolders() {
  const el = $("subfolders");
  const v = state.view;
  if (v.kind !== "folder") { el.innerHTML = ""; return; }
  try {
    const res = await api.facets({ path: v.path });
    el.innerHTML = res.children.map((c) =>
      `<button class="folder" data-path="${esc(c.path)}">📁 <span>${esc(c.name)}</span><span class="badge">${c.count}</span></button>`).join("");
    el.querySelectorAll("button[data-path]").forEach((b) => b.addEventListener("click", () => setView({ kind: "folder", path: b.dataset.path })));
  } catch (e) {
    el.innerHTML = "";
  }
}

function renderGrid(res) {
  const grid = $("grid");
  $("resultCount").textContent = `${res.total} 件` + (res.overlay ? `（反映待ち ${res.overlay}）` : "");
  $("empty").hidden = res.hits.length > 0;
  grid.innerHTML = res.hits.map((h) => {
    const f = h.file;
    const thumb = f.urls.thumbnail
      ? `<img src="${esc(f.urls.thumbnail)}" alt="" loading="lazy" onerror="this.replaceWith(Object.assign(document.createElement('span'),{className:'icon',textContent:'${esc(extOf(f))}'}))">`
      : `<span class="icon">${esc(extOf(f))}</span>`;
    const tags = (f.tags || []).map((t) => `<span class="chip">${esc(t.slice(1))}</span>`).join("");
    const snippet = h.snippet ? `<div class="snippet">${h.snippet}</div>` : "";
    const page = h.page ? `<span class="page">p.${h.page}</span>` : "";
    return `<article class="card${h.fresh ? " fresh" : ""}${state.selectedId === f.file_id ? " selected" : ""}" data-id="${esc(f.file_id)}" data-page="${h.page || 0}">
      <div class="thumb">${thumb}</div>
      <div class="meta">
        <div class="name" title="${esc(f.name)}">${esc(f.name)}</div>
        <div class="sub"><span>${fmtSize(f.size_bytes)}</span>${page}<span>${fmtDate(f.dates?.modified)}</span></div>
        ${snippet}
        <div class="tags">${tags}</div>
      </div>
      <button class="info" title="詳細" data-info="${esc(f.file_id)}">i</button>
    </article>`;
  }).join("");
  grid.querySelectorAll(".card").forEach((card) => {
    card.addEventListener("click", (ev) => {
      if (ev.target.closest("[data-info]")) { selectFile(card.dataset.id); return; }
      openViewer(card.dataset.id, Number(card.dataset.page) || 0);
    });
  });
}

// --- sidebar trees -------------------------------------------------------
async function renderTrees() {
  const roots = $("rootViews");
  roots.innerHTML = `<li><div class="node${state.view.kind === "all" ? " active" : ""}" data-all><span class="twisty"></span><span class="label">すべてのファイル</span></div></li>`;
  roots.querySelector("[data-all]").addEventListener("click", () => setView({ kind: "all" }));
  for (const root of ROOTS) {
    const ul = $(root.id);
    ul.innerHTML = "";
    await renderChildren(ul, root.path);
  }
}

async function renderChildren(ul, path) {
  let res;
  try { res = await api.facets({ path }); } catch { return; }
  for (const c of res.children) {
    const li = document.createElement("li");
    const active = state.view.kind === "folder" && state.view.path === c.path;
    const open = state.expanded.has(c.path);
    li.innerHTML = `<div class="node${active ? " active" : ""}" data-path="${esc(c.path)}"><span class="twisty">${open ? "▾" : "▸"}</span><span class="label">${esc(c.name)}</span><span class="badge">${c.count}</span></div><ul${open ? "" : " hidden"}></ul>`;
    const node = li.querySelector(".node");
    node.addEventListener("click", (ev) => {
      if (ev.target.classList.contains("twisty")) {
        if (state.expanded.has(c.path)) state.expanded.delete(c.path); else state.expanded.add(c.path);
        renderTrees();
        return;
      }
      state.expanded.add(c.path);
      setView({ kind: "folder", path: c.path });
    });
    ul.appendChild(li);
    if (open) await renderChildren(li.querySelector("ul"), c.path);
  }
}

// --- details panel ---------------------------------------------------------
async function selectFile(id) {
  state.selectedId = id;
  document.querySelectorAll(".card").forEach((c) => c.classList.toggle("selected", c.dataset.id === id));
  const panel = $("details");
  panel.hidden = false;
  panel.innerHTML = "<p class='msg'>読み込み中…</p>";
  let f;
  try { f = await api.file(id); } catch (e) { panel.innerHTML = `<p class='msg'>${esc(e.message)}</p>`; return; }
  const tags = (f.tags || []).slice();
  const original = new Set(tags);
  const worm = state.worm;
  const strict = worm === "strict";
  const appendOnly = worm === "append_only";
  const canEditName = !strict && !appendOnly;
  const canEditAuthor = !strict && !(appendOnly && f.author?.override);
  const versions = (f.versions || []).length;
  const versionRows = (f.versions || []).map((v, i) =>
    `<li><a href="/api/drive/files/${esc(f.file_id)}/versions/${i + 1}/download">版 ${i + 1}</a> <span class="msg">${fmtDate(v.at)} ${fmtSize(v.size_bytes)}</span></li>`).join("");
  panel.innerHTML = `
    ${f.urls.thumbnail ? `<img class="thumb-large" src="${esc(f.urls.thumbnail)}" alt="" onerror="this.remove()">` : ""}
    <h3>${esc(f.name)}</h3>
    ${worm !== "off" ? `<p class="worm-hint">${esc(WORM_HINT[worm] || "")}</p>` : ""}
    <dl>
      <dt>種類</dt><dd>${esc(f.content_type || "")}</dd>
      <dt>サイズ</dt><dd>${fmtSize(f.size_bytes)}</dd>
      <dt>追加</dt><dd>${fmtDate(f.dates?.uploaded)}</dd>
      <dt>更新</dt><dd>${fmtDate(f.dates?.modified)}</dd>
      <dt>著者</dt><dd>${esc(f.author?.extracted || "") || "<span class='msg'>（未抽出）</span>"}</dd>
      <dt>改訂</dt><dd>${f.revision}${versions ? `（版 ${versions + 1}）` : ""}</dd>
    </dl>
    <label>名前</label><input type="text" id="dName" value="${esc(f.name)}"${canEditName ? "" : " disabled"}>
    <label>タグ${strict ? "" : "（Enter で追加。「/」で階層）"}</label>
    <div class="tagedit" id="dTags"></div>
    <label>著者（上書き）</label><input type="text" id="dAuthor" value="${esc(f.author?.override || "")}" placeholder="${esc(f.author?.extracted || "")}"${canEditAuthor ? "" : " disabled"}>
    ${versions ? `<label>以前の版</label><ul class="versions">${versionRows}</ul>` : ""}
    <div class="row">
      ${strict ? "" : `<button class="btn primary" id="dSave">保存</button>`}
      <a class="btn" href="${esc(f.urls.download)}">ダウンロード</a>
      <label class="btn" for="dVersionInput">新しい版</label>
      <input id="dVersionInput" type="file" hidden>
      ${worm === "off" ? `<button class="btn danger" id="dDelete">削除</button>` : ""}
    </div>
    <p class="msg" id="dMsg"></p>`;
  const tagBox = $("dTags");
  const renderTags = () => {
    const removable = (t) => worm === "off" || (appendOnly && !original.has(t));
    tagBox.innerHTML = tags.map((t, i) => `<span class="chip">${esc(t.slice(1))}${removable(t) ? `<button type="button" data-i="${i}" aria-label="削除">×</button>` : ""}</span>`).join("")
      + (strict ? "" : `<input id="dTagInput" placeholder="タグを追加">`);
    tagBox.querySelectorAll("button[data-i]").forEach((b) => b.addEventListener("click", () => { tags.splice(Number(b.dataset.i), 1); renderTags(); }));
    if (strict) return;
    $("dTagInput").addEventListener("keydown", (ev) => {
      if (ev.key === "Enter") {
        ev.preventDefault();
        const t = ev.target.value.trim();
        if (t) { const norm = "/" + t.split("/").map((s) => s.trim()).filter(Boolean).join("/"); if (!tags.includes(norm)) tags.push(norm); }
        renderTags();
        $("dTagInput").focus();
      }
    });
  };
  renderTags();
  $("dSave")?.addEventListener("click", async () => {
    const msg = $("dMsg");
    msg.textContent = "保存中…";
    try {
      const body = { tags, expected_revision: f.revision };
      if (canEditName) body.name = $("dName").value;
      if (canEditAuthor) body.author = $("dAuthor").value;
      await api.patch(id, body);
      msg.textContent = "保存しました";
      await refresh();
      await renderTrees();
      selectFile(id);
    } catch (e) { msg.textContent = (e.code === "worm_readonly" ? "監査モードのため変更できません: " : "保存できませんでした: ") + e.message; }
  });
  $("dVersionInput").addEventListener("change", async (ev) => {
    const file = ev.target.files?.[0];
    ev.target.value = "";
    if (!file) return;
    const msg = $("dMsg");
    msg.textContent = "新しい版をアップロード中…";
    try {
      const item = await uploadOne(file, () => {});
      await api.newVersion(id, item, f.revision);
      msg.textContent = "新しい版を登録しました";
      await refresh();
      selectFile(id);
    } catch (e) { msg.textContent = "新しい版を登録できませんでした: " + e.message; }
  });
  $("dDelete")?.addEventListener("click", async () => {
    if (!confirm(`「${f.name}」を削除しますか？`)) return;
    try {
      await api.remove(id);
      panel.hidden = true;
      state.selectedId = null;
      await refresh();
      await renderTrees();
    } catch (e) { $("dMsg").textContent = "削除できませんでした: " + e.message; }
  });
}

// --- viewer ------------------------------------------------------------------
let viewer = null;
let viewerPageListener = null;

async function openViewer(id, page) {
  let f;
  try { f = await api.file(id); } catch (e) { alert(e.message); return; }
  const el = $("viewer");
  const body = $("viewerBody");
  el.hidden = false;
  $("viewerTitle").textContent = f.name;
  $("viewerPage").textContent = "";
  $("viewerDownload").href = f.urls.download;
  body.innerHTML = "";
  destroyViewer();
  if (f.urls.preview) {
    const host = document.createElement("div");
    body.appendChild(host);
    try {
      viewer = new Viewer(host, { mode: "embedded", layout: "continuous" });
      const manifest = await viewer.open({ kind: "single", url: f.urls.preview });
      const pages = manifest?.views?.[0]?.pages?.length;
      if (page > 0) {
        viewer.scrollToPage(page - 1);
        $("viewerPage").textContent = pages ? `${page} / ${pages} ページ（ヒット位置）` : `${page} ページ目`;
      } else if (pages) {
        $("viewerPage").textContent = `${pages} ページ`;
      }
    } catch (e) {
      console.error(e);
      body.innerHTML = `<p class="message">プレビューをまだ表示できません（生成中の可能性があります）。<br>${esc(e.message)}<br><a href="${esc(f.urls.download)}">ダウンロード</a></p>`;
    }
  } else if (isImage(f)) {
    const img = document.createElement("img");
    img.src = f.urls.content;
    img.alt = f.name;
    body.appendChild(img);
  } else {
    body.innerHTML = `<p class="message">この形式のプレビューはありません。<br><a href="${esc(f.urls.download)}">ダウンロード</a></p>`;
  }
}

function destroyViewer() {
  if (viewer) { try { viewer.destroy(); } catch {} viewer = null; }
}

function closeViewer() {
  $("viewer").hidden = true;
  destroyViewer();
  $("viewerBody").innerHTML = "";
}

// --- upload ----------------------------------------------------------------
function putWithProgress(url, file, headers, onProgress) {
  return new Promise((resolve, reject) => {
    const xhr = new XMLHttpRequest();
    xhr.open("PUT", url);
    for (const [k, v] of Object.entries(headers)) xhr.setRequestHeader(k, v);
    xhr.upload.onprogress = (ev) => { if (ev.lengthComputable) onProgress(ev.loaded / ev.total); };
    xhr.onload = () => {
      let data = null;
      try { data = xhr.responseText ? JSON.parse(xhr.responseText) : null; } catch { data = { message: xhr.responseText }; }
      if (xhr.status >= 200 && xhr.status < 300) resolve(data);
      else { const err = new Error(data?.detail || data?.message || xhr.statusText); err.status = xhr.status; err.code = data?.code; reject(err); }
    };
    xhr.onerror = () => reject(new Error("network error"));
    xhr.send(file);
  });
}

// uploadOne runs the streamuploader flow for one file and returns the
// uploaded item, ready to register or to attach as a new version.
async function uploadOne(file, onProgress) {
  const key = await api.createUploadKey(file);
  const url = `/api/upload/keys/${encodeURIComponent(key.upload_key)}/content`;
  const headers = { "Content-Type": file.type || "application/octet-stream" };
  onProgress(0, "送信中");
  try {
    await putWithProgress(url, file, headers, (p) => onProgress(p));
  } catch (e) {
    if (e.code === "document_password_required") {
      const pw = prompt(`「${file.name}」はパスワード付きです。パスワードを入力してください。`);
      if (pw === null) throw new Error("キャンセルしました");
      await putWithProgress(url, file, { ...headers, "X-Document-Password": pw }, (p) => onProgress(p));
    } else throw e;
  }
  onProgress(1, "処理中");
  for (let i = 0; i < 20; i++) {
    const res = await api.wait([key.upload_key]);
    const it = res.items?.[0];
    if (it && it.status === "uploaded") return it;
    if (it && (it.status === "failed" || it.status === "canceled" || it.status === "expired")) throw new Error(it.error || it.status);
  }
  throw new Error("timeout");
}

async function uploadFiles(files) {
  const tag = currentTag();
  const tags = tag ? [tag] : [];
  for (const file of files) {
    const row = document.createElement("div");
    row.className = "upload-row";
    row.innerHTML = `<span>${esc(file.name)}</span><progress max="1" value="0"></progress><span class="status">準備中</span>`;
    $("uploads").appendChild(row);
    const progress = row.querySelector("progress");
    const status = row.querySelector(".status");
    try {
      const item = await uploadOne(file, (p, phase) => { progress.value = p; if (phase) status.textContent = phase; });
      await api.register(item, tags);
      status.textContent = "完了";
      progress.value = 1;
      setTimeout(() => row.remove(), 4000);
    } catch (e) {
      row.classList.add("failed");
      status.textContent = e.message;
    }
  }
  await refresh();
  await renderTrees();
}

// --- wiring ----------------------------------------------------------------
$("searchForm").addEventListener("submit", (ev) => {
  ev.preventDefault();
  const q = $("searchInput").value.trim();
  setView(q ? { kind: "search", q } : { kind: "all" });
});
$("brand").addEventListener("click", (ev) => { ev.preventDefault(); $("searchInput").value = ""; setView({ kind: "all" }); });
$("sortSelect").addEventListener("change", (ev) => { state.sort = ev.target.value; refresh(); });
$("fileInput").addEventListener("change", (ev) => { uploadFiles([...ev.target.files]); ev.target.value = ""; });
$("viewerClose").addEventListener("click", closeViewer);
document.addEventListener("keydown", (ev) => { if (ev.key === "Escape" && !$("viewer").hidden) closeViewer(); });
window.addEventListener("popstate", () => setView(viewFromHash(), false));

let dragDepth = 0;
document.addEventListener("dragenter", (ev) => { ev.preventDefault(); dragDepth++; $("dropzone").hidden = false; });
document.addEventListener("dragover", (ev) => ev.preventDefault());
document.addEventListener("dragleave", () => { if (--dragDepth <= 0) { dragDepth = 0; $("dropzone").hidden = true; } });
document.addEventListener("drop", (ev) => {
  ev.preventDefault(); dragDepth = 0; $("dropzone").hidden = true;
  if (ev.dataTransfer?.files?.length) uploadFiles([...ev.dataTransfer.files]);
});

const initial = viewFromHash();
if (initial.kind === "search") $("searchInput").value = initial.q;
loadInfo();
setView(initial, false);
