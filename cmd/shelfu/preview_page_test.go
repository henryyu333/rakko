//go:build darwin

package main

// The preview page: a fake macOS desktop, the rendered Rakko windows placed
// at their real frames, live input forwarding, a 60 fps recording player and
// an annotation layer. No framework, no external files.

const previewHTML = `<!doctype html>
<html lang="zh-CN">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Rakko Preview</title>
<style>
:root {
  color-scheme: dark;
  --bg: #0b0c0e;
  --side: #121316;
  --card: #1a1c20;
  --line: #26282d;
  --ink: #f2f3f5;
  --muted: #8b8f98;
  --accent: #0a84ff;
  --red: #ff453a;
  --green: #30d158;
}
* { box-sizing: border-box; }
html, body { height: 100%; margin: 0; }
body {
  background: var(--bg); color: var(--ink);
  font: 13px/1.45 -apple-system, BlinkMacSystemFont, "SF Pro Text", "Helvetica Neue", sans-serif;
  overflow: hidden;
}
#app { display: flex; height: 100%; }
#side {
  width: 288px; flex: none; height: 100%; overflow-y: auto;
  background: var(--side); border-right: 1px solid var(--line);
  padding: 18px 16px 28px;
}
#side header { display: flex; gap: 10px; align-items: center; margin-bottom: 14px; }
.otter {
  width: 38px; height: 38px; border-radius: 11px; flex: none;
  background: radial-gradient(circle at 38% 30%, #7d5a3c 0 34%, #4a3423 35% 100%);
  box-shadow: inset 0 0 0 1px rgba(255,255,255,.12);
  position: relative;
}
.otter:before, .otter:after {
  content: ""; position: absolute; top: 14px; width: 4px; height: 4px;
  border-radius: 50%; background: #0d0d0f;
}
.otter:before { left: 12px; } .otter:after { right: 12px; }
h1 { font-size: 15px; margin: 0; letter-spacing: .1px; }
header p { margin: 2px 0 0; font-size: 11px; color: var(--muted); }
#conn { display: flex; align-items: center; gap: 7px; font-size: 11.5px;
  color: var(--muted); margin: 0 2px 16px; }
#conn i { width: 7px; height: 7px; border-radius: 50%; background: #ff9f0a; }
#conn.on i { background: var(--green); box-shadow: 0 0 8px rgba(48,209,88,.6); }
#conn.off i { background: var(--red); }
.group { margin-bottom: 16px; }
.group h2 {
  font-size: 10.5px; text-transform: uppercase; letter-spacing: .9px;
  color: var(--muted); margin: 0 2px 7px; font-weight: 700;
}
.btns { display: grid; grid-template-columns: 1fr 1fr; gap: 6px; }
.btns.one { grid-template-columns: 1fr; }
button {
  font: inherit; color: var(--ink); background: var(--card);
  border: 1px solid var(--line); border-radius: 8px; padding: 7px 9px;
  cursor: pointer; text-align: left; white-space: nowrap; overflow: hidden;
  text-overflow: ellipsis;
}
button:hover { background: #22252a; border-color: #34373d; }
button.on { background: #103a68; border-color: #1d6cc4; }
button:active { transform: translateY(.5px); }
button.small { padding: 5px 8px; font-size: 12px; text-align: center; }
.tools { display: flex; gap: 6px; flex-wrap: wrap; }
.tools button { flex: 1; text-align: center; }
.tools button.on { background: #5a2120; border-color: #c03a32; }
.row { display: flex; gap: 6px; align-items: center; }
input[type=text], select {
  font: inherit; color: var(--ink); background: var(--card);
  border: 1px solid var(--line); border-radius: 7px; padding: 6px 8px;
  min-width: 0; width: 100%;
}
input[type=text]::placeholder { color: #5c6068; }
.help { color: var(--muted); font-size: 11px; margin: 6px 2px 0; }
#stage { position: relative; flex: 1; min-width: 0; display: flex;
  flex-direction: column; }
#scroll { flex: 1; min-height: 0; overflow: auto; display: block; padding: 18px; }
#wrap { position: relative; margin: auto; }
#screen, #overlay { display: block; position: absolute; left: 0; top: 0; }
#overlay { pointer-events: none; }
#overlay.draw { pointer-events: auto; cursor: crosshair; }
#playbar {
  flex: none; display: flex; align-items: center; gap: 8px;
  padding: 9px 14px; border-top: 1px solid var(--line);
  background: #101114; font-size: 12px;
}
#playbar[hidden] { display: none; }
#playbar input[type=range] { flex: 1; accent-color: var(--accent); }
#playbar button { padding: 4px 9px; }
#frameLabel { color: var(--muted); font-variant-numeric: tabular-nums;
  min-width: 76px; text-align: center; }
#playbar select { width: auto; padding: 3px 6px; }
#toast {
  position: absolute; left: 50%; bottom: 26px; transform: translateX(-50%);
  background: rgba(20,21,24,.94); border: 1px solid var(--line);
  border-radius: 9px; padding: 8px 14px; font-size: 12px; color: var(--ink);
  opacity: 0; transition: opacity .18s; pointer-events: none; max-width: 70%;
}
#toast.show { opacity: 1; }
#ime { position: fixed; left: -1000px; top: 0; width: 10px; height: 10px;
  opacity: 0; border: 0; padding: 0; }
</style>
</head>
<body>
<div id="app">
  <aside id="side">
    <header>
      <div class="otter"></div>
      <div><h1>Rakko Preview</h1><p>真实界面 · 127.0.0.1:5199</p></div>
    </header>
    <div id="conn"><i></i><span>连接中…</span></div>

    <section class="group">
      <h2>刘海</h2>
      <div class="btns">
        <button data-state="collapsed">收起</button>
        <button data-state="hover">悬停</button>
        <button data-state="search">搜索条</button>
        <button data-state="results">搜索结果</button>
        <button data-state="drag">拖文件进来</button>
      </div>
    </section>

    <section class="group">
      <h2>页面</h2>
      <div class="btns">
        <button data-state="tray">托盘</button>
        <button data-state="clipboard">剪贴板</button>
        <button data-state="tools">工具</button>
        <button data-state="usage">AI 用量</button>
      </div>
    </section>

    <section class="group">
      <h2>实时活动</h2>
      <div class="btns">
        <button data-state="live-charge">正在充电</button>
        <button data-state="live-charged">已充满</button>
        <button data-state="live-low">电量低</button>
        <button data-state="live-headset">耳机连接</button>
        <button data-state="live-headset-off">耳机断开</button>
        <button data-state="live-download">下载完成</button>
      </div>
    </section>

    <section class="group">
      <h2>设置</h2>
      <div class="btns">
        <button data-state="settings-general">通用</button>
        <button data-state="settings-notch">刘海</button>
        <button data-state="settings-tray">托盘</button>
        <button data-state="settings-clipboard">剪贴板</button>
        <button data-state="settings-usage">AI 用量</button>
        <button data-state="settings-shortcuts">快捷键</button>
        <button data-state="settings-permissions">系统权限</button>
        <button data-state="settings-about">关于</button>
      </div>
    </section>

    <section class="group">
      <h2>缩放</h2>
      <div class="btns">
        <button id="fit" class="small on">适配</button>
        <button class="small" data-zoom="1">1×</button>
        <button class="small" data-zoom="2">2×</button>
        <button class="small" data-zoom="4">4×</button>
      </div>
    </section>

    <section class="group">
      <h2>标注</h2>
      <div class="tools">
        <button data-tool="rect">框</button>
        <button data-tool="arrow">箭头</button>
        <button data-tool="pin">图钉</button>
      </div>
      <div class="row" style="margin-top:6px">
        <input id="pinText" type="text" placeholder="图钉文字">
      </div>
      <div class="btns" style="margin-top:6px">
        <button id="undo" class="small">撤销</button>
        <button id="clear" class="small">清除本状态</button>
        <button id="saveShot" class="small">保存截图</button>
        <button id="syncNotes" class="small">写回标注</button>
      </div>
      <p class="help">标注按状态保存；录制时记录帧号。服务写到 .scratch/preview/。</p>
    </section>

    <section class="group">
      <h2>录制 60 fps</h2>
      <div class="btns">
        <button data-record="open">点刘海</button>
        <button data-record="switch">切页</button>
        <button data-record="close">收起</button>
        <button data-record="hover">悬停</button>
      </div>
      <p class="help">录制从当前状态开始，1.4 秒；完成后可在下方播放、0.25×、逐帧和拖进度。</p>
    </section>
  </aside>

  <main id="stage">
    <div id="scroll">
      <div id="wrap">
        <canvas id="screen"></canvas>
        <canvas id="overlay"></canvas>
      </div>
    </div>
    <div id="playbar" hidden>
      <button id="play">▶</button>
      <button id="prev">◀︎</button>
      <button id="next">▶︎</button>
      <input id="scrub" type="range" min="0" max="0" value="0">
      <span id="frameLabel">0 / 0</span>
      <select id="speed"><option value="1">1×</option><option value="0.25">0.25×</option></select>
      <button id="live">返回实时</button>
    </div>
    <div id="toast"></div>
  </main>
</div>
<textarea id="ime" autocomplete="off" autocorrect="off" autocapitalize="off" spellcheck="false"></textarea>
<script>
"use strict";
const W = 1512, H = 982;
const NX = (W - 180) / 2, NY = 0, NW = 180, NH = 32, MENUBAR = 38;

const screenCanvas = document.getElementById("screen");
const overlay = document.getElementById("overlay");
const sctx = screenCanvas.getContext("2d");
const octx = overlay.getContext("2d");
const wrap = document.getElementById("wrap");
const scroll = document.getElementById("scroll");
const conn = document.getElementById("conn");
const toastEl = document.getElementById("toast");
const playbar = document.getElementById("playbar");
const scrub = document.getElementById("scrub");
const frameLabel = document.getElementById("frameLabel");
const ime = document.getElementById("ime");

let scale = 1, fit = true, zoom = 1;
let state = (location.hash || "").replace(/^#/, "") || "collapsed";
let lastFrame = null;
let lastSeq = 0;
let decodeBusy = false;
let decodeQueued = null;
let liveDirty = true;

let tool = null, draft = null;
let annotations = [];
let saveTimer = 0;
let recording = null, playFrame = 0, playing = false, playTimer = 0, speed = 1;
let playbackMode = false;

/* ---- layout ------------------------------------------------------------ */

function resize() {
  const availW = Math.max(320, scroll.clientWidth - 36);
  const availH = Math.max(220, scroll.clientHeight - 36);
  scale = fit ? Math.min(availW / W, availH / H) : zoom;
  const cw = Math.round(W * scale), ch = Math.round(H * scale);
  wrap.style.width = cw + "px";
  wrap.style.height = ch + "px";
  // Cap the backing store: 4× on a Retina display would otherwise be a
  // 12k-wide canvas and hundreds of megabytes per layer.
  const dpr = Math.min(window.devicePixelRatio || 1, 8192 / Math.max(cw, ch, 1));
  for (const c of [screenCanvas, overlay]) {
    c.width = Math.round(cw * dpr);
    c.height = Math.round(ch * dpr);
    c.style.width = cw + "px";
    c.style.height = ch + "px";
  }
  sctx.setTransform(scale * dpr, 0, 0, scale * dpr, 0, 0);
  octx.setTransform(scale * dpr, 0, 0, scale * dpr, 0, 0);
  draw();
}
window.addEventListener("resize", resize);

/* ---- fake desktop ------------------------------------------------------ */

function drawDesktop(ctx) {
  const g = ctx.createLinearGradient(0, 0, W, H);
  g.addColorStop(0, "#20304a");
  g.addColorStop(0.45, "#3c2c4a");
  g.addColorStop(1, "#161b2b");
  ctx.fillStyle = g;
  ctx.fillRect(0, 0, W, H);
  const blob = (x, y, r, c) => {
    const rg = ctx.createRadialGradient(x, y, 0, x, y, r);
    rg.addColorStop(0, c);
    rg.addColorStop(1, "rgba(0,0,0,0)");
    ctx.fillStyle = rg;
    ctx.fillRect(x - r, y - r, r * 2, r * 2);
  };
  blob(300, 360, 520, "rgba(255,138,92,.28)");
  blob(1160, 620, 560, "rgba(92,140,255,.25)");
  blob(820, 180, 420, "rgba(180,120,255,.18)");

  // menu bar
  ctx.fillStyle = "rgba(24,25,28,.74)";
  ctx.fillRect(0, 0, W, MENUBAR);
  ctx.fillStyle = "rgba(255,255,255,.86)";
  ctx.font = "12.5px -apple-system, BlinkMacSystemFont, 'Helvetica Neue', sans-serif";
  ctx.textBaseline = "middle";
  ctx.fillText(" Finder", 14, 19);
  const menus = ["文件", "编辑", "显示", "前往", "窗口", "帮助"];
  let x = 78;
  for (const m of menus) { ctx.fillText(m, x, 19); x += ctx.measureText(m).width + 20; }
  // clock, then Wi-Fi and battery, right to left
  const now = new Date();
  const hh = String(now.getHours()).padStart(2, "0");
  const mm = String(now.getMinutes()).padStart(2, "0");
  ctx.textAlign = "right";
  ctx.fillText(hh + ":" + mm, W - 18, 19);
  ctx.textAlign = "left";
  // wifi and battery
  ctx.strokeStyle = "rgba(255,255,255,.85)";
  ctx.fillStyle = "rgba(255,255,255,.85)";
  ctx.lineWidth = 1.2;
  ctx.beginPath(); ctx.arc(W - 94, 21, 7, Math.PI * 1.25, Math.PI * 1.75); ctx.stroke();
  ctx.beginPath(); ctx.arc(W - 94, 21, 3.5, Math.PI * 1.25, Math.PI * 1.75); ctx.stroke();
  ctx.beginPath(); ctx.arc(W - 94, 22, 1, 0, Math.PI * 2); ctx.fill();
  ctx.strokeRect(W - 80, 15, 18, 9);
  ctx.fillRect(W - 62, 17, 2, 5);
  ctx.fillRect(W - 78, 17, 13, 5);

  // hardware camera housing
  ctx.fillStyle = "#000";
  rounded(ctx, NX, NY - 10, NW, NH + 10, [0, 0, 12, 12]);
  ctx.fill();
  ctx.fillStyle = "#0a0a0c";
  ctx.beginPath(); ctx.arc(NX + NW / 2, 21, 4, 0, Math.PI * 2); ctx.fill();

  // dock
  ctx.fillStyle = "rgba(255,255,255,.13)";
  rounded(ctx, W / 2 - 250, H - 74, 500, 60, 16);
  ctx.fill();
  const icons = ["#f59f3c", "#4c9bf5", "#ef5b6b", "#7ad07a", "#b48cf2", "#f2d14c", "#66c8d8"];
  for (let i = 0; i < icons.length; i++) {
    ctx.fillStyle = icons[i];
    rounded(ctx, W / 2 - 216 + i * 62, H - 63, 46, 38, 10);
    ctx.fill();
  }
}

function rounded(ctx, x, y, w, h, r) {
  const rr = Array.isArray(r) ? r : [r, r, r, r];
  ctx.beginPath();
  ctx.moveTo(x + rr[0], y);
  ctx.lineTo(x + w - rr[1], y);
  ctx.quadraticCurveTo(x + w, y, x + w, y + rr[1]);
  ctx.lineTo(x + w, y + h - rr[2]);
  ctx.quadraticCurveTo(x + w, y + h, x + w - rr[2], y + h);
  ctx.lineTo(x + rr[3], y + h);
  ctx.quadraticCurveTo(x, y + h, x, y + h - rr[3]);
  ctx.lineTo(x, y + rr[0]);
  ctx.quadraticCurveTo(x, y, x + rr[0], y);
  ctx.closePath();
}

/* ---- drawing ----------------------------------------------------------- */

function draw() {
  sctx.clearRect(0, 0, W, H);
  drawDesktop(sctx);
  if (playbackMode && recording) {
    drawRecorded(sctx, playFrame);
  } else if (lastFrame) {
    drawLayers(sctx, lastFrame.layers);
  }
  drawAnnotations(octx, playbackMode ? "playback" : "live");
}

function drawLayers(ctx, layers) {
  for (const l of layers) {
    if (!l.img) continue;
    ctx.save();
    if (l.opaque) {
      ctx.save();
      rounded(ctx, l.x, l.y, l.w, l.h, 10);
      ctx.clip();
      ctx.drawImage(l.img, l.x, l.y, l.w, l.h);
      ctx.restore();
      ctx.strokeStyle = "rgba(255,255,255,.10)";
      ctx.lineWidth = 1;
      rounded(ctx, l.x + .5, l.y + .5, l.w - 1, l.h - 1, 10);
      ctx.stroke();
    } else {
      ctx.shadowColor = "rgba(0,0,0,.55)";
      ctx.shadowBlur = 30;
      ctx.shadowOffsetY = 14;
      ctx.drawImage(l.img, l.x, l.y, l.w, l.h);
    }
    ctx.restore();
  }
}

function drawRecorded(ctx, i) {
  if (!recording || !recording.frames[i]) return;
  const f = recording.frames[i];
  if (!f.img) return;
  ctx.save();
  ctx.shadowColor = "rgba(0,0,0,.55)";
  ctx.shadowBlur = 30;
  ctx.shadowOffsetY = 14;
  ctx.drawImage(f.img, f.x, f.y, f.w, f.h);
  ctx.restore();
}

/* ---- frames over the stream ------------------------------------------- */

function ingest(msg) {
  if (msg.seq && msg.seq <= lastSeq) return;
  lastSeq = Math.max(lastSeq, msg.seq || 0);
  if (decodeBusy) { decodeQueued = msg; return; }
  decode(msg);
}

function decode(msg) {
  decodeBusy = true;
  const layers = msg.layers || [];
  let pending = layers.length;
  if (pending === 0) { finish(); return; }
  const out = layers.map((l) => ({ ...l, img: null }));
  layers.forEach((l, i) => {
    const img = new Image();
    img.onload = () => { out[i].img = img; if (--pending === 0) finish(); };
    img.onerror = () => { if (--pending === 0) finish(); };
    img.src = "data:image/png;base64," + l.png;
  });
  function finish() {
    lastFrame = { seq: msg.seq, state: msg.state, view: msg.view || msg.state, layers: out };
    decodeBusy = false;
    if (!playbackMode) draw();
    if (decodeQueued) { const q = decodeQueued; decodeQueued = null; decode(q); }
  }
}

/* ---- annotations ------------------------------------------------------- */

const NOTE_KEY = "rakko-preview-annotations-v1";

function noteState() {
  if (playbackMode && recording) return recording.frames[playFrame].state;
  return lastFrame && lastFrame.view ? lastFrame.view : state;
}
function noteFrame() { return playbackMode ? playFrame : -1; }

function visibleNotes() {
  return annotations.filter((a) => {
    if (a.state !== noteState()) return false;
    if (a.frame < 0) return !playbackMode;
    return playbackMode && a.frame === playFrame;
  });
}

function drawAnnotations(ctx, mode, clear) {
  if (clear !== false) ctx.clearRect(0, 0, W, H);
  for (const a of visibleNotes()) {
    ctx.save();
    ctx.strokeStyle = a.color || "#ff453a";
    ctx.fillStyle = a.color || "#ff453a";
    ctx.lineWidth = 2;
    ctx.lineJoin = "round";
    ctx.lineCap = "round";
    if (a.tool === "rect") {
      const x = Math.min(a.x, a.x2), y = Math.min(a.y, a.y2);
      const w = Math.abs(a.x2 - a.x), h = Math.abs(a.y2 - a.y);
      ctx.strokeRect(x, y, w, h);
    } else if (a.tool === "arrow") {
      arrow(ctx, a.x, a.y, a.x2, a.y2);
    } else if (a.tool === "pin") {
      pin(ctx, a);
    }
    ctx.restore();
  }
  if (draft && mode === "live") {
    ctx.save();
    ctx.strokeStyle = "#ff453a";
    ctx.fillStyle = "#ff453a";
    ctx.lineWidth = 2;
    if (draft.tool === "rect") {
      const x = Math.min(draft.x, draft.x2), y = Math.min(draft.y, draft.y2);
      ctx.strokeRect(x, y, Math.abs(draft.x2 - draft.x), Math.abs(draft.y2 - draft.y));
    } else if (draft.tool === "arrow") {
      arrow(ctx, draft.x, draft.y, draft.x2, draft.y2);
    }
    ctx.restore();
  }
}

function arrow(ctx, x1, y1, x2, y2) {
  const ang = Math.atan2(y2 - y1, x2 - x1);
  const len = 12;
  ctx.beginPath();
  ctx.moveTo(x1, y1);
  ctx.lineTo(x2, y2);
  ctx.stroke();
  ctx.beginPath();
  ctx.moveTo(x2, y2);
  ctx.lineTo(x2 - len * Math.cos(ang - 0.42), y2 - len * Math.sin(ang - 0.42));
  ctx.lineTo(x2 - len * Math.cos(ang + 0.42), y2 - len * Math.sin(ang + 0.42));
  ctx.closePath();
  ctx.fill();
}

function pin(ctx, a) {
  ctx.beginPath();
  ctx.arc(a.x, a.y, 9, 0, Math.PI * 2);
  ctx.fill();
  ctx.fillStyle = "#fff";
  ctx.beginPath();
  ctx.arc(a.x, a.y, 3.2, 0, Math.PI * 2);
  ctx.fill();
  if (a.text) {
    ctx.font = "600 13px -apple-system, BlinkMacSystemFont, 'Helvetica Neue', sans-serif";
    const pad = 7, tw = ctx.measureText(a.text).width;
    const x = a.x + 14, y = a.y - 11;
    ctx.fillStyle = "rgba(20,21,24,.92)";
    rounded(ctx, x, y, tw + pad * 2, 22, 6);
    ctx.fill();
    ctx.strokeStyle = "rgba(255,69,58,.65)";
    ctx.lineWidth = 1;
    rounded(ctx, x + .5, y + .5, tw + pad * 2 - 1, 21, 6);
    ctx.stroke();
    ctx.fillStyle = "#fff";
    ctx.textBaseline = "middle";
    ctx.fillText(a.text, x + pad, y + 11.5);
  }
}

function addNote(a) {
  a.id = Math.random().toString(36).slice(2);
  a.state = noteState();
  a.frame = noteFrame();
  a.color = "#ff453a";
  annotations.push(a);
  localSave();
  draw();
  schedulePush();
}

function localSave() {
  try { localStorage.setItem(NOTE_KEY, JSON.stringify(annotations)); } catch (e) {}
}

function schedulePush() {
  clearTimeout(saveTimer);
  saveTimer = setTimeout(() => pushNotes(false), 450);
}

async function pushNotes(withShot) {
  const body = { annotations, state: noteState(), frame: noteFrame() };
  if (withShot) body.png = snapshot();
  try {
    const r = await fetch("/api/annotations", {
      method: "POST", headers: { "Content-Type": "application/json" },
      body: JSON.stringify(body),
    });
    if (withShot) {
      const shot = await r.text();
      toast(shot ? "截图已保存：" + shot : "标注已保存");
    }
  } catch (e) { toast("保存失败：" + e); }
}

function snapshot() {
  const c = document.createElement("canvas");
  const s = 2;
  c.width = W * s; c.height = H * s;
  const ctx = c.getContext("2d");
  ctx.scale(s, s);
  drawDesktop(ctx);
  if (playbackMode && recording) drawRecorded(ctx, playFrame);
  else if (lastFrame) drawLayers(ctx, lastFrame.layers);
  const keep = draft;
  draft = null;
  drawAnnotations(ctx, playbackMode ? "playback" : "live", false);
  draft = keep;
  return c.toDataURL("image/png");
}

function toast(msg) {
  toastEl.textContent = msg;
  toastEl.classList.add("show");
  clearTimeout(toast._t);
  toast._t = setTimeout(() => toastEl.classList.remove("show"), 2200);
}

/* ---- input ------------------------------------------------------------- */

function toPt(e) {
  const r = screenCanvas.getBoundingClientRect();
  return { x: (e.clientX - r.left) / scale, y: (e.clientY - r.top) / scale };
}

let lastMoveSent = 0;
let moveQueued = null;
function send(body) {
  fetch("/api/input", {
    method: "POST", headers: { "Content-Type": "application/json" },
    body: JSON.stringify(body),
  }).catch(() => {});
}

screenCanvas.addEventListener("pointermove", (e) => {
  if (tool) return;
  const p = toPt(e);
  moveQueued = p;
  const now = performance.now();
  if (now - lastMoveSent > 14) {
    lastMoveSent = now;
    send({ kind: "move", x: p.x, y: p.y });
    moveQueued = null;
  }
});

screenCanvas.addEventListener("pointerdown", (e) => {
  if (e.button !== 0) return;
  ime.focus({ preventScroll: true });
  if (tool) return;
  const p = toPt(e);
  send({ kind: "down", x: p.x, y: p.y });
  screenCanvas.setPointerCapture(e.pointerId);
});

screenCanvas.addEventListener("pointerup", (e) => {
  if (tool) return;
  const p = toPt(e);
  send({ kind: "up", x: p.x, y: p.y });
  try { screenCanvas.releasePointerCapture(e.pointerId); } catch (err) {}
});

screenCanvas.addEventListener("contextmenu", (e) => e.preventDefault());

screenCanvas.addEventListener("wheel", (e) => {
  if (tool) return;
  e.preventDefault();
  send({ kind: "wheel", x: 0, y: 0, dx: e.deltaX, dy: e.deltaY });
}, { passive: false });

/* annotation pointer */
overlay.addEventListener("pointerdown", (e) => {
  if (!tool) return;
  const p = toPt(e);
  if (tool === "pin") {
    addNote({ tool: "pin", x: p.x, y: p.y, x2: p.x, y2: p.y, text: document.getElementById("pinText").value.trim() });
    return;
  }
  draft = { tool, x: p.x, y: p.y, x2: p.x, y2: p.y };
  overlay.setPointerCapture(e.pointerId);
  draw();
});
overlay.addEventListener("pointermove", (e) => {
  if (!draft) return;
  const p = toPt(e);
  draft.x2 = p.x; draft.y2 = p.y;
  draw();
});
overlay.addEventListener("pointerup", (e) => {
  if (!draft) return;
  const p = toPt(e);
  draft.x2 = p.x; draft.y2 = p.y;
  if (Math.abs(draft.x2 - draft.x) > 3 || Math.abs(draft.y2 - draft.y) > 3) {
    addNote({ tool: draft.tool, x: draft.x, y: draft.y, x2: draft.x2, y2: draft.y2, text: "" });
  }
  draft = null;
  draw();
});

/* keyboard */
let composing = false;
document.addEventListener("compositionstart", () => { composing = true; });
document.addEventListener("compositionend", (e) => {
  composing = false;
  if (e.data) send({ kind: "type", text: e.data });
});

const named = new Set(["Enter", "Escape", "Backspace", "Tab", "Delete",
  "ArrowUp", "ArrowDown", "ArrowLeft", "ArrowRight", "Home", "End",
  "PageUp", "PageDown"]);

document.addEventListener("keydown", (e) => {
  const t = e.target;
  // The hidden IME field is our keyboard sink; other form controls keep
  // their own keys so the annotation text and zoom box stay usable.
  if (t && t !== ime && (t.tagName === "INPUT" || t.tagName === "TEXTAREA" || t.tagName === "SELECT")) return;
  if (composing || e.isComposing) return;
  const mods = (e.shiftKey ? 1 : 0) | (e.ctrlKey ? 2 : 0) | (e.altKey ? 4 : 0) | (e.metaKey ? 8 : 0);
  if (named.has(e.key)) {
    e.preventDefault();
    send({ kind: "key", key: e.key, mods });
    return;
  }
  if (e.metaKey || e.ctrlKey || e.altKey) {
    if (e.code === "KeyQ") { e.preventDefault(); return; } // never quit the test host
    if (/^(Key[A-Z]|Digit[0-9]|Minus|Equal|Comma|Period|Slash|Semicolon|Quote|BracketLeft|BracketRight|Backslash|Backquote|Space)$/.test(e.code)) {
      e.preventDefault();
      send({ kind: "key", key: e.code, mods });
    }
    return;
  }
  if (e.key === " ") {
    e.preventDefault();
    send({ kind: "type", text: " " });
    return;
  }
  if (e.key.length === 1) {
    e.preventDefault();
    send({ kind: "type", text: e.key });
  }
});

/* ---- stream ------------------------------------------------------------ */

let es = null;
function connect() {
  es = new EventSource("/events");
  es.onopen = () => {
    conn.className = "conn on";
    conn.querySelector("span").textContent = "已连接";
    // A rebuilt server restarts its sequence numbers; accept its frames.
    lastSeq = 0;
    // A restarted server re-applies the page's state.
    fetch("/api/state", {
      method: "POST", headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ name: state }),
    }).catch(() => {});
  };
  es.onerror = () => {
    conn.className = "conn off";
    conn.querySelector("span").textContent = "连接断开，正在重连…";
  };
  es.addEventListener("frame", (e) => {
    let msg;
    try { msg = JSON.parse(e.data); } catch (err) { return; }
    ingest(msg);
  });
  es.addEventListener("state", (e) => {
    try {
      const msg = JSON.parse(e.data);
      if (msg.name && msg.name !== state) { state = msg.name; markState(); }
    } catch (err) {}
  });
  es.addEventListener("record", (e) => {
    let msg;
    try { msg = JSON.parse(e.data); } catch (err) { return; }
    loadRecording(msg);
  });
}
connect();

/* ---- state buttons ----------------------------------------------------- */

function markState() {
  for (const b of document.querySelectorAll("[data-state]")) {
    b.classList.toggle("on", b.dataset.state === state);
  }
  location.hash = state;
}
for (const b of document.querySelectorAll("[data-state]")) {
  b.addEventListener("click", () => {
    state = b.dataset.state;
    markState();
    fetch("/api/state", {
      method: "POST", headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ name: state }),
    }).catch(() => {});
  });
}

/* ---- zoom -------------------------------------------------------------- */

for (const b of document.querySelectorAll("[data-zoom]")) {
  b.addEventListener("click", () => {
    fit = false;
    zoom = parseFloat(b.dataset.zoom);
    for (const o of document.querySelectorAll("[data-zoom], #fit")) o.classList.remove("on");
    b.classList.add("on");
    resize();
  });
}
document.getElementById("fit").addEventListener("click", () => {
  fit = true;
  document.getElementById("fit").classList.add("on");
  for (const o of document.querySelectorAll("[data-zoom]")) o.classList.remove("on");
  resize();
});

/* ---- annotation tools -------------------------------------------------- */

for (const b of document.querySelectorAll("[data-tool]")) {
  b.addEventListener("click", () => {
    tool = tool === b.dataset.tool ? null : b.dataset.tool;
    for (const o of document.querySelectorAll("[data-tool]")) o.classList.toggle("on", o.dataset.tool === tool);
    overlay.classList.toggle("draw", !!tool);
    draw();
  });
}
document.getElementById("undo").addEventListener("click", () => {
  for (let i = annotations.length - 1; i >= 0; i--) {
    if (annotations[i].state === noteState()) { annotations.splice(i, 1); break; }
  }
  localSave(); schedulePush(); draw();
});
document.getElementById("clear").addEventListener("click", () => {
  if (!confirm("清除「" + noteState() + "」的标注？")) return;
  annotations = annotations.filter((a) => a.state !== noteState());
  localSave(); schedulePush(); draw();
});
document.getElementById("saveShot").addEventListener("click", () => pushNotes(true));
document.getElementById("syncNotes").addEventListener("click", () => pushNotes(false));

/* ---- recording --------------------------------------------------------- */

for (const b of document.querySelectorAll("[data-record]")) {
  b.addEventListener("click", () => {
    toast("录制中…");
    fetch("/api/record", {
      method: "POST", headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ action: b.dataset.record }),
    }).catch(() => {});
  });
}

function loadRecording(msg) {
  const frames = msg.frames || [];
  if (!frames.length) { toast("录制没有帧"); return; }
  let pending = frames.length;
  const out = frames.map((f) => ({ ...f, img: null }));
  frames.forEach((f, i) => {
    const img = new Image();
    img.onload = () => { out[i].img = img; if (--pending === 0) ready(); };
    img.onerror = () => { if (--pending === 0) ready(); };
    img.src = f.url;
  });
  function ready() {
    recording = { id: msg.id, fps: msg.fps || 60, frames: out };
    playbackMode = true;
    playFrame = 0;
    playing = false;
    playbar.hidden = false;
    resize();
    scrub.max = String(out.length - 1);
    scrub.value = "0";
    updatePlayLabel();
    draw();
    play();
  }
}

function updatePlayLabel() {
  if (!recording) return;
  frameLabel.textContent = (playFrame + 1) + " / " + recording.frames.length + " · " + recording.frames[playFrame].state;
}

function drawPlayback() {
  if (!recording) return;
  draw();
  updatePlayLabel();
  scrub.value = String(playFrame);
}

function play() {
  if (!recording) return;
  if (playing) { playing = false; clearTimeout(playTimer); document.getElementById("play").textContent = "▶"; return; }
  if (playFrame >= recording.frames.length - 1) playFrame = 0;
  playing = true;
  document.getElementById("play").textContent = "❚❚";
  const interval = 1000 / recording.fps / speed;
  const step = () => {
    if (!playing) return;
    playFrame++;
    if (playFrame >= recording.frames.length) {
      playFrame = recording.frames.length - 1;
      playing = false;
      document.getElementById("play").textContent = "▶";
      drawPlayback();
      return;
    }
    drawPlayback();
    playTimer = setTimeout(step, interval);
  };
  playTimer = setTimeout(step, interval);
}

document.getElementById("play").addEventListener("click", play);
document.getElementById("prev").addEventListener("click", () => {
  if (!recording) return;
  playing = false; clearTimeout(playTimer);
  document.getElementById("play").textContent = "▶";
  playFrame = Math.max(0, playFrame - 1);
  drawPlayback();
});
document.getElementById("next").addEventListener("click", () => {
  if (!recording) return;
  playing = false; clearTimeout(playTimer);
  document.getElementById("play").textContent = "▶";
  playFrame = Math.min(recording.frames.length - 1, playFrame + 1);
  drawPlayback();
});
scrub.addEventListener("input", () => {
  if (!recording) return;
  playing = false; clearTimeout(playTimer);
  document.getElementById("play").textContent = "▶";
  playFrame = parseInt(scrub.value, 10) || 0;
  drawPlayback();
});
document.getElementById("speed").addEventListener("change", (e) => {
  speed = parseFloat(e.target.value) || 1;
  if (playing) { playing = false; clearTimeout(playTimer); play(); }
});
document.getElementById("live").addEventListener("click", () => {
  playbackMode = false;
  playing = false;
  clearTimeout(playTimer);
  document.getElementById("play").textContent = "▶";
  playbar.hidden = true;
  resize();
});

/* ---- boot -------------------------------------------------------------- */

async function loadNotes() {
  try {
    const local = JSON.parse(localStorage.getItem(NOTE_KEY) || "null");
    if (Array.isArray(local)) annotations = local;
  } catch (e) {}
  try {
    const r = await fetch("/api/annotations");
    const j = await r.json();
    if (j && Array.isArray(j.annotations) && j.annotations.length) annotations = j.annotations;
  } catch (e) {}
  localSave();
  draw();
}

markState();
resize();
loadNotes();
</script>
</body>
</html>
`
