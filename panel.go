// panel.html — a single self-contained page for the CPA management UI. It
// fetches the accounts JSON route and renders nickname / uid / domain / credits
// / status per credential, with a manual refresh. Served by the plugin at
// /v0/resource/plugins/workbuddy-bridge/panel (sidebar menu "WorkBuddy Bridge").
package main

const panelHTML = `<!doctype html>
<html lang="zh-CN">
<head>
<meta charset="utf-8">
<title>WorkBuddy Bridge — 账号与积分</title>
<style>
  body { font-family: -apple-system, "Segoe UI", "Microsoft YaHei", sans-serif; margin: 24px; color: #1f2328; }
  h1 { font-size: 18px; margin: 0 0 4px; }
  .sub { color: #656d76; font-size: 13px; margin-bottom: 16px; }
  table { border-collapse: collapse; width: 100%; font-size: 13px; }
  th, td { text-align: left; padding: 8px 10px; border-bottom: 1px solid #d0d7de; }
  th { color: #656d76; font-weight: 600; background: #f6f8fa; }
  .btn { background: #0969da; color: #fff; border: 0; border-radius: 6px; padding: 6px 14px; font-size: 13px; cursor: pointer; }
  .btn:disabled { opacity: .6; }
  .err { color: #cf222e; white-space: pre-wrap; }
  .dim { color: #656d76; }
  .badge { display: inline-block; padding: 1px 8px; border-radius: 10px; font-size: 12px; }
  .badge-ok { background: #dafbe1; color: #1a7f37; }
  .badge-off { background: #ffebe9; color: #cf222e; }
</style>
</head>
<body>
<h1>WorkBuddy Bridge（国际版 WorkBuddy AI）</h1>
<div class="sub">dsh-workbuddy-bridge 移植 · 模型：<b>workbuddy-bridge/deepseek-v4.1-flash</b>（免费 x0.00）</div>
<p><button class="btn" id="refresh">刷新</button> <span class="dim" id="meta"></span></p>
<table>
  <thead><tr><th>账号</th><th>UID</th><th>域名</th><th>积分</th><th>状态</th></tr></thead>
  <tbody id="rows"><tr><td colspan="5" class="dim">加载中…</td></tr></tbody>
</table>
<script>
const ACCOUNTS = "/v0/management/plugins/workbuddy-bridge/accounts";
async function load() {
  const btn = document.getElementById("refresh");
  btn.disabled = true;
  const meta = document.getElementById("meta");
  try {
    const r = await fetch(ACCOUNTS, { headers: { Accept: "application/json" } });
    const data = await r.json();
    meta.textContent = "更新于 " + new Date().toLocaleTimeString();
    const tbody = document.getElementById("rows");
    if (!Array.isArray(data) || data.length === 0) {
      tbody.innerHTML = '<tr><td colspan="5" class="dim">没有账号。把 workbuddy-&lt;uid&gt;.json（global）拷贝为 workbuddy-bridge-&lt;uid&gt;.json 后重启生效。</td></tr>';
      return;
    }
    tbody.innerHTML = data.map(a => {
      const err = a.error ? '<div class="err">' + a.error + '</div>' : '';
      const badge = a.disabled ? '<span class="badge badge-off">禁用</span>' : '<span class="badge badge-ok">启用</span>';
      return '<tr><td>' + (a.nickname || a.name || "—") + '</td><td class="dim">' + (a.uid || "—") + '</td><td class="dim">' + (a.domain || "—") + '</td><td>' + (a.credits || "—") + '</td><td>' + badge + err + '</td></tr>';
    }).join("");
  } catch (e) {
    meta.textContent = "";
    document.getElementById("rows").innerHTML = '<tr><td colspan="5" class="err">请求失败: ' + e + '</td></tr>';
  } finally {
    btn.disabled = false;
  }
}
document.getElementById("refresh").addEventListener("click", load);
load();
</script>
</body>
</html>`
