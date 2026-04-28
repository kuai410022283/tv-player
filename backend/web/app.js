// ═══════════════════════════════════════════════════════
// TVPlayer Admin - app.js
// ═══════════════════════════════════════════════════════

const API = '/api/v1';
let groups = [], selectedClientIds = new Set();
let adminToken = localStorage.getItem('admin_token') || '';
let channelPage = 1, clientPage = 1;
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
    if (res.status === 401 || res.status === 403) {
      const data = await res.json().catch(() => ({}));
      if (data.message && data.message.includes('需要管理员权限')) {
        showLogin();
        throw new Error('需要认证');
      }
    }
    return res.json();
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

function fmtDate(d) { return d ? new Date(d).toLocaleString('zh-CN') : '-'; }
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
  document.getElementById('login-modal').classList.add('show');
  document.getElementById('login-password').focus();
}

function hideLogin() { document.getElementById('login-modal').classList.remove('show'); }

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
      hideLogin();
      toast('登录成功');
      loadDashboard();
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
if (!adminToken) { showLogin(); } else { hideLogin(); }

// ═══ Navigation ═══════════════════════════════════════
function showSection(name, el) {
  document.querySelectorAll('.main > div[id^="sec-"]').forEach(e => e.style.display = 'none');
  document.getElementById('sec-' + name).style.display = 'block';
  document.querySelectorAll('.nav-item').forEach(e => e.classList.remove('active'));
  if (el) el.classList.add('active');
  const loaders = {
    dashboard: loadDashboard,
    channels: loadChannels,
    groups: loadGroups,
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
    body.innerHTML = logs.data.map(l =>
      `<tr><td>#${l.client_id}</td><td>${badge(l.action)}</td><td>${esc(l.ip)}</td><td>${fmtDate(l.created_at)}</td></tr>`
    ).join('');
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
      <td>${c.id}</td>
      <td><strong>${esc(c.name)}</strong></td>
      <td>${gm[c.group_id] || '-'}</td>
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
  document.getElementById('ch-group').innerHTML = groups.map(g => `<option value="${g.id}">${g.name}</option>`).join('');
  document.getElementById('channels-page').textContent = channelPage;
  document.getElementById('channels-info').textContent = `共 ${channelTotal} 个频道`;
}

function channelPrevPage() {
  if (channelPage > 1) { channelPage--; loadChannels(document.getElementById('channel-search').value); }
}

function channelNextPage() {
  if (channelPage * PAGE_SIZE < channelTotal) { channelPage++; loadChannels(document.getElementById('channel-search').value); }
}

function searchChannels() {
  clearTimeout(window._st);
  window._st = setTimeout(() => loadChannels(document.getElementById('channel-search').value), 300);
}

async function saveChannel() {
  const id = document.getElementById('ch-edit-id').value;
  const d = {
    name: document.getElementById('ch-name').value,
    group_id: +document.getElementById('ch-group').value,
    stream_url: document.getElementById('ch-url').value,
    stream_type: document.getElementById('ch-type').value,
    logo: document.getElementById('ch-logo').value,
    epg_channel_id: document.getElementById('ch-epg').value
  };
  if (!d.name || !d.stream_url) { toast('请填写名称和流地址', 'error'); return; }
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
async function loadGroups() {
  const r = await api('/groups');
  document.getElementById('groups-body').innerHTML = (r.data || []).map(g => `<tr>
    <td>${g.id}</td><td>${esc(g.name)}</td><td>${g.sort_order}</td>
    <td><div class="btn-group">
      <button class="btn btn-ghost btn-sm" onclick="editGroup(${g.id},'${esc(g.name)}',${g.sort_order})">编辑</button>
      <button class="btn btn-danger btn-sm" onclick="deleteGroup(${g.id})">删除</button>
    </div></td>
  </tr>`).join('');
}

async function saveGroup() {
  const id = document.getElementById('grp-edit-id').value;
  const d = { name: document.getElementById('grp-name').value, sort_order: +document.getElementById('grp-sort').value || 0 };
  if (!d.name) { toast('请填写名称', 'error'); return; }
  await api(id ? `/groups/${id}` : '/groups', { method: id ? 'PUT' : 'POST', body: JSON.stringify(d) });
  hideModal('group-modal');
  loadGroups();
  toast(id ? '已更新' : '已添加');
}

function editGroup(id, n, s) {
  document.getElementById('grp-edit-id').value = id;
  document.getElementById('grp-name').value = n;
  document.getElementById('grp-sort').value = s;
  showModal('group-modal');
}

async function deleteGroup(id) {
  if (!confirm('确定？')) return;
  await api(`/groups/${id}`, { method: 'DELETE' });
  loadGroups();
}

// ═══ Sources ══════════════════════════════════════════
async function loadSources() {
  const r = await api('/m3u');
  document.getElementById('sources-body').innerHTML = (r.data || []).map(s => `<tr>
    <td>${s.id}</td>
    <td>${esc(s.name)}</td>
    <td style="max-width:300px;overflow:hidden;text-overflow:ellipsis">${esc(s.url)}</td>
    <td>${fmtDate(s.last_sync)}</td>
    <td><div class="btn-group">
      <button class="btn btn-primary btn-sm" onclick="importSource(${s.id})">同步</button>
      <button class="btn btn-danger btn-sm" onclick="deleteSource(${s.id})">删除</button>
    </div></td>
  </tr>`).join('');
}

async function saveSource() {
  const d = { name: document.getElementById('src-name').value, url: document.getElementById('src-url').value };
  if (!d.name || !d.url) { toast('请填写完整', 'error'); return; }
  await api('/m3u', { method: 'POST', body: JSON.stringify(d) });
  hideModal('source-modal');
  loadSources();
  toast('已添加');
}

async function importSource(id) {
  toast('正在导入...');
  const r = await api(`/m3u/${id}/import`, { method: 'POST' });
  toast(r.data ? `导入: ${r.data.imported} 频道` : '失败', 'error');
}

async function deleteSource(id) {
  if (!confirm('确定？')) return;
  await api(`/m3u/${id}`, { method: 'DELETE' });
  loadSources();
}

async function importM3UContent() {
  const c = document.getElementById('import-content').value;
  if (!c) { toast('请粘贴内容', 'error'); return; }
  toast('正在导入...');
  const r = await api('/m3u/import-string', { method: 'POST', body: JSON.stringify({ content: c }) });
  toast(r.data ? `导入: ${r.data.imported} 频道` : '失败', 'error');
  if (r.data) hideModal('import-modal');
}

// ═══ Streams ══════════════════════════════════════════
async function loadStreams() {
  const r = await api('/stream/active');
  const body = document.getElementById('streams-body');
  if (r.data && r.data.length) {
    body.innerHTML = r.data.map(s => `<tr>
      <td>${s.channel_id}</td>
      <td style="max-width:300px;overflow:hidden;text-overflow:ellipsis">${esc(s.url)}</td>
      <td>${badge(s.status)}</td>
      <td>${fmtDate(s.started_at)}</td>
    </tr>`).join('');
  } else {
    body.innerHTML = '<tr><td colspan="4" style="text-align:center;color:var(--text2);padding:40px">暂无活跃流</td></tr>';
  }
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
          ${c.status === 'approved' ? `<button class="btn btn-warn btn-sm" onclick="showRejectModal(${c.id})">吊销</button>` : ''}
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

function showApproveModal(id) {
  document.getElementById('approve-client-id').value = id;
  document.getElementById('approve-days').value = '365';
  document.getElementById('approve-streams').value = '2';
  hideModal('client-detail-modal');
  showModal('approve-modal');
}

async function doApprove() {
  const id = +document.getElementById('approve-client-id').value;
  const d = {
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
    body.innerHTML = r.data.map(l => `<tr>
      <td>${l.id}</td>
      <td>#${l.client_id}</td>
      <td>${badge(l.action)}</td>
      <td>${l.channel_id || '-'}</td>
      <td style="font-family:monospace;font-size:12px">${esc(l.ip)}</td>
      <td style="max-width:200px;overflow:hidden;text-overflow:ellipsis">${esc(l.detail)}</td>
      <td>${fmtDate(l.created_at)}</td>
    </tr>`).join('');
  } else {
    body.innerHTML = '<tr><td colspan="7" style="text-align:center;color:var(--text2);padding:40px">暂无日志</td></tr>';
  }
}

// ═══ Client Settings ══════════════════════════════════
async function loadClientSettings() {
  const r = await api('/settings');
  if (r.data) {
    document.getElementById('set-auto-approve').value = r.data.auto_approve || 'false';
    document.getElementById('set-max-streams').value = r.data.default_max_streams || '2';
    document.getElementById('set-expire-days').value = r.data.default_expire_days || '365';
    document.getElementById('set-require-note').value = r.data.require_note || 'false';
  }
}

async function saveAllClientSettings() {
  const settings = {
    auto_approve: document.getElementById('set-auto-approve').value,
    default_max_streams: document.getElementById('set-max-streams').value,
    default_expire_days: document.getElementById('set-expire-days').value,
    require_note: document.getElementById('set-require-note').value,
  };
  for (const [k, v] of Object.entries(settings)) {
    await api('/settings', { method: 'POST', body: JSON.stringify({ key: k, value: v }) });
  }
  toast('策略已保存');
}

async function saveClientSetting(key, value) {
  await api('/settings', { method: 'POST', body: JSON.stringify({ key, value }) });
}

// ═══ Init ═════════════════════════════════════════════
loadDashboard();
