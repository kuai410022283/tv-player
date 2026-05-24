// ═══════════════════════════════════════════════════════
// TVPlayer Admin - app.js
// ═══════════════════════════════════════════════════════

const API = '/api/v1';
let groups = [], selectedClientIds = new Set(), selectedGroupIds = new Set();
let adminToken = localStorage.getItem('admin_token') || '';
let channelPage = 1, clientPage = 1, groupPage = 1;
const PAGE_SIZE = 20;

// ═══ API helpers ══════════════════════════════════════
let loadingCount = 0;

function showLoading() {
  loadingCount++;
  document.getElementById('loading-overlay').classList.add('show');
}

function hideLoading() {
  loadingCount = Math.max(0, loadingCount - 1);
  if (loadingCount === 0) document.getElementById('loading-overlay').classList.remove('show');
}

async function api(path, opts = {}) {
  const headers = { 'Content-Type': 'application/json' };
  if (adminToken) headers['Authorization'] = 'Bearer ' + adminToken;
  showLoading();
  try {
    const res = await fetch(API + path, { headers, ...opts });
    let data = {};
    const text = await res.text();
    if (text) {
      try { data = JSON.parse(text); } catch(e) {}
    }
    if (!res.ok) {
      if (res.status === 401 || res.status === 403) {
        showLogin();
        throw new Error('需要重新登录');
      }
      throw new Error(data.message || `请求失败 (${res.status})`);
    }
    return data;
  } catch (e) {
    toast('请求失败: ' + e.message, 'error');
    throw e;
  } finally {
    hideLoading();
  }
}

function toast(msg, type = 'success') {
  const el = document.getElementById('toast');
  el.textContent = msg;
  el.className = `toast toast-${type}`;
  el.style.display = 'block';
  setTimeout(() => el.style.display = 'none', 3000);
}

function showModal(id) { document.getElementById(id).classList.add('show'); }
function hideModal(id) { document.getElementById(id).classList.remove('show'); }

// ═══ Utilities ════════════════════════════════════════
function timeAgo(dateStr) {
  if (!dateStr) return '-';
  const d = new Date(dateStr), now = new Date(), diff = (now - d) / 1000;
  if (diff < 60) return '刚刚';
  if (diff < 3600) return Math.floor(diff / 60) + '分钟前';
  if (diff < 86400) return Math.floor(diff / 3600) + '小时前';
  return Math.floor(diff / 86400) + '天前';
}

function fmtDate(d) { 
  if (!d || d.startsWith('0001-01-01')) return '<span style="color:var(--text3)">从未同步</span>';
  return new Date(d).toLocaleString('zh-CN'); 
}
function badge(status) { return `<span class="badge badge-${status}">${status}</span>`; }
function esc(s) { if (!s) return ''; const d = document.createElement('div'); d.textContent = s; return d.innerHTML; }

function formatUptime(seconds) {
  if (!seconds || seconds <= 0) return '-';
  const d = Math.floor(seconds / 86400);
  const h = Math.floor((seconds % 86400) / 3600);
  const m = Math.floor((seconds % 3600) / 60);
  if (d > 0) return `${d}天${h}时${m}分`;
  if (h > 0) return `${h}时${m}分`;
  return `${m}分`;
}

// ═══ Login ════════════════════════════════════════════
function showLogin() {
  if (!window.location.pathname.includes('/login.html')) {
    window.location.href = '/admin/login.html';
  }
}

function hideLogin() { /* do nothing */ }

async function doLogin() {
  const password = document.getElementById('login-password').value;
  if (!password) { toast('请输入密码', 'error'); return; }
  try {
    const res = await fetch(API + '/admin/login', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ password })
    });
    const data = await res.json();
    if (data.code === 0 && data.data && data.data.token) {
      adminToken = data.data.token;
      localStorage.setItem('admin_token', adminToken);
      toast('登录成功');
      setTimeout(() => { window.location.href = '/admin/'; }, 500);
    } else {
      toast(data.message || '密码错误', 'error');
    }
  } catch (e) {
    toast('登录失败: ' + e.message, 'error');
  }
}

function logout() {
  adminToken = '';
  localStorage.removeItem('admin_token');
  showLogin();
  toast('已退出登录');
}

// 检查登录状态
if (!adminToken) { showLogin(); }

// ═══ Navigation ═══════════════════════════════════════
function showSection(name, el) {
  document.querySelectorAll('.main > div[id^="sec-"]').forEach(e => e.style.display = 'none');
  document.getElementById('sec-' + name).style.display = 'block';
  document.querySelectorAll('.nav-item').forEach(e => e.classList.remove('active'));
  
  if (el) {
    el.classList.add('active');
  } else {
    // Attempt to find and highlight the correct nav item if el is not provided
    const navItems = document.querySelectorAll('.nav-item');
    for (let item of navItems) {
      if (item.getAttribute('onclick') && item.getAttribute('onclick').includes(`'${name}'`)) {
        item.classList.add('active');
        break;
      }
    }
  }

  localStorage.setItem('last_active_section', name);

  const loaders = {
    dashboard: loadDashboard,
    channels: loadChannels,
    groups: loadGroups,
    plans: loadPlans,
    sources: loadSources,
    streams: loadStreams,
    clients: loadClients,
    'client-logs': loadClientLogs,
    'client-settings': loadClientSettings
  };
  if (loaders[name]) loaders[name]();
}

// ═══ Dashboard ════════════════════════════════════════
async function loadDashboard() {
  const [stats, clientStats, sources, logs] = await Promise.all([
    api('/stats'), api('/admin/clients/stats'), api('/m3u'), api('/admin/clients/logs?limit=10')
  ]);
  const s = stats.data || {};
  document.getElementById('stat-total').textContent = s.total_channels || 0;
  document.getElementById('stat-online').textContent = s.online_channels || 0;
  document.getElementById('stat-streams').textContent = s.active_streams || 0;
  document.getElementById('stat-uptime').textContent = formatUptime(s.uptime_seconds);
  document.getElementById('stat-memory').textContent = s.memory_mb ? s.memory_mb + ' MB' : '-';
  const cs = clientStats.data || {};
  document.getElementById('stat-clients').textContent = cs.total_clients || 0;
  document.getElementById('stat-pending').textContent = cs.pending_clients || 0;
  document.getElementById('stat-online-clients').textContent = cs.online_clients || 0;
  document.getElementById('stat-sources').textContent = sources.data ? sources.data.length : 0;

  const body = document.getElementById('dash-logs-body');
  if (logs.data && logs.data.length) {
    body.innerHTML = logs.data.map(l => {
      let actionBadge = '';
      if (l.action === 'play') actionBadge = '<span class="badge badge-success">播放</span>';
      else if (l.action === 'login') actionBadge = '<span class="badge badge-info">登录</span>';
      else if (l.action === 'heartbeat') actionBadge = '<span class="badge badge-warning" style="background:#eab308;color:#fff;">心跳</span>';
      else if (l.action === 'error') actionBadge = '<span class="badge badge-danger">错误</span>';
      else actionBadge = badge(l.action);

      return `<tr>
        <td><strong>${esc(l.client_name)}</strong> <span style="font-size:11px;color:var(--text2)">#${l.client_id}</span></td>
        <td>${actionBadge}</td>
        <td style="font-family:monospace;font-size:12px">${esc(l.ip)}</td>
        <td>${fmtDate(l.created_at)}</td>
      </tr>`;
    }).join('');
  } else {
    body.innerHTML = '<tr><td colspan="4" style="text-align:center;color:var(--text2);padding:30px">暂无记录</td></tr>';
  }
}

// ═══ Channels ═════════════════════════════════════════
let channelTotal = 0;

async function loadChannels(search = '') {
  const q = search
    ? `?search=${encodeURIComponent(search)}&page=${channelPage}&page_size=${PAGE_SIZE}`
    : `?page=${channelPage}&page_size=${PAGE_SIZE}`;
  const [chRes, grpRes] = await Promise.all([api('/channels' + q), api('/groups')]);
  groups = grpRes.data || [];
  const gm = {};
  groups.forEach(g => gm[g.id] = g.name);
  const body = document.getElementById('channels-body');
  if (chRes.data && chRes.data.items) {
    channelTotal = chRes.data.total || 0;
    body.innerHTML = chRes.data.items.map(c => `<tr>
      <td><input type="checkbox" class="ch-check" value="${c.id}"></td>
      <td>${c.id}</td>
      <td><strong class="text-ellipsis" title="${esc(c.name)}">${esc(c.name)}</strong></td>
      <td>${gm[c.group_id] || '-'}</td>
      <td><span style="font-size:12px;color:var(--text2);background:var(--surface);padding:2px 6px;border-radius:4px">${esc(c.source || '手动')}</span></td>
      <td><span class="badge badge-${c.stream_type}">${c.stream_type.toUpperCase()}</span></td>
      <td>${badge(c.status)}</td>
      <td>${c.is_favorite ? '⭐' : '-'}</td>
      <td><div class="btn-group">
        <button class="btn btn-ghost btn-sm" onclick="editChannel(${c.id})">编辑</button>
        <button class="btn btn-ghost btn-sm" onclick="toggleFav(${c.id})">${c.is_favorite ? '取消' : '⭐'}</button>
        <button class="btn btn-danger btn-sm" onclick="deleteChannel(${c.id})">删除</button>
      </div></td>
    </tr>`).join('');
  }
  document.getElementById('check-all-channels').checked = false;
  document.getElementById('ch-group').innerHTML = groups.map(g => `<option value="${g.id}">${g.name}</option>`).join('');
  document.getElementById('channels-page').textContent = channelPage;
  document.getElementById('channels-info').textContent = `共 ${channelTotal} 个频道`;
}

function toggleAllChannels(cb) {
  document.querySelectorAll('.ch-check').forEach(el => el.checked = cb.checked);
}

async function doChannelBatchDelete() {
  const ids = Array.from(document.querySelectorAll('.ch-check:checked')).map(el => +el.value);
  if (!ids.length) { toast('请先选择要删除的频道', 'error'); return; }
  await api('/channels/batch', { method: 'DELETE', body: JSON.stringify({ ids }) });
  hideModal('channel-batch-modal');
  toast(`已删除 ${ids.length} 个频道`);
  loadChannels(document.getElementById('channel-search').value);
}

function channelPrevPage() {
  if (channelPage > 1) { channelPage--; loadChannels(document.getElementById('channel-search').value); }
}

function channelNextPage() {
  if (channelPage * PAGE_SIZE < channelTotal) { channelPage++; loadChannels(document.getElementById('channel-search').value); }
}

function searchChannels() {
  clearTimeout(window._st);
  window._st = setTimeout(() => {
    channelPage = 1;
    loadChannels(document.getElementById('channel-search').value);
  }, 300);
}

function showAddChannelModal() {
  document.getElementById('channel-modal-title').textContent = '添加频道';
  document.getElementById('ch-edit-id').value = '';
  document.getElementById('ch-name').value = '';
  if (groups.length > 0) {
    document.getElementById('ch-group').value = groups[0].id;
  }
  document.getElementById('ch-url').value = '';
  document.getElementById('ch-type').value = '';
  document.getElementById('ch-logo').value = '';
  document.getElementById('ch-epg').value = '';
  document.getElementById('ch-is-direct').value = 'true';
  document.getElementById('ch-user-agent').value = '';
  document.getElementById('ch-headers').value = '';
  showModal('channel-modal');
}

async function saveChannel() {
  const id = document.getElementById('ch-edit-id').value;
  const d = {
    name: document.getElementById('ch-name').value,
    group_id: +document.getElementById('ch-group').value,
    stream_url: document.getElementById('ch-url').value,
    stream_type: document.getElementById('ch-type').value,
    logo: document.getElementById('ch-logo').value,
    epg_channel_id: document.getElementById('ch-epg').value,
    is_direct: document.getElementById('ch-is-direct').value === 'true',
    user_agent: document.getElementById('ch-user-agent').value,
    custom_headers: document.getElementById('ch-headers').value
  };
  if (!d.name || !d.stream_url) { toast('请填写名称和流地址', 'error'); return; }
  if (d.custom_headers) {
    try {
      JSON.parse(d.custom_headers);
    } catch (e) {
      toast('自定义 Headers 必须是合法的 JSON 格式', 'error');
      return;
    }
  }
  await api(id ? `/channels/${id}` : '/channels', { method: id ? 'PUT' : 'POST', body: JSON.stringify(d) });
  hideModal('channel-modal');
  loadChannels();
  toast(id ? '已更新' : '已添加');
}

async function editChannel(id) {
  const r = await api(`/channels/${id}`);
  if (!r.data) return;
  const c = r.data;
  document.getElementById('ch-edit-id').value = c.id;
  document.getElementById('ch-name').value = c.name;
  document.getElementById('ch-group').value = c.group_id;
  document.getElementById('ch-url').value = c.stream_url;
  document.getElementById('ch-type').value = c.stream_type;
  document.getElementById('ch-logo').value = c.logo || '';
  document.getElementById('ch-epg').value = c.epg_channel_id || '';
  document.getElementById('ch-is-direct').value = c.is_direct !== false ? 'true' : 'false';
  document.getElementById('ch-user-agent').value = c.user_agent || '';
  document.getElementById('ch-headers').value = c.custom_headers || '';
  document.getElementById('channel-modal-title').textContent = '编辑频道';
  showModal('channel-modal');
}

async function deleteChannel(id) {
  if (!confirm('确定删除？')) return;
  await api(`/channels/${id}`, { method: 'DELETE' });
  loadChannels();
}

async function toggleFav(id) {
  await api(`/channels/${id}/favorite`, { method: 'POST' });
  loadChannels();
}

// ═══ Groups ═══════════════════════════════════════════
let groupTotal = 0;

async function loadGroups() {
  const search = document.getElementById('group-search').value;
  let q = `?page=${groupPage}&page_size=${PAGE_SIZE}`;
  if (search) q += '&search=' + encodeURIComponent(search);

  const r = await api('/admin/groups' + q);
  const items = r.data ? r.data.items || [] : [];
  groupTotal = r.data ? r.data.total || 0 : 0;
  selectedGroupIds.clear();

  // Merge items into global groups array
  items.forEach(item => {
    const idx = groups.findIndex(g => g.id === item.id);
    if (idx >= 0) {
      groups[idx] = item;
    } else {
      groups.push(item);
    }
  });

  document.getElementById('groups-body').innerHTML = items.map(g => {
    const isDefault = g.name === '未分类';
    return `<tr>
    <td>${isDefault ? '' : `<input type="checkbox" class="group-check" value="${g.id}" onchange="updateSelectedGroups()">`}</td>
    <td>${g.id}</td><td>${esc(g.name)}</td><td>${g.sort_order}</td>
    <td><span style="font-size:12px;color:var(--text2);background:var(--surface);padding:2px 6px;border-radius:4px">${esc(g.source || '手动')}</span></td>
    <td>${g.is_direct ? '<span class="badge badge-success">开启</span>' : '<span class="badge badge-warn">关闭</span>'}</td>
    <td>
      ${isDefault ? '<span style="color:var(--text3);font-size:12px;user-select:none">系统内置</span>' : `<div class="btn-group">
        <button class="btn btn-ghost btn-sm" onclick="editGroup(${g.id})">编辑</button>
        <button class="btn btn-danger btn-sm" onclick="deleteGroup(${g.id}, '${esc(g.source || '手动')}', '${esc(g.name)}', ${g.channel_count})">删除</button>
      </div>`}
    </td>
  </tr>`}).join('');

  document.getElementById('groups-page').textContent = groupPage;
  document.getElementById('groups-info').textContent = `共 ${groupTotal} 个分组`;
}

function groupPrevPage() { if (groupPage > 1) { groupPage--; loadGroups(); } }
function groupNextPage() { if (groupPage * PAGE_SIZE < groupTotal) { groupPage++; loadGroups(); } }
function searchGroups() { clearTimeout(window._gt); window._gt = setTimeout(() => { groupPage = 1; loadGroups(); }, 300); }

function toggleAllGroups(el) {
  document.querySelectorAll('.group-check').forEach(cb => { cb.checked = el.checked; });
  updateSelectedGroups();
}

function updateSelectedGroups() {
  selectedGroupIds.clear();
  document.querySelectorAll('.group-check:checked').forEach(cb => selectedGroupIds.add(+cb.value));
}

async function doGroupBatchDelete() {
  if (selectedGroupIds.size === 0) { toast('请先勾选分组', 'error'); hideModal('group-batch-modal'); return; }
  const r = await api('/groups/batch', {
    method: 'POST',
    body: JSON.stringify({ ids: [...selectedGroupIds], action: 'delete' })
  });
  hideModal('group-batch-modal');
  toast(`已删除勾选的分组`);
  loadGroups();
}

function showAddGroupModal() {
  document.getElementById('grp-edit-id').value = '';
  document.getElementById('grp-name').value = '';
  document.getElementById('grp-sort').value = '0';
  document.getElementById('grp-is-direct').value = 'true';
  document.getElementById('grp-user-agent').value = '';
  document.getElementById('grp-headers').value = '';
  showModal('group-modal');
}

async function saveGroup() {
  const id = document.getElementById('grp-edit-id').value;
  const d = { 
    name: document.getElementById('grp-name').value, 
    sort_order: +document.getElementById('grp-sort').value || 0,
    is_direct: document.getElementById('grp-is-direct').value === 'true',
    user_agent: document.getElementById('grp-user-agent').value,
    custom_headers: document.getElementById('grp-headers').value
  };
  if (!d.name) { toast('请填写名称', 'error'); return; }
  if (d.custom_headers) {
    try {
      JSON.parse(d.custom_headers);
    } catch (e) {
      toast('自定义 Headers 必须是合法的 JSON 格式', 'error');
      return;
    }
  }
  await api(id ? `/groups/${id}` : '/groups', { method: id ? 'PUT' : 'POST', body: JSON.stringify(d) });
  hideModal('group-modal');
  loadGroups();
  toast(id ? '已更新' : '已添加');
}

function editGroup(id) {
  const g = groups.find(x => x.id === id);
  if (!g) return;
  document.getElementById('grp-edit-id').value = g.id;
  document.getElementById('grp-name').value = g.name;
  document.getElementById('grp-sort').value = g.sort_order;
  document.getElementById('grp-is-direct').value = g.is_direct !== false ? 'true' : 'false';
  document.getElementById('grp-user-agent').value = g.user_agent || '';
  document.getElementById('grp-headers').value = g.custom_headers || '';
  showModal('group-modal');
}

async function deleteGroup(id, source, name, count) {
  if (!confirm(`该分组 [${source} - ${name}] 下包含 ${count} 个频道。\n删除分组将同步删除这些频道，此操作不可恢复，确定要删除吗？`)) return;
  await api(`/groups/${id}`, { method: 'DELETE' });
  loadGroups();
}

let sourcesList = [];
async function loadSources() {
  const r = await api('/m3u');
  sourcesList = r.data || [];
  document.getElementById('sources-body').innerHTML = sourcesList.map(s => `<tr>
    <td>${s.id}</td>
    <td><strong>${esc(s.name)}</strong></td>
    <td style="max-width:300px;overflow:hidden;text-overflow:ellipsis" title="${esc(s.url)}">${esc(s.url)}</td>
    <td>${s.auto_sync ? `<span class="badge badge-online">开启 (${s.sync_interval}h)</span>` : '<span class="badge badge-offline">关闭</span>'}</td>
    <td>${fmtDate(s.last_sync)}</td>
    <td><div class="btn-group">
      <button class="btn btn-primary btn-sm" onclick="importSource(${s.id})">同步</button>
      <button class="btn btn-ghost btn-sm" onclick="editSource(${s.id})">编辑</button>
      <button class="btn btn-danger btn-sm" onclick="deleteSource(${s.id})">删除</button>
    </div></td>
  </tr>`).join('');
}

function showAddSourceModal() {
  document.getElementById('src-modal-title').innerText = '添加M3U源';
  document.getElementById('src-edit-id').value = '';
  document.getElementById('src-name').value = '';
  document.getElementById('src-url').value = '';
  document.getElementById('src-auto-sync').value = 'false';
  document.getElementById('src-sync-interval').value = '12';
  document.getElementById('src-user-agent').value = '';
  document.getElementById('src-headers').value = '';
  showModal('source-modal');
}

function editSource(id) {
  const s = sourcesList.find(x => x.id === id);
  if (!s) return;
  document.getElementById('src-modal-title').innerText = '编辑M3U源';
  document.getElementById('src-edit-id').value = s.id;
  document.getElementById('src-name').value = s.name;
  document.getElementById('src-url').value = s.url;
  document.getElementById('src-auto-sync').value = s.auto_sync ? 'true' : 'false';
  document.getElementById('src-sync-interval').value = s.sync_interval || 12;
  document.getElementById('src-user-agent').value = s.user_agent || '';
  document.getElementById('src-headers').value = s.custom_headers || '';
  showModal('source-modal');
}

async function saveSource() {
  const id = document.getElementById('src-edit-id').value;
  const d = { 
    name: document.getElementById('src-name').value, 
    url: document.getElementById('src-url').value,
    auto_sync: document.getElementById('src-auto-sync').value === 'true',
    sync_interval: parseInt(document.getElementById('src-sync-interval').value) || 12,
    user_agent: document.getElementById('src-user-agent').value,
    custom_headers: document.getElementById('src-headers').value
  };
  if (!d.name || !d.url) { toast('请填写完整', 'error'); return; }
  if (d.custom_headers) {
    try {
      JSON.parse(d.custom_headers);
    } catch (e) {
      toast('自定义 Headers 必须是合法的 JSON 格式', 'error');
      return;
    }
  }
  await api(id ? `/m3u/${id}` : '/m3u', { method: id ? 'PUT' : 'POST', body: JSON.stringify(d) });
  hideModal('source-modal');
  loadSources();
  toast(id ? '已更新' : '已添加');
}

async function importSource(id) {
  toast('已发起后台同步...');
  const r = await api(`/m3u/${id}/import`, { method: 'POST' });
  if (r.data && r.data.message) {
    toast(r.data.message, 'success');
  } else {
    toast('失败', 'error');
  }
  // 3秒后刷新列表查看同步状态
  setTimeout(loadSources, 3000);
}

async function deleteSource(id) {
  if (!confirm('确定？')) return;
  await api(`/m3u/${id}`, { method: 'DELETE' });
  loadSources();
}

async function importM3UContent() {
  const n = document.getElementById('import-name').value;
  const c = document.getElementById('import-content').value;
  if (!n) { toast('请填写来源名称', 'error'); return; }
  if (!c) { toast('请粘贴内容', 'error'); return; }
  toast('正在导入...');
  const r = await api('/m3u/import-string', { method: 'POST', body: JSON.stringify({ name: n, content: c }) });
  toast(r.data ? `导入: ${r.data.imported} 频道` : '失败', 'error');
  if (r.data) hideModal('import-modal');
}

// ═══ Streams ══════════════════════════════════════════
function formatSpeed(bytesPerSec) {
  if (!bytesPerSec) return '0 KB/s';
  if (bytesPerSec > 1024 * 1024) return (bytesPerSec / (1024 * 1024)).toFixed(2) + ' MB/s';
  return (bytesPerSec / 1024).toFixed(1) + ' KB/s';
}

async function loadStreams() {
  const r = await api('/stream/active');
  const body = document.getElementById('streams-body');
  if (r.data && r.data.length) {
    body.innerHTML = r.data.map(s => `<tr>
      <td><div style="font-size:12px;color:var(--text2)">${s.session_id.substring(0, 15)}...</div>
          <strong>${esc(s.client_name) || ('设备ID: ' + s.client_id)}</strong><br>
          <span style="font-size:11px;color:var(--text2)">IP: ${s.client_ip}</span></td>
      <td><strong>${esc(s.channel_name)}</strong><br><span style="font-size:11px;color:var(--text2)">ID: ${s.channel_id}</span></td>
      <td style="max-width:200px;overflow:hidden;text-overflow:ellipsis;white-space:nowrap" title="${esc(s.url)}">${esc(s.url)}</td>
      <td>${badge(s.status)}<br><span style="font-size:12px;color:var(--accent);font-weight:bold">${formatSpeed(s.speed_bytes)}</span></td>
      <td><span style="font-size:11px">启动: ${fmtDate(s.started_at)}</span><br><span style="font-size:11px;color:var(--text2)">活跃: ${fmtDate(s.last_active)}</span></td>
      <td><button class="btn btn-danger btn-sm" onclick="killStream('${s.session_id}')">踢下线</button></td>
    </tr>`).join('');
  } else {
    body.innerHTML = '<tr><td colspan="6" style="text-align:center;color:var(--text2);padding:40px">暂无活跃流</td></tr>';
  }
}

async function killStream(sessionId) {
  if (!confirm('确定要强制断开该代理流吗？')) return;
  await api(`/stream/active/${sessionId}`, { method: 'DELETE' });
  toast('指令已发送');
  setTimeout(loadStreams, 500);
}

// ═══ Clients ══════════════════════════════════════════
let clientTotal = 0;

async function loadClients() {
  const status = document.getElementById('client-status-filter').value;
  const search = document.getElementById('client-search').value;
  let q = `?page=${clientPage}&page_size=${PAGE_SIZE}`;
  if (status) q += '&status=' + status;
  if (search) q += '&search=' + encodeURIComponent(search);

  const [listRes, statsRes] = await Promise.all([api('/admin/clients' + q), api('/admin/clients/stats')]);

  const st = statsRes.data || {};
  document.getElementById('cstat-total').textContent = st.total_clients || 0;
  document.getElementById('cstat-pending').textContent = st.pending_clients || 0;
  document.getElementById('cstat-online').textContent = st.online_clients || 0;

  const body = document.getElementById('clients-body');
  const items = listRes.data ? listRes.data.items || [] : [];
  clientTotal = listRes.data ? listRes.data.total || 0 : 0;
  selectedClientIds.clear();

  if (items.length === 0) {
    body.innerHTML = '<tr><td colspan="11" style="text-align:center;color:var(--text2);padding:40px">暂无设备</td></tr>';
  } else {
    body.innerHTML = items.map(c => `<tr>
      <td><input type="checkbox" class="client-check" value="${c.id}" onchange="updateSelectedClients()"></td>
      <td>${c.id}</td>
      <td><strong>${esc(c.name)}</strong><br><span style="font-size:11px;color:var(--text2)">${esc(c.device_id).substring(0, 16)}...</span></td>
      <td>${esc(c.device_model)}<br><span style="font-size:11px;color:var(--text2)">${esc(c.device_os)}</span></td>
      <td style="font-family:monospace;font-size:12px">${esc(c.ip)}</td>
      <td>${badge(c.status)}</td>
      <td>${c.max_streams}</td>
      <td>${c.total_play_minutes}分钟</td>
      <td>${timeAgo(c.last_seen)}</td>
      <td>${fmtDate(c.created_at)}</td>
      <td>
        <div class="btn-group">
          <button class="btn btn-ghost btn-sm" onclick="showClientDetail(${c.id})">详情</button>
          ${c.status === 'pending' ? `<button class="btn btn-primary btn-sm" onclick="showApproveModal(${c.id})">通过</button><button class="btn btn-danger btn-sm" onclick="showRejectModal(${c.id})">拒绝</button>` : ''}
          ${c.status === 'approved' ? `<button class="btn btn-primary btn-sm" onclick="showApproveModal(${c.id})">改授权</button><button class="btn btn-warn btn-sm" onclick="showRejectModal(${c.id})">吊销</button>` : ''}
          ${c.status === 'rejected' || c.status === 'banned' ? `<button class="btn btn-info btn-sm" onclick="unbanClient(${c.id})">解封</button>` : ''}
        </div>
      </td>
    </tr>`).join('');
  }
  document.getElementById('clients-page').textContent = clientPage;
  document.getElementById('clients-info').textContent = `共 ${clientTotal} 台设备`;
}

function clientPrevPage() { if (clientPage > 1) { clientPage--; loadClients(); } }
function clientNextPage() { if (clientPage * PAGE_SIZE < clientTotal) { clientPage++; loadClients(); } }
function searchClients() { clearTimeout(window._ct); window._ct = setTimeout(loadClients, 300); }

function toggleAllClients(el) {
  document.querySelectorAll('.client-check').forEach(cb => { cb.checked = el.checked; });
  updateSelectedClients();
}

function updateSelectedClients() {
  selectedClientIds.clear();
  document.querySelectorAll('.client-check:checked').forEach(cb => selectedClientIds.add(+cb.value));
}

async function showClientDetail(id) {
  const r = await api(`/admin/clients/${id}`);
  if (!r.data) { toast('加载失败', 'error'); return; }
  const c = r.data;
  const tokenPreview = c.token_preview ? c.token_preview + '********' : '(无令牌)';

  document.getElementById('client-detail-content').innerHTML = `
    <div class="detail-grid" style="margin-bottom:20px">
      <div class="label">设备ID</div><div class="value" style="font-family:monospace">${esc(c.device_id)}</div>
      <div class="label">设备名称</div><div class="value">${esc(c.name)}</div>
      <div class="label">设备型号</div><div class="value">${esc(c.device_model)}</div>
      <div class="label">系统版本</div><div class="value">${esc(c.device_os)}</div>
      <div class="label">客户端版本</div><div class="value">${esc(c.app_version)}</div>
      <div class="label">IP地址</div><div class="value" style="font-family:monospace">${esc(c.ip)}</div>
      <div class="label">状态</div><div class="value">${badge(c.status)}</div>
      <div class="label">当前套餐</div><div class="value">${c.plan_name ? '<span class="badge badge-info">' + esc(c.plan_name) + '</span>' : '-'}</div>
      <div class="label">最大并发流</div><div class="value">${c.max_streams}</div>
      <div class="label">授权过期</div><div class="value">${fmtDate(c.expires_at)}</div>
      <div class="label">审批人</div><div class="value">${esc(c.approved_by) || '-'}</div>
      <div class="label">拒绝原因</div><div class="value">${esc(c.reject_reason) || '-'}</div>
      <div class="label">累计播放</div><div class="value">${c.total_play_minutes} 分钟</div>
      <div class="label">最近在线</div><div class="value">${fmtDate(c.last_seen)}</div>
      <div class="label">注册时间</div><div class="value">${fmtDate(c.created_at)}</div>
      <div class="label">申请备注</div><div class="value">${esc(c.request_note) || '-'}</div>
      <div class="label">令牌</div><div class="value"><code style="font-size:12px">${tokenPreview}</code></div>
    </div>
    <div class="btn-group" style="flex-wrap:wrap">
      <button class="btn btn-ghost btn-sm" onclick="showTokenModal(${c.id})">🔑 令牌管理</button>
      ${c.status === 'approved' ? `<button class="btn btn-warn btn-sm" onclick="banClient(${c.id},'管理员封禁')">封禁</button>` : ''}
      ${c.status !== 'approved' ? `<button class="btn btn-primary btn-sm" onclick="showApproveModal(${c.id})">通过</button>` : ''}
      <button class="btn btn-danger btn-sm" onclick="deleteClient(${c.id})">删除设备</button>
    </div>
  `;
  showModal('client-detail-modal');
}

let allPlans = [];

async function loadPlans() {
  const r = await api('/admin/plans');
  allPlans = r.data || [];
  document.getElementById('plans-body').innerHTML = allPlans.map(p => `<tr>
    <td>${p.id}</td>
    <td><strong>${esc(p.name)}</strong></td>
    <td>${p.days > 0 ? p.days + ' 天' : '永久'}</td>
    <td>${p.max_streams}</td>
    <td>${p.price > 0 ? '¥' + p.price : '-'}</td>
    <td>${esc(p.description)}</td>
    <td><div class="btn-group">
      <button class="btn btn-ghost btn-sm" onclick="editPlan(${p.id})">编辑</button>
      <button class="btn btn-danger btn-sm" onclick="deletePlan(${p.id})">删除</button>
    </div></td>
  </tr>`).join('');
}

async function savePlan() {
  const id = document.getElementById('plan-edit-id').value;
  
  // Collect selected groups
  const checkboxes = document.querySelectorAll('#plan-groups-container input[type="checkbox"]:checked');
  const groupIds = Array.from(checkboxes).map(cb => parseInt(cb.value));

  const d = {
    name: document.getElementById('plan-name').value,
    days: +document.getElementById('plan-days').value || 0,
    max_streams: +document.getElementById('plan-streams').value || 1,
    price: parseFloat(document.getElementById('plan-price').value) || 0.0,
    description: document.getElementById('plan-desc').value,
    group_ids: groupIds
  };
  if (!d.name) { toast('请填写名称', 'error'); return; }
  await api(id ? `/admin/plans/${id}` : '/admin/plans', { method: id ? 'PUT' : 'POST', body: JSON.stringify(d) });
  hideModal('plan-modal');
  loadPlans();
  toast(id ? '已更新' : '已添加');
}

async function editPlan(id) {
  // Fetch groups if not already loaded
  const groupsRes = await api('/admin/groups');
  const groups = (groupsRes.data && groupsRes.data.items) ? groupsRes.data.items : [];

  let p = { name: '', days: 365, max_streams: 2, price: 0, description: '', group_ids: [] };
  if (id) {
    const found = allPlans.find(x => x.id === id);
    if (found) p = found;
  }

  // Render checkboxes
  const container = document.getElementById('plan-groups-container');
  container.innerHTML = groups.map(g => {
    const isChecked = p.group_ids && p.group_ids.includes(g.id);
    return `<label style="display:flex;align-items:center;gap:5px;cursor:pointer;background:var(--bg2);padding:4px 10px;border-radius:4px;">
      <input type="checkbox" value="${g.id}" ${isChecked ? 'checked' : ''}>
      ${esc(g.name)}
    </label>`;
  }).join('');

  document.getElementById('plan-edit-id').value = id || '';
  document.getElementById('plan-name').value = p.name;
  document.getElementById('plan-days').value = p.days;
  document.getElementById('plan-streams').value = p.max_streams;
  document.getElementById('plan-price').value = p.price;
  document.getElementById('plan-desc').value = p.description;
  showModal('plan-modal');
}

async function deletePlan(id) {
  if (!confirm('确定删除此套餐？')) return;
  await api(`/admin/plans/${id}`, { method: 'DELETE' });
  loadPlans();
}

async function showApproveModal(id) {
  document.getElementById('approve-client-id').value = id;
  const [plansRes, clientRes] = await Promise.all([
    api('/admin/plans'),
    api('/admin/clients/' + id)
  ]);
  const plans = plansRes.data || [];
  const client = clientRes.data || {};
  
  const select = document.getElementById('approve-plan-id');
  select.innerHTML = '<option value="0">-- 自定义授权 (不绑定套餐) --</option>' + 
    plans.map(p => `<option value="${p.id}" data-days="${p.days}" data-streams="${p.max_streams}">${esc(p.name)}</option>`).join('');
  
  select.value = client.plan_id || 0;

  if (client.plan_id > 0) {
    document.getElementById('approve-days').disabled = true;
    document.getElementById('approve-streams').disabled = true;
    const plan = plans.find(p => p.id === client.plan_id);
    if (plan) {
      document.getElementById('approve-days').value = plan.days;
      document.getElementById('approve-streams').value = plan.max_streams;
    }
  } else {
    document.getElementById('approve-days').disabled = false;
    document.getElementById('approve-streams').disabled = false;
    document.getElementById('approve-days').value = client.expires_at ? Math.max(0, Math.ceil((new Date(client.expires_at) - new Date()) / (1000 * 3600 * 24))) : 365;
    document.getElementById('approve-streams').value = client.max_streams || 2;
  }
  
  hideModal('client-detail-modal');
  showModal('approve-modal');
}

function onApprovePlanChange() {
  const select = document.getElementById('approve-plan-id');
  const opt = select.options[select.selectedIndex];
  if (select.value === "0") {
    document.getElementById('approve-days').disabled = false;
    document.getElementById('approve-streams').disabled = false;
  } else {
    document.getElementById('approve-days').value = opt.dataset.days;
    document.getElementById('approve-streams').value = opt.dataset.streams;
    document.getElementById('approve-days').disabled = true;
    document.getElementById('approve-streams').disabled = true;
  }
}

async function doApprove() {
  const id = +document.getElementById('approve-client-id').value;
  const d = {
    plan_id: +document.getElementById('approve-plan-id').value,
    max_days: +document.getElementById('approve-days').value,
    max_streams: +document.getElementById('approve-streams').value
  };
  const r = await api(`/admin/clients/${id}/approve`, { method: 'POST', body: JSON.stringify(d) });
  hideModal('approve-modal');
  if (r.code === 0) { toast('已审批通过'); loadClients(); } else { toast(r.message, 'error'); }
}

function showRejectModal(id) {
  document.getElementById('reject-client-id').value = id;
  document.getElementById('reject-reason').value = '';
  hideModal('client-detail-modal');
  showModal('reject-modal');
}

async function doReject() {
  const id = +document.getElementById('reject-client-id').value;
  const reason = document.getElementById('reject-reason').value || '管理员拒绝';
  const r = await api(`/admin/clients/${id}/reject`, { method: 'POST', body: JSON.stringify({ reason }) });
  hideModal('reject-modal');
  if (r.code === 0) { toast('已拒绝'); loadClients(); } else { toast(r.message, 'error'); }
}

async function banClient(id, reason) {
  if (!confirm('确定封禁此设备？')) return;
  await api(`/admin/clients/${id}/ban`, { method: 'POST', body: JSON.stringify({ reason }) });
  hideModal('client-detail-modal');
  toast('已封禁');
  loadClients();
}

async function unbanClient(id) {
  await api(`/admin/clients/${id}/unban`, { method: 'POST' });
  toast('已解封');
  loadClients();
}

async function deleteClient(id) {
  if (!confirm('确定删除此设备？删除后无法恢复。')) return;
  await api(`/admin/clients/${id}`, { method: 'DELETE' });
  hideModal('client-detail-modal');
  toast('已删除');
  loadClients();
}

// ── Token management ──
async function showTokenModal(id) {
  const r = await api(`/admin/clients/${id}`);
  if (!r.data) return;
  const c = r.data;
  const tokenPreview = c.token_preview ? c.token_preview + '********' : '(无令牌)';

  document.getElementById('token-modal-content').innerHTML = `
    <p style="color:var(--text2);font-size:13px;margin-bottom:16px">设备: <strong>${esc(c.name)}</strong> (#${c.id})</p>
    <div class="token-box" id="token-display">${tokenPreview}</div>
    <div class="btn-group" style="margin-top:16px">
      <button class="btn btn-primary btn-sm" onclick="regenerateToken(${c.id})">🔄 重新生成令牌</button>
      <button class="btn btn-danger btn-sm" onclick="revokeToken(${c.id})">🚫 吊销令牌</button>
    </div>
    <p style="color:var(--text2);font-size:11px;margin-top:12px">吊销后客户端需要重新注册，重新生成会替换旧令牌</p>
  `;
  hideModal('client-detail-modal');
  showModal('token-modal');
}

async function regenerateToken(id) {
  if (!confirm('重新生成令牌？旧令牌将立即失效。')) return;
  const r = await api(`/admin/clients/${id}/regenerate`, { method: 'POST' });
  if (r.data) {
    document.getElementById('token-display').innerHTML =
      `<strong style="color:var(--accent)">${r.data.token}</strong><br><span style="font-size:11px;color:var(--warn)">⚠️ 请立即复制保存，关闭后无法再次查看</span>`;
    toast('新令牌已生成');
  } else { toast('操作失败', 'error'); }
}

async function revokeToken(id) {
  if (!confirm('吊销令牌？客户端将无法连接。')) return;
  await api(`/admin/clients/${id}/revoke`, { method: 'POST' });
  toast('令牌已吊销');
  hideModal('token-modal');
  loadClients();
}

// ── Batch operations ──
async function doBatch() {
  if (selectedClientIds.size === 0) { toast('请先勾选设备', 'error'); return; }
  const action = document.getElementById('batch-action').value;
  if (!confirm(`确定对 ${selectedClientIds.size} 个设备执行 [${action}] 操作？`)) return;

  const r = await api('/admin/clients/batch', {
    method: 'POST',
    body: JSON.stringify({ ids: [...selectedClientIds], action })
  });
  hideModal('batch-modal');

  if (r.data) { toast(`已处理 ${r.data.affected} 个设备`); loadClients(); }
  else toast('操作失败', 'error');
}

// ═══ Client Logs ══════════════════════════════════════
async function loadClientLogs() {
  const r = await api('/admin/clients/logs?limit=200');
  const body = document.getElementById('client-logs-body');
  if (r.data && r.data.length) {
    body.innerHTML = r.data.map(l => {
      let actionBadge = '';
      if (l.action === 'play') actionBadge = '<span class="badge badge-success">播放</span>';
      else if (l.action === 'login') actionBadge = '<span class="badge badge-info">登录</span>';
      else if (l.action === 'heartbeat') actionBadge = '<span class="badge badge-warning" style="background:#eab308;color:#fff;">心跳</span>';
      else if (l.action === 'error') actionBadge = '<span class="badge badge-danger">错误</span>';
      else actionBadge = badge(l.action);

      return `<tr>
        <td>${l.id}</td>
        <td><strong>${esc(l.client_name)}</strong><br><span style="font-size:11px;color:var(--text2)">ID: #${l.client_id}</span></td>
        <td>${actionBadge}</td>
        <td><strong>${l.channel_name ? esc(l.channel_name) : '-'}</strong><br><span style="font-size:11px;color:var(--text2)">${l.channel_id ? 'ID: ' + l.channel_id : ''}</span></td>
        <td style="font-family:monospace;font-size:12px">${esc(l.ip)}</td>
        <td style="max-width:200px;overflow:hidden;text-overflow:ellipsis">${esc(l.detail)}</td>
        <td>${fmtDate(l.created_at)}</td>
      </tr>`;
    }).join('');
  } else {
    body.innerHTML = '<tr><td colspan="7" style="text-align:center;color:var(--text2);padding:40px">暂无日志</td></tr>';
  }
}

// ═══ Client Settings & EPG ══════════════════════════════
async function loadClientSettings() {
  const [setRes, plansRes] = await Promise.all([
    api('/settings'),
    api('/admin/plans')
  ]);

  const select = document.getElementById('set-default-plan-id');
  const plans = plansRes.data || [];
  select.innerHTML = '<option value="0">-- 自定义授权 (使用下方并发和天数) --</option>' + 
    plans.map(p => `<option value="${p.id}">${esc(p.name)}</option>`).join('');

  if (setRes.data) {
    document.getElementById('set-auto-approve').value = setRes.data.auto_approve || 'false';
    document.getElementById('set-default-plan-id').value = setRes.data.default_plan_id || '0';
    document.getElementById('set-max-streams').value = setRes.data.default_max_streams || '2';
    document.getElementById('set-expire-days').value = setRes.data.default_expire_days || '365';
    document.getElementById('set-require-note').value = setRes.data.require_note || 'false';
    
    // EPG 配置
    if(document.getElementById('set-epg-source-url')) {
      document.getElementById('set-epg-source-url').value = setRes.data.epg_source_url || '';
      document.getElementById('set-epg-refresh-hours').value = setRes.data.epg_refresh_hours || '12';
    }
  }
}

async function saveAllClientSettings() {
  const settings = {
    auto_approve: document.getElementById('set-auto-approve').value,
    default_plan_id: document.getElementById('set-default-plan-id').value,
    default_max_streams: document.getElementById('set-max-streams').value,
    default_expire_days: document.getElementById('set-expire-days').value,
    require_note: document.getElementById('set-require-note').value,
  };
  
  if(document.getElementById('set-epg-source-url')) {
    settings.epg_source_url = document.getElementById('set-epg-source-url').value.trim();
    settings.epg_refresh_hours = document.getElementById('set-epg-refresh-hours').value;
  }

  for (const [k, v] of Object.entries(settings)) {
    await api('/settings', { method: 'POST', body: JSON.stringify({ key: k, value: String(v) }) });
  }
  toast('策略及 EPG 配置已保存');
}

async function refreshEPGCache() {
  try {
    const res = await api('/admin/epg/refresh', { method: 'POST' });
    if(res.code === 0) {
      toast(res.data.message || '强制刷新已触发');
    }
  } catch (e) {
    toast('触发失败: ' + e.message, true);
  }
}

async function saveClientSetting(key, value) {
  await api('/settings', { method: 'POST', body: JSON.stringify({ key, value: String(value) }) });
}

// ═══ Init ═════════════════════════════════════════════
if (!window.location.pathname.includes('/login.html') && adminToken) {
  const lastSection = localStorage.getItem('last_active_section') || 'dashboard';
  showSection(lastSection);
}
