const state = {
  token: localStorage.getItem('dockertree.token') || '',
  theme: localStorage.getItem('dockertree.theme') || preferredTheme(),
  activeView: 'overview',
  projects: [],
  localImages: [],
  stats: [],
  statsLoaded: false,
  operations: [],
  templates: [],
  cleanup: null,
  composePreview: null,
  imageSearchResults: [],
  selected: null,
  selectedContainer: null,
  autoRefreshInterval: storedAutoRefreshInterval(),
  autoRefreshTimer: null,
  scanInFlight: false,
  pagination: {
    pageSize: 10,
    containers: 1,
    projects: 1,
    images: 1,
    imageSearch: 1,
    services: 1,
  },
};

const tokenInput = document.querySelector('#token');
const themeToggle = document.querySelector('#themeToggle');
const containersEl = document.querySelector('#containers');
const projectsEl = document.querySelector('#projects');
const containerPaginationEl = document.querySelector('#containerPagination');
const projectPaginationEl = document.querySelector('#projectPagination');
const imagePaginationEl = document.querySelector('#imagePagination');
const imageSearchPaginationEl = document.querySelector('#imageSearchPagination');
const deployOutput = document.querySelector('#deployOutput');
const operationOutput = document.querySelector('#operationOutput');
const filterBar = document.querySelector('#filterBar');
const globalSearch = document.querySelector('#globalSearch');
const statusFilter = document.querySelector('#statusFilter');
const healthFilter = document.querySelector('#healthFilter');
const typeFilter = document.querySelector('#typeFilter');
const tagFilter = document.querySelector('#tagFilter');
const favoriteOnly = document.querySelector('#favoriteOnly');
const sortBy = document.querySelector('#sortBy');
const autoRefreshSelect = document.querySelector('#autoRefreshInterval');
const historyTarget = document.querySelector('#historyTarget');
const historyFailedOnly = document.querySelector('#historyFailedOnly');
tokenInput.value = state.token;
autoRefreshSelect.value = String(state.autoRefreshInterval);
setTheme(state.theme, false);

themeToggle.addEventListener('click', () => {
  setTheme(state.theme === 'dark' ? 'light' : 'dark', true);
});

document.querySelector('#saveToken').addEventListener('click', async () => {
  state.token = tokenInput.value.trim();
  localStorage.setItem('dockertree.token', state.token);
  await loadProjects();
  await loadLocalImages();
  await loadTemplates();
  if (state.activeView === 'overview') await loadStats({ silent: true });
});

document.querySelector('#scan').addEventListener('click', async () => {
  try {
    const projects = await scanInventory();
    await loadLocalImages({ silent: true });
    if (state.activeView === 'overview') await loadStats({ silent: true });
    showOperationResult({ output: `已扫描 ${projects.length} 个项目。` });
  } catch (err) {
    showOperationResult({ error: err.message });
  }
});

for (const button of document.querySelectorAll('#viewTabs [data-view]')) {
  button.addEventListener('click', () => setActiveView(button.dataset.view));
}

document.querySelector('#deployModeImage').addEventListener('click', () => setDeployMode('image'));
document.querySelector('#deployModeCompose').addEventListener('click', () => setDeployMode('compose'));
document.querySelector('#imageSearch').addEventListener('click', searchImages);
document.querySelector('#refreshLocalImages').addEventListener('click', loadLocalImages);
document.querySelector('#imagePreview').addEventListener('click', () => deployContainer(true));
document.querySelector('#imageDeploy').addEventListener('click', () => deployContainer(false));
document.querySelector('#composePreview').addEventListener('click', () => deployCompose(true));
document.querySelector('#composeDeploy').addEventListener('click', () => deployCompose(false));
document.querySelector('#imageQuery').addEventListener('input', syncDerivedContainerName);
for (const id of ['composeName', 'composePath', 'composeContent']) {
  document.querySelector(`#${id}`).addEventListener('input', () => {
    state.composePreview = null;
    document.querySelector('#composeDiff').classList.add('hidden');
  });
}
document.querySelector('#refreshStats').addEventListener('click', () => loadStats());
document.querySelector('#clearFilters').addEventListener('click', clearFilters);
document.querySelector('#refreshHistory').addEventListener('click', loadOperations);
historyTarget.addEventListener('change', loadOperations);
historyFailedOnly.addEventListener('change', loadOperations);
document.querySelector('#checkAllUpdates').addEventListener('click', checkAllUpdates);
document.querySelector('#saveAutomation').addEventListener('click', saveAutomationSettings);
document.querySelector('#testNotification').addEventListener('click', testNotification);
document.querySelector('#cleanupPreview').addEventListener('click', loadCleanupPreview);
document.querySelector('#executeCleanup').addEventListener('click', executeCleanup);
document.querySelector('#exportConfig').addEventListener('click', exportConfig);
document.querySelector('#restoreConfig').addEventListener('click', restoreConfig);
document.querySelector('#loadTemplate').addEventListener('click', applySelectedTemplate);
document.querySelector('#saveTemplate').addEventListener('click', saveTemplate);
document.querySelector('#deleteTemplate').addEventListener('click', deleteTemplate);
autoRefreshSelect.addEventListener('change', () => {
  state.autoRefreshInterval = Number(autoRefreshSelect.value) || 0;
  syncAutoRefresh();
});

for (const control of [globalSearch, statusFilter, healthFilter, typeFilter, tagFilter, favoriteOnly, sortBy]) {
  control.addEventListener('input', applyFilters);
  control.addEventListener('change', applyFilters);
}

document.addEventListener('visibilitychange', () => {
  if (!document.hidden && state.autoRefreshInterval > 0) {
    runAutoRefresh();
  }
});

function preferredTheme() {
  return window.matchMedia && window.matchMedia('(prefers-color-scheme: dark)').matches ? 'dark' : 'light';
}

function storedAutoRefreshInterval() {
  const value = Number(localStorage.getItem('dockertree.autoRefreshInterval'));
  return [0, 30, 60, 300].includes(value) ? value : 0;
}

function setTheme(theme, persist) {
  state.theme = theme === 'dark' ? 'dark' : 'light';
  document.documentElement.setAttribute('data-theme', state.theme);
  themeToggle.textContent = state.theme === 'dark' ? '浅色' : '深色';
  themeToggle.setAttribute('aria-label', state.theme === 'dark' ? '切换浅色模式' : '切换深色模式');
  themeToggle.setAttribute('title', state.theme === 'dark' ? '切换浅色模式' : '切换深色模式');
  if (persist) {
    localStorage.setItem('dockertree.theme', state.theme);
  }
}

function setDeployMode(mode) {
  const imageMode = mode === 'image';
  document.querySelector('#imageDeployForm').classList.toggle('hidden', !imageMode);
  document.querySelector('#composeDeployForm').classList.toggle('hidden', imageMode);
  document.querySelector('#deployModeImage').classList.toggle('active', imageMode);
  document.querySelector('#deployModeCompose').classList.toggle('active', !imageMode);
}

function setActiveView(view) {
  state.activeView = view;
  for (const button of document.querySelectorAll('#viewTabs [data-view]')) {
    button.classList.toggle('active', button.dataset.view === view);
  }
  for (const panel of document.querySelectorAll('[data-view-panel]')) {
    const active = panel.dataset.viewPanel === view;
    panel.classList.toggle('active', active);
    panel.classList.toggle('hidden', !active);
  }
  filterBar.classList.toggle('hidden', ['images', 'deploy', 'history', 'maintenance'].includes(view));
  if (view === 'overview' && state.token && !state.statsLoaded) {
    loadStats({ silent: true });
  }
  if (view === 'history' && state.token) loadOperations();
  if (view === 'maintenance' && state.token) loadAutomationSettings();
}

async function scanInventory() {
  if (state.scanInFlight) return state.projects;
  state.scanInFlight = true;
  try {
    const projects = await api('/api/scan', { method: 'POST' });
    state.projects = projects;
    reconcileSelectedProject();
    updateTagFilter();
    renderOverview();
    renderContainers();
    renderProjects();
    return projects;
  } finally {
    state.scanInFlight = false;
  }
}

function syncAutoRefresh() {
  if (state.autoRefreshTimer) {
    clearInterval(state.autoRefreshTimer);
    state.autoRefreshTimer = null;
  }
  localStorage.setItem('dockertree.autoRefreshInterval', String(state.autoRefreshInterval));
  if (state.autoRefreshInterval <= 0) return;
  state.autoRefreshTimer = setInterval(runAutoRefresh, state.autoRefreshInterval * 1000);
}

async function runAutoRefresh() {
  if (document.hidden || !state.token || state.scanInFlight) return;
  try {
    await scanInventory();
    if (state.activeView === 'overview') await loadStats({ silent: true });
  } catch (err) {
    showOperationResult({ error: `自动刷新失败：${err.message}` });
  }
}

async function api(path, options = {}) {
  const res = await fetch(path, {
    ...options,
    headers: {
      Authorization: `Bearer ${state.token}`,
      ...(options.headers || {}),
    },
  });
  if (!res.ok) {
    throw new Error(await res.text());
  }
  return res.json();
}

async function apiResult(path, options = {}) {
  try {
    return await api(path, options);
  } catch (err) {
    try {
      return JSON.parse(err.message);
    } catch (_) {
      throw err;
    }
  }
}

async function apiText(path, options = {}) {
  const res = await fetch(path, {
    ...options,
    headers: {
      Authorization: `Bearer ${state.token}`,
      ...(options.headers || {}),
    },
  });
  if (!res.ok) throw new Error(await res.text());
  return res.text();
}

async function loadProjects() {
  try {
    state.projects = await api('/api/projects');
    reconcileSelectedProject();
    updateTagFilter();
    updateHistoryTargets();
    renderOverview();
    renderContainers();
    if (state.selected) {
      selectProject(state.selected);
    } else {
      renderProjects();
    }
  } catch (err) {
    projectsEl.innerHTML = '';
    projectPaginationEl.innerHTML = '';
    const row = document.createElement('div');
    row.className = 'empty';
    row.textContent = `无法读取项目：${err.message}`;
    projectsEl.appendChild(row);
  }
}

function reconcileSelectedProject() {
  const selectedExists = state.selected && state.projects.some((project) => project.id === state.selected);
  if (!selectedExists) {
    state.selected = null;
  }
  const containerExists = state.selectedContainer && flattenContainers().some((item) => item.id === state.selectedContainer);
  if (!containerExists) {
    state.selectedContainer = null;
  }
}

async function loadLocalImages(options = {}) {
  const imagesEl = document.querySelector('#localImages');
  imagesEl.innerHTML = '';
  try {
    state.localImages = await api('/api/images/local');
    renderImagesColumn();
    renderOverview();
    if (!state.localImages.length) {
      return;
    }
    if (!options.silent) {
      showOperationResult({ output: `已加载 ${state.localImages.length} 个本地镜像。` });
    }
  } catch (err) {
    if (!options.silent) {
      showOperationResult({ error: err.message });
    }
  }
}

function renderImagesColumn() {
  const imagesEl = document.querySelector('#localImages');
  imagesEl.innerHTML = '';
  if (!state.localImages.length) {
    imagesEl.innerHTML = '<div class="empty">本地还没有可显示的镜像。</div>';
    imagePaginationEl.innerHTML = '';
    return;
  }
  const paged = pageItems(state.localImages, state.pagination.images);
  state.pagination.images = paged.currentPage;
  for (const image of paged.items) {
    const ref = imageRef(image);
    const row = document.createElement('div');
    row.className = 'image-row';
    row.innerHTML = '<strong></strong><span class="image-meta"></span><div class="row-actions"><button class="secondary" type="button" data-use>使用</button><button class="danger" type="button" data-delete>删除</button></div>';
    row.querySelector('strong').textContent = ref;
    row.querySelector('.image-meta').textContent = `${image.id || ''} ${image.size || ''} ${image.created || ''}`.trim();
    row.querySelector('[data-use]').addEventListener('click', () => {
      setActiveView('deploy');
      document.querySelector('#imageQuery').value = ref;
      document.querySelector('#containerName').dataset.touched = 'false';
      syncDerivedContainerName();
    });
    row.querySelector('[data-delete]').addEventListener('click', () => deleteImage(ref));
    imagesEl.appendChild(row);
  }
  renderPagination(imagePaginationEl, paged, (page) => {
    state.pagination.images = page;
    renderImagesColumn();
  });
}

function imageRef(image) {
  if (!image.repository || image.repository === '<none>') return image.id || '';
  if (!image.tag || image.tag === '<none>') return image.repository;
  return `${image.repository}:${image.tag}`;
}

async function loadStats(options = {}) {
  const statsStatus = document.querySelector('#statsStatus');
  if (!state.token) {
    statsStatus.textContent = '保存 Admin token 后读取资源快照';
    return;
  }
  if (!options.silent) statsStatus.textContent = '正在读取资源快照...';
  try {
    state.stats = await api('/api/containers/stats');
    state.statsLoaded = true;
    statsStatus.textContent = `已读取 ${state.stats.length} 个容器的资源数据`;
    renderOverview();
  } catch (err) {
    statsStatus.textContent = `资源快照失败：${err.message}`;
    if (!options.silent) showOperationResult({ error: err.message });
  }
}

function renderOverview() {
  const containers = flattenContainers();
  const running = containers.filter(isRunningContainer);
  const unhealthy = containers.filter(isUnhealthyContainer);
  document.querySelector('#summaryProjects').textContent = String(state.projects.length);
  document.querySelector('#summaryContainers').textContent = String(containers.length);
  document.querySelector('#summaryImages').textContent = String(state.localImages.length);
  document.querySelector('#summaryRunning').textContent = String(running.length);
  document.querySelector('#summaryStopped').textContent = String(containers.length - running.length);
  document.querySelector('#summaryUnhealthy').textContent = String(unhealthy.length);
  renderAttentionList();
  renderStatsTable();
}

function renderAttentionList() {
  const attentionList = document.querySelector('#attentionList');
  attentionList.innerHTML = '';
  const items = filteredContainers().filter((item) => isUnhealthyContainer(item) || !isRunningContainer(item));
  if (!items.length) {
    attentionList.innerHTML = '<div class="empty">当前筛选范围内没有需要处理的容器。</div>';
    return;
  }
  for (const item of items) {
    const row = document.createElement('div');
    row.className = 'attention-row';
    const action = isRunningContainer(item) ? 'restart' : 'start';
    row.innerHTML = `
      <div><strong class="name"></strong><div class="meta"></div></div>
      <div class="row-actions">
        <button class="secondary" type="button" data-logs>日志</button>
        <button type="button" data-action></button>
      </div>
    `;
    row.querySelector('.name').textContent = item.name;
    row.querySelector('.meta').textContent = `${item.projectName} · ${item.health || item.status || item.state || 'unknown'}`;
    row.querySelector('[data-logs]').addEventListener('click', () => openContainerLogs(item));
    row.querySelector('[data-action]').textContent = action === 'restart' ? '重启' : '启动';
    row.querySelector('[data-action]').addEventListener('click', () => containerLifecycle(item, action));
    attentionList.appendChild(row);
  }
}

function renderStatsTable() {
  const body = document.querySelector('#statsBody');
  body.innerHTML = '';
  const statsByID = new Map(state.stats.map((item) => [item.containerId, item]));
  const statsByName = new Map(state.stats.map((item) => [item.name, item]));
  const containers = filteredContainers();
  if (!containers.length) {
    body.innerHTML = '<tr><td colspan="6">当前筛选范围内没有容器。</td></tr>';
    return;
  }
  for (const container of containers) {
    const stats = statsByID.get(container.containerId) || statsByName.get(container.name);
    const row = document.createElement('tr');
    row.innerHTML = '<td></td><td></td><td></td><td></td><td></td><td></td>';
    row.children[0].textContent = container.name;
    row.children[1].textContent = stats ? `${Number(stats.cpuPercent || 0).toFixed(2)}%` : '-';
    row.children[2].textContent = stats ? `${stats.memoryUsage || '-'} (${Number(stats.memoryPercent || 0).toFixed(2)}%)` : '-';
    row.children[3].textContent = stats?.networkIO || '-';
    row.children[4].textContent = stats?.blockIO || '-';
    row.children[5].textContent = stats ? String(stats.pids || 0) : '-';
    body.appendChild(row);
  }
}

function applyFilters() {
  state.pagination.containers = 1;
  state.pagination.projects = 1;
  renderOverview();
  renderContainers();
  renderProjects();
}

function clearFilters() {
  globalSearch.value = '';
  statusFilter.value = 'all';
  healthFilter.value = 'all';
  typeFilter.value = 'all';
  tagFilter.value = 'all';
  favoriteOnly.checked = false;
  sortBy.value = 'name';
  applyFilters();
}

function updateTagFilter() {
  const selected = tagFilter.value;
  const tags = [...new Set(state.projects.flatMap((project) => project.tags || []))].sort((a, b) => a.localeCompare(b, 'zh-CN'));
  tagFilter.innerHTML = '<option value="all">全部标签</option>';
  for (const tag of tags) {
    const option = document.createElement('option');
    option.value = tag;
    option.textContent = tag;
    tagFilter.appendChild(option);
  }
  tagFilter.value = tags.includes(selected) ? selected : 'all';
}

function filteredProjects() {
  return state.projects.filter(projectMatchesFilters).sort(compareEntities);
}

function filteredContainers() {
  return flattenContainers().filter(containerMatchesFilters).sort(compareEntities);
}

function projectMatchesFilters(project) {
  const services = project.services || [];
  const searchable = [
    project.name,
    project.type,
    project.status,
    project.workingDir,
    ...(project.aliases || []),
    ...(project.tags || []),
    ...(project.configFiles || []),
    ...services.flatMap((service) => [service.name, service.image, ...(service.ports || [])]),
  ];
  return matchesCommonFilters({
    searchable,
    favorite: project.favorite,
    tags: project.tags,
    type: project.type,
    running: services.some(isRunningContainer),
    health: aggregateHealth(services),
  });
}

function containerMatchesFilters(container) {
  return matchesCommonFilters({
    searchable: [container.name, container.image, container.projectName, ...(container.aliases || []), ...(container.tags || []), ...(container.ports || [])],
    favorite: container.favorite,
    tags: container.tags,
    type: container.projectType,
    running: isRunningContainer(container),
    health: normalizedHealth(container.health),
  });
}

function matchesCommonFilters(entity) {
  const query = globalSearch.value.trim().toLowerCase();
  if (query && !entity.searchable.some((value) => String(value || '').toLowerCase().includes(query))) return false;
  if (favoriteOnly.checked && !entity.favorite) return false;
  if (typeFilter.value !== 'all' && entity.type !== typeFilter.value) return false;
  if (tagFilter.value !== 'all' && !(entity.tags || []).includes(tagFilter.value)) return false;
  if (statusFilter.value === 'running' && !entity.running) return false;
  if (statusFilter.value === 'stopped' && entity.running) return false;
  if (healthFilter.value !== 'all' && entity.health !== healthFilter.value) return false;
  return true;
}

function compareEntities(a, b) {
  if (Boolean(a.favorite) !== Boolean(b.favorite)) return a.favorite ? -1 : 1;
  if (sortBy.value === 'lastScanned') {
    return new Date(b.lastScanned || 0) - new Date(a.lastScanned || 0);
  }
  if (sortBy.value === 'status') {
    const difference = statusRank(a) - statusRank(b);
    if (difference) return difference;
  }
  return String(a.name || '').localeCompare(String(b.name || ''), 'zh-CN');
}

function statusRank(entity) {
  if (isUnhealthyContainer(entity)) return 0;
  if (!isRunningContainer(entity)) return 1;
  return 2;
}

function isRunningContainer(item) {
  const value = `${item.state || ''} ${item.status || ''}`.toLowerCase();
  return item.state === 'running' || value.includes(' up ') || value.startsWith('up ') || value.includes('running');
}

function isUnhealthyContainer(item) {
  return normalizedHealth(item.health) === 'unhealthy';
}

function normalizedHealth(value) {
  const health = String(value || '').toLowerCase();
  if (health.includes('unhealthy')) return 'unhealthy';
  if (health.includes('healthy')) return 'healthy';
  return 'none';
}

function aggregateHealth(services) {
  if (services.some(isUnhealthyContainer)) return 'unhealthy';
  if (services.some((service) => normalizedHealth(service.health) === 'healthy')) return 'healthy';
  return 'none';
}

function renderProjects() {
  projectsEl.innerHTML = '';
  const projects = filteredProjects();
  if (!projects.length) {
    projectsEl.innerHTML = `<div class="empty">${state.projects.length ? '没有符合筛选条件的项目。' : '还没有项目。点击扫描读取本机 Docker 状态。'}</div>`;
    projectPaginationEl.innerHTML = '';
    return;
  }
  const paged = pageItems(projects, state.pagination.projects);
  state.pagination.projects = paged.currentPage;
  for (const project of paged.items) {
    const entry = document.createElement('div');
    entry.className = 'project-entry';

    const btn = document.createElement('button');
    btn.className = `project-item ${state.selected === project.id ? 'active' : ''}`;
    btn.dataset.testid = 'project-item';
    btn.innerHTML = `
      <div class="row"><span class="name"></span><span class="status"></span></div>
      <div class="meta"></div>
      <div class="path"></div>
    `;
    btn.querySelector('.name').textContent = `${project.favorite ? '★ ' : ''}${project.name}`;
    btn.querySelector('.status').textContent = project.status || project.type;
    btn.querySelector('.meta').textContent = [project.type, `${(project.services || []).length} services`, ...(project.tags || [])].join(' · ');
    btn.querySelector('.path').textContent = [...(project.aliases || []), (project.configFiles || []).join(', ') || project.workingDir || 'standalone container'].filter(Boolean).join(' · ');
    btn.addEventListener('click', () => toggleProject(project.id));
    entry.appendChild(btn);
    if (state.selected === project.id) {
      entry.appendChild(renderProjectDetail(project));
    }
    projectsEl.appendChild(entry);
  }
  renderPagination(projectPaginationEl, paged, (page) => {
    state.pagination.projects = page;
    renderProjects();
  });
}

function renderContainers() {
  containersEl.innerHTML = '';
  const containers = filteredContainers();
  if (!containers.length) {
    containersEl.innerHTML = `<div class="empty">${flattenContainers().length ? '没有符合筛选条件的容器。' : '没有容器。点击扫描读取本机 Docker 状态。'}</div>`;
    containerPaginationEl.innerHTML = '';
    return;
  }
  const paged = pageItems(containers, state.pagination.containers);
  state.pagination.containers = paged.currentPage;
  for (const item of paged.items) {
    const entry = document.createElement('div');
    entry.className = 'container-entry';

    const btn = document.createElement('div');
    btn.className = `container-item ${state.selectedContainer === item.id ? 'active' : ''}`;
    btn.setAttribute('role', 'button');
    btn.tabIndex = 0;
    btn.innerHTML = `
      <div class="row">
        <span class="name"></span>
        <span class="status"></span>
      </div>
      <div class="meta"></div>
      <div class="path"></div>
    `;
    btn.querySelector('.name').textContent = `${item.favorite ? '★ ' : ''}${item.name}`;
    btn.querySelector('.status').textContent = item.health && item.health !== 'none' ? `${item.state || item.projectType} · ${item.health}` : item.state || item.projectType;
    btn.querySelector('.meta').textContent = [item.image, ...(item.tags || [])].filter(Boolean).join(' · ');
    btn.querySelector('.path').textContent = `${item.projectName} · ${(item.ports || []).join(', ') || 'no exposed ports'}`;
    btn.addEventListener('click', () => toggleContainer(item));
    btn.addEventListener('keydown', (event) => {
      if (event.target === btn && (event.key === 'Enter' || event.key === ' ')) {
        event.preventDefault();
        toggleContainer(item);
      }
    });
    entry.appendChild(btn);
    if (state.selectedContainer === item.id) {
      entry.appendChild(renderContainerDetail(item));
    }
    containersEl.appendChild(entry);
  }
  renderPagination(containerPaginationEl, paged, (page) => {
    state.pagination.containers = page;
    renderContainers();
  });
}

function openContainerProject(item) {
  setActiveView('projects');
  selectProject(item.projectId);
  renderContainers();
}

function toggleContainer(item) {
  if (state.selectedContainer === item.id) {
    state.selectedContainer = null;
    renderContainers();
    return;
  }
  state.selectedContainer = item.id;
  renderContainers();
}

function renderContainerDetail(item) {
  const detail = document.createElement('div');
  detail.className = 'container-detail detail-inner';
  detail.dataset.containerId = item.id;
  detail.innerHTML = `
    <div class="detail-head">
      <div>
        <h2></h2>
        <p class="path"></p>
      </div>
      <div class="actions">
        <button class="secondary" type="button" data-start>启动</button>
        <button class="secondary" type="button" data-stop>停止</button>
        <button class="secondary" type="button" data-restart>重启</button>
        <button class="secondary" type="button" data-logs>日志</button>
        <button class="secondary" type="button" data-inspect>检查</button>
        <button class="secondary" type="button" data-project>查看项目</button>
        <button class="danger" type="button" data-delete>删除容器</button>
      </div>
    </div>
    <table class="table">
      <tbody>
        <tr><th>容器 ID</th><td data-container-id></td></tr>
        <tr><th>镜像</th><td data-image></td></tr>
        <tr><th>状态</th><td data-status></td></tr>
        <tr><th>健康</th><td data-health></td></tr>
        <tr><th>端口</th><td data-ports></td></tr>
        <tr><th>挂载</th><td data-mounts></td></tr>
        <tr><th>标签</th><td data-tags></td></tr>
      </tbody>
    </table>
    <section class="inspect-panel hidden" data-inspect-panel>
      <table class="table"><tbody>
        <tr><th>创建时间</th><td data-inspect-created></td></tr>
        <tr><th>重启策略</th><td data-restart-policy></td></tr>
        <tr><th>网络</th><td data-inspect-networks></td></tr>
        <tr><th>挂载详情</th><td data-inspect-mounts></td></tr>
      </tbody></table>
    </section>
    <pre id="containerPlan">选择容器操作查看结果。</pre>
    ${logViewerMarkup(false)}
  `;
  detail.querySelector('h2').textContent = item.name;
  detail.querySelector('.path').textContent = `${item.projectName} · ${item.projectType}`;
  detail.querySelector('[data-container-id]').textContent = item.containerId || item.id;
  detail.querySelector('[data-image]').textContent = item.image || '-';
  detail.querySelector('[data-status]').textContent = item.status || item.state || '-';
  detail.querySelector('[data-health]').textContent = item.health || '无健康检查';
  detail.querySelector('[data-ports]').textContent = (item.ports || []).join(', ') || 'no exposed ports';
  detail.querySelector('[data-mounts]').textContent = (item.mounts || []).join(', ') || '-';
  detail.querySelector('[data-tags]').textContent = (item.tags || []).join(', ') || '-';
  detail.querySelector('[data-start]').addEventListener('click', () => containerLifecycle(item, 'start'));
  detail.querySelector('[data-stop]').addEventListener('click', () => containerLifecycle(item, 'stop'));
  detail.querySelector('[data-restart]').addEventListener('click', () => containerLifecycle(item, 'restart'));
  detail.querySelector('[data-logs]').addEventListener('click', () => containerLogs(item));
  detail.querySelector('[data-inspect]').addEventListener('click', () => containerInspect(item));
  detail.querySelector('[data-project]').addEventListener('click', () => openContainerProject(item));
  detail.querySelector('[data-delete]').addEventListener('click', () => deleteContainer(item));
  initializeLogViewer(detail.querySelector('[data-log-viewer]'), [], () => containerLogs(item));
  return detail;
}

function flattenContainers() {
  const items = [];
  for (const project of state.projects) {
    for (const svc of project.services || []) {
      items.push({
        id: svc.containerId || svc.name,
        containerId: svc.containerId || svc.name,
        projectId: project.id,
        projectName: project.name,
        projectType: project.type,
        name: svc.name,
        image: svc.image,
        state: svc.state,
        status: svc.status,
        health: svc.health,
        ports: svc.ports || [],
        mounts: svc.mounts || [],
        aliases: project.aliases || [],
        tags: project.tags || [],
        favorite: project.favorite,
        lastScanned: project.lastScanned,
      });
    }
  }
  return items;
}

function toggleProject(id) {
  if (state.selected === id) {
    state.selected = null;
    state.pagination.services = 1;
    renderProjects();
    return;
  }
  selectProject(id);
}

function selectProject(id) {
  const selectedChanged = state.selected !== id;
  state.selected = id;
  if (selectedChanged) {
    state.pagination.services = 1;
  }
  ensureProjectVisible(id);
  renderProjects();
  const project = state.projects.find((item) => item.id === id);
  if (!project) return;
}

function renderProjectDetail(project) {
  const deleteLabel = isStandaloneProject(project) ? '删除容器' : '删除项目';
  const detail = document.createElement('div');
  detail.className = 'project-detail detail-inner';
  detail.dataset.projectId = project.id;
  detail.innerHTML = `
    <div class="detail-head">
      <div>
        <h2></h2>
        <p class="path"></p>
        <div class="service-links" data-project-links></div>
      </div>
      <div class="actions">
        <button class="secondary" id="start">启动</button>
        <button class="secondary" id="stop">停止</button>
        <button class="secondary" id="restart">重启</button>
        <button class="secondary" id="logs">日志</button>
        <button class="secondary" id="checkUpdate">检查更新</button>
        <button class="secondary" id="preview">更新预览</button>
        <button class="danger" id="deploy">确认部署</button>
        <button class="danger" id="deleteProject"></button>
      </div>
    </div>
    <div class="metadata-editor">
      <label class="check-field"><input id="projectFavorite" type="checkbox" />收藏项目</label>
      <label>别名（逗号分隔）<input id="projectAliases" type="text" maxlength="512" /></label>
      <label>标签（逗号分隔）<input id="projectTags" type="text" maxlength="512" /></label>
      <button id="saveMetadata" class="secondary" type="button">保存分类信息</button>
      <div class="link-editor-block">
        <div class="section-heading"><strong>访问链接</strong><button id="addServiceLink" class="secondary" type="button">添加链接</button></div>
        <div id="projectLinks" class="link-editor"></div>
      </div>
    </div>
    <table class="table">
      <thead><tr><th>服务</th><th>镜像</th><th>状态</th><th>健康</th><th>端口</th><th>挂载</th></tr></thead>
      <tbody></tbody>
    </table>
    <div class="servicePagination pagination"></div>
    <pre id="plan">选择更新预览查看将执行的命令。</pre>
    ${logViewerMarkup(true)}
  `;
  detail.querySelector('h2').textContent = project.name;
  detail.querySelector('.path').textContent = (project.configFiles || []).join(', ') || project.workingDir || project.type;
  detail.querySelector('#deleteProject').textContent = deleteLabel;
  detail.querySelector('#projectFavorite').checked = Boolean(project.favorite);
  detail.querySelector('#projectAliases').value = (project.aliases || []).join(', ');
  detail.querySelector('#projectTags').value = (project.tags || []).join(', ');
  renderProjectLinks(detail.querySelector('[data-project-links]'), project.links || []);
  renderLinkEditor(detail.querySelector('#projectLinks'), project.links || []);
  const services = project.services || [];
  const pagedServices = pageItems(services, state.pagination.services);
  state.pagination.services = pagedServices.currentPage;
  const tbody = detail.querySelector('tbody');
  for (const svc of pagedServices.items) {
    const tr = document.createElement('tr');
    tr.innerHTML = '<td></td><td></td><td></td><td></td><td></td><td></td>';
    tr.children[0].textContent = svc.name;
    tr.children[1].textContent = svc.image;
    tr.children[2].textContent = svc.status || svc.state;
    tr.children[3].textContent = svc.health || '无健康检查';
    tr.children[4].textContent = (svc.ports || []).join(', ');
    tr.children[5].textContent = (svc.mounts || []).join(', ');
    tbody.appendChild(tr);
  }
  renderPagination(detail.querySelector('.servicePagination'), pagedServices, (page) => {
    state.pagination.services = page;
    renderProjects();
  });
  detail.querySelector('#preview').addEventListener('click', () => preview(project.id));
  detail.querySelector('#deploy').addEventListener('click', () => deploy(project.id));
  detail.querySelector('#start').addEventListener('click', () => lifecycle(project.id, 'start'));
  detail.querySelector('#stop').addEventListener('click', () => lifecycle(project.id, 'stop'));
  detail.querySelector('#restart').addEventListener('click', () => lifecycle(project.id, 'restart'));
  detail.querySelector('#logs').addEventListener('click', () => logs(project.id));
  detail.querySelector('#checkUpdate').addEventListener('click', () => checkProjectUpdate(project));
  detail.querySelector('#saveMetadata').addEventListener('click', () => saveProjectMetadata(project, detail));
  detail.querySelector('#addServiceLink').addEventListener('click', () => addServiceLink(detail.querySelector('#projectLinks')));
  detail.querySelector('#deleteProject').addEventListener('click', () => {
    if (isStandaloneProject(project)) {
      deleteContainer(standaloneDeleteTarget(project));
      return;
    }
    deleteProject(project);
  });
  initializeLogViewer(detail.querySelector('[data-log-viewer]'), services, () => logs(project.id));
  return detail;
}

function ensureProjectVisible(id) {
  const index = filteredProjects().findIndex((project) => project.id === id);
  if (index >= 0) {
    state.pagination.projects = Math.floor(index / state.pagination.pageSize) + 1;
  }
}

function pageItems(items, requestedPage) {
  const pageSize = state.pagination.pageSize;
  const totalPages = Math.max(1, Math.ceil(items.length / pageSize));
  const currentPage = Math.min(Math.max(Number(requestedPage) || 1, 1), totalPages);
  const start = (currentPage - 1) * pageSize;
  return {
    items: items.slice(start, start + pageSize),
    currentPage,
    totalPages,
    totalItems: items.length,
  };
}

function renderPagination(el, page, onPageChange) {
  el.innerHTML = '';
  if (page.totalItems <= state.pagination.pageSize) return;

  const prev = document.createElement('button');
  prev.className = 'secondary';
  prev.type = 'button';
  prev.textContent = '上一页';
  prev.disabled = page.currentPage === 1;
  prev.addEventListener('click', () => onPageChange(page.currentPage - 1));

  const status = document.createElement('span');
  status.textContent = `${page.currentPage} / ${page.totalPages} · 共 ${page.totalItems} 条`;

  const next = document.createElement('button');
  next.className = 'secondary';
  next.type = 'button';
  next.textContent = '下一页';
  next.disabled = page.currentPage === page.totalPages;
  next.addEventListener('click', () => onPageChange(page.currentPage + 1));

  el.append(prev, status, next);
}

function currentProjectPlan() {
  return document.querySelector('.project-detail #plan');
}

function currentContainerPlan() {
  return document.querySelector('.container-detail #containerPlan');
}

function isStandaloneProject(project) {
  return project.type === 'standalone';
}

function standaloneDeleteTarget(project) {
  const svc = (project.services || [])[0] || {};
  return {
    id: svc.containerId || svc.name || project.name,
    containerId: svc.containerId || svc.name || project.name,
    name: svc.name || project.name,
  };
}

function logViewerMarkup(includeService) {
  return `
    <section class="log-viewer hidden" data-log-viewer>
      <div class="log-toolbar">
        <label class="${includeService ? '' : 'hidden'}">服务
          <select class="logService" aria-label="日志服务"><option value="">全部服务</option></select>
        </label>
        <label>尾部行数
          <select class="logTail" aria-label="日志尾部行数">
            <option value="100">100</option>
            <option value="300" selected>300</option>
            <option value="1000">1000</option>
          </select>
        </label>
        <label class="check-field"><input class="logTimestamps" type="checkbox" />时间戳</label>
        <label>关键词<input class="logKeyword" type="search" placeholder="筛选已加载日志" /></label>
        <button class="secondary refreshLogs" type="button">刷新日志</button>
      </div>
      <pre class="logOutput">选择日志按钮读取内容。</pre>
    </section>
  `;
}

function initializeLogViewer(viewer, services, refreshLogs) {
  const serviceSelect = viewer.querySelector('.logService');
  for (const service of services) {
    const option = document.createElement('option');
    option.value = service.name;
    option.textContent = service.name;
    serviceSelect.appendChild(option);
  }
  viewer.querySelector('.refreshLogs').addEventListener('click', refreshLogs);
  viewer.querySelector('.logKeyword').addEventListener('input', () => renderLogText(viewer));
}

function logQuery(viewer) {
  const params = new URLSearchParams();
  params.set('tail', viewer.querySelector('.logTail').value);
  params.set('timestamps', String(viewer.querySelector('.logTimestamps').checked));
  const service = viewer.querySelector('.logService').value;
  if (service) params.set('service', service);
  return params.toString();
}

function setLogText(viewer, text) {
  viewer.rawLogText = text;
  renderLogText(viewer);
}

function renderLogText(viewer) {
  const keyword = viewer.querySelector('.logKeyword').value;
  viewer.querySelector('.logOutput').textContent = filterLogText(viewer.rawLogText || '', keyword);
}

function filterLogText(text, keyword) {
  const query = keyword.trim().toLowerCase();
  if (!query) return text;
  return text.split('\n').filter((line) => line.toLowerCase().includes(query)).join('\n');
}

function splitMetadataInput(value) {
  return value.split(/[，,\n]/).map((item) => item.trim()).filter(Boolean);
}

async function saveProjectMetadata(project, detail) {
  const payload = {
    favorite: detail.querySelector('#projectFavorite').checked,
    aliases: splitMetadataInput(detail.querySelector('#projectAliases').value),
    tags: splitMetadataInput(detail.querySelector('#projectTags').value),
    links: collectServiceLinks(detail.querySelector('#projectLinks')),
  };
  try {
    const updated = await api(`/api/projects/${encodeURIComponent(project.id)}/metadata`, {
      method: 'PATCH',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(payload),
    });
    state.projects = state.projects.map((item) => item.id === updated.id ? updated : item);
    updateTagFilter();
    renderOverview();
    renderContainers();
    renderProjects();
    showOperationResult({ output: `已保存 ${updated.name} 的收藏、标签和别名。` });
  } catch (err) {
    showOperationResult({ error: err.message });
  }
}

function ensureContainerVisible(id) {
  const index = filteredContainers().findIndex((container) => container.id === id);
  if (index >= 0) {
    state.pagination.containers = Math.floor(index / state.pagination.pageSize) + 1;
  }
}

function openContainerLogs(item) {
  state.selectedContainer = item.id;
  ensureContainerVisible(item.id);
  setActiveView('containers');
  renderContainers();
  containerLogs(item);
}

async function checkProjectUpdate(project) {
  const planEl = currentProjectPlan() || deployOutput;
  planEl.textContent = '正在检查远端镜像...';
  try {
    const check = await api(`/api/projects/${encodeURIComponent(project.id)}/actions/check-update`, { method: 'POST' });
    planEl.textContent = `${project.name}: ${updateStatusLabel(check.status)}\n\n$ ${check.command || '-'}\n${check.output || check.error || ''}`;
  } catch (err) {
    planEl.textContent = err.message;
  }
}

async function containerInspect(container) {
  const id = container.containerId || container.id;
  const panel = document.querySelector('.container-detail [data-inspect-panel]');
  if (!panel) return;
  panel.classList.remove('hidden');
  panel.querySelector('[data-inspect-created]').textContent = '正在读取...';
  try {
    const info = await api(`/api/containers/${encodeURIComponent(id)}/inspect`);
    panel.querySelector('[data-inspect-created]').textContent = info.created || '-';
    panel.querySelector('[data-restart-policy]').textContent = info.restartPolicy || 'no';
    panel.querySelector('[data-inspect-networks]').textContent = (info.networks || []).join(', ') || '-';
    const inspectMounts = (info.mounts || []).map((mount) => `${mount.type}: ${mount.source} -> ${mount.destination}${mount.rw ? '' : ' (ro)'}`);
    panel.querySelector('[data-inspect-mounts]').textContent = inspectMounts.join('\n') || '-';
  } catch (err) {
    panel.querySelector('[data-inspect-created]').textContent = err.message;
  }
}

function renderProjectLinks(container, links) {
  container.innerHTML = '';
  for (const link of links) {
    const anchor = document.createElement('a');
    anchor.href = link.url;
    anchor.target = '_blank';
    anchor.rel = 'noopener noreferrer';
    anchor.textContent = link.name;
    container.appendChild(anchor);
  }
}

function renderLinkEditor(container, links) {
  container.innerHTML = '';
  for (const link of links) addServiceLink(container, link);
}

function addServiceLink(container, link = {}) {
  const row = document.createElement('div');
  row.className = 'link-editor-row';
  row.innerHTML = '<input data-link-name placeholder="名称" /><input data-link-url placeholder="http://localhost:8080" /><button class="secondary" type="button" title="删除链接">×</button>';
  row.querySelector('[data-link-name]').value = link.name || '';
  row.querySelector('[data-link-url]').value = link.url || '';
  row.querySelector('button').addEventListener('click', () => row.remove());
  container.appendChild(row);
}

function collectServiceLinks(container) {
  return [...container.querySelectorAll('.link-editor-row')].map((row) => ({
    name: row.querySelector('[data-link-name]').value.trim(),
    url: row.querySelector('[data-link-url]').value.trim(),
  })).filter((link) => link.name && link.url);
}

async function preview(id) {
  const planEl = currentProjectPlan() || deployOutput;
  try {
    const plan = await api(`/api/projects/${encodeURIComponent(id)}/actions/preview-update`, { method: 'POST' });
    planEl.textContent = [
      `Project: ${plan.projectName}`,
      `Working dir: ${plan.workingDir || '-'}`,
      '',
      'Commands:',
      ...(plan.commands || []).map((cmd) => `  ${cmd}`),
      '',
      'Warnings:',
      ...((plan.warnings || []).length ? plan.warnings : ['  none']),
    ].join('\n');
  } catch (err) {
    planEl.textContent = err.message;
  }
}

async function deploy(id) {
  const planEl = currentProjectPlan() || deployOutput;
  if (!confirm('确认执行更新部署命令？')) return;
  try {
    const results = await apiResult(`/api/projects/${encodeURIComponent(id)}/actions/deploy`, { method: 'POST' });
    planEl.textContent = formatDeployResult(results);
    showOperationResult(results);
    await loadProjects();
  } catch (err) {
    planEl.textContent = err.message;
  }
}

async function deleteContainer(container) {
  const id = container.containerId || container.id;
  if (!confirm(`确认删除容器 ${container.name}？`)) return;
  try {
    const result = await apiResult(`/api/containers/${encodeURIComponent(id)}`, { method: 'DELETE' });
    showOperationResult(result);
    await loadProjects();
  } catch (err) {
    showOperationResult({ error: err.message });
  }
}

async function containerLifecycle(container, action) {
  const id = container.containerId || container.id;
  const planEl = currentContainerPlan() || deployOutput;
  try {
    const result = await apiResult(`/api/containers/${encodeURIComponent(id)}/actions/${action}`, { method: 'POST' });
    planEl.textContent = formatDeployResult(result);
    showOperationResult(result);
    await loadProjects();
  } catch (err) {
    planEl.textContent = err.message;
  }
}

async function containerLogs(container) {
  const id = container.containerId || container.id;
  const viewer = document.querySelector('.container-detail [data-log-viewer]');
  if (!viewer) return;
  viewer.classList.remove('hidden');
  viewer.querySelector('.logOutput').textContent = '正在读取日志...';
  try {
    const text = await apiText(`/api/containers/${encodeURIComponent(id)}/logs?${logQuery(viewer)}`);
    setLogText(viewer, text);
  } catch (err) {
    viewer.querySelector('.logOutput').textContent = err.message;
  }
}

async function deleteProject(project) {
  if (!confirm(`确认删除项目 ${project.name} 的容器和网络？不会删除数据卷和 compose 文件。`)) return;
  try {
    const result = await apiResult(`/api/projects/${encodeURIComponent(project.id)}`, { method: 'DELETE' });
    const planEl = currentProjectPlan() || deployOutput;
    planEl.textContent = formatDeployResult(result);
    showOperationResult(result);
    await loadProjects();
  } catch (err) {
    showOperationResult({ error: err.message });
  }
}

async function deleteImage(ref, force = false) {
  if (!force && !confirm(`确认删除镜像 ${ref}？`)) return;
  try {
    const result = await apiResult('/api/images', jsonDelete(force ? { ref, force: true } : { ref }));
    showOperationResult(result);
    if (!force && requiresForceImageDelete(result) && confirm(`镜像 ${ref} 正被容器使用。强制删除镜像标签？不会删除正在使用它的容器。`)) {
      await deleteImage(ref, true);
      return;
    }
    if (result.error) return;
    await loadLocalImages({ silent: true });
  } catch (err) {
    showOperationResult({ error: err.message });
  }
}

function requiresForceImageDelete(result) {
  const text = `${result.output || ''}\n${result.error || ''}`.toLowerCase();
  return text.includes('must be forced') || text.includes('is using its referenced image');
}

async function lifecycle(id, action) {
  const planEl = currentProjectPlan() || deployOutput;
  try {
    const result = await apiResult(`/api/projects/${encodeURIComponent(id)}/actions/${action}`, { method: 'POST' });
    planEl.textContent = formatDeployResult(result);
    showOperationResult(result);
    await loadProjects();
  } catch (err) {
    planEl.textContent = err.message;
  }
}

async function logs(id) {
  const viewer = document.querySelector('.project-detail [data-log-viewer]');
  if (!viewer) return;
  viewer.classList.remove('hidden');
  viewer.querySelector('.logOutput').textContent = '正在读取日志...';
  try {
    const text = await apiText(`/api/projects/${encodeURIComponent(id)}/logs?${logQuery(viewer)}`);
    setLogText(viewer, text);
  } catch (err) {
    viewer.querySelector('.logOutput').textContent = err.message;
  }
}

function updateHistoryTargets() {
  const selected = historyTarget.value;
  historyTarget.innerHTML = '<option value="">全部目标</option>';
  for (const project of state.projects) {
    const option = document.createElement('option');
    option.value = project.id;
    option.textContent = project.name;
    historyTarget.appendChild(option);
  }
  historyTarget.value = state.projects.some((project) => project.id === selected) ? selected : '';
}

async function loadOperations() {
  const params = new URLSearchParams({ limit: '200' });
  if (historyTarget.value) params.set('targetId', historyTarget.value);
  if (historyFailedOnly.checked) params.set('failed', 'true');
  try {
    state.operations = await api(`/api/operations?${params}`);
    renderOperations();
  } catch (err) {
    document.querySelector('#operationHistory').innerHTML = `<div class="empty"></div>`;
    document.querySelector('#operationHistory .empty').textContent = err.message;
  }
}

function renderOperations() {
  const list = document.querySelector('#operationHistory');
  list.innerHTML = '';
  if (!state.operations.length) {
    list.innerHTML = '<div class="empty">没有符合条件的操作记录。</div>';
    return;
  }
  for (const operation of state.operations) {
    const row = document.createElement('details');
    row.className = `history-row ${operation.success ? 'success' : 'failed'}`;
    row.innerHTML = `
      <summary><span data-title></span><span data-time></span></summary>
      <dl class="history-detail">
        <dt>命令</dt><dd data-command></dd><dt>输出</dt><dd><pre data-output></pre></dd>
      </dl>
    `;
    row.querySelector('[data-title]').textContent = `${operation.success ? '成功' : '失败'} · ${operation.action} · ${operation.targetName || operation.targetId}`;
    row.querySelector('[data-time]').textContent = new Date(operation.timestamp).toLocaleString();
    row.querySelector('[data-command]').textContent = operation.command || '-';
    row.querySelector('[data-output]').textContent = [operation.output, operation.error].filter(Boolean).join('\n') || '无输出';
    list.appendChild(row);
  }
}

async function checkAllUpdates() {
  const output = document.querySelector('#updateCheckResults');
  output.textContent = '正在检查镜像更新...';
  try {
    const checks = await api('/api/updates/check', { method: 'POST' });
    output.innerHTML = '';
    if (!checks.length) {
      output.textContent = '没有可检查的 Compose 项目。';
      return;
    }
    for (const check of checks) {
      const row = document.createElement('div');
      row.className = `update-check ${check.status}`;
      row.textContent = `${check.projectName}: ${updateStatusLabel(check.status)}${check.error ? ` · ${check.error}` : ''}`;
      output.appendChild(row);
    }
  } catch (err) {
    output.textContent = err.message;
  }
}

function updateStatusLabel(status) {
  if (status === 'available') return '发现可用更新';
  if (status === 'current') return '当前镜像已是最新';
  return '无法确定';
}

async function loadAutomationSettings() {
  try {
    const settings = await api('/api/settings/automation');
    document.querySelector('#updateCheckInterval').value = String(settings.updateCheckIntervalMinutes || 0);
    document.querySelector('#webhookType').value = settings.webhookType || 'generic';
    document.querySelector('#webhookURL').value = settings.webhookURL || '';
    document.querySelector('#notifyOnUpdates').checked = Boolean(settings.notifyOnUpdates);
  } catch (err) {
    showOperationResult({ error: err.message });
  }
}

async function saveAutomationSettings() {
  const payload = {
    updateCheckIntervalMinutes: Number(document.querySelector('#updateCheckInterval').value),
    webhookType: document.querySelector('#webhookType').value,
    webhookURL: document.querySelector('#webhookURL').value.trim(),
    notifyOnUpdates: document.querySelector('#notifyOnUpdates').checked,
  };
  try {
    await api('/api/settings/automation', jsonPatch(payload));
    showOperationResult({ output: '自动检查与通知设置已保存。' });
  } catch (err) {
    showOperationResult({ error: err.message });
  }
}

async function testNotification() {
  if (!confirm('确认向已配置的 Webhook/ntfy 地址发送测试通知？')) return;
  try {
    await api('/api/notifications/test', { method: 'POST' });
    showOperationResult({ output: '测试通知已发送。' });
  } catch (err) {
    showOperationResult({ error: err.message });
  }
}

async function loadCleanupPreview() {
  const list = document.querySelector('#cleanupCandidates');
  list.textContent = '正在生成清理预览...';
  try {
    state.cleanup = await api('/api/cleanup/preview');
    renderCleanupCandidates();
  } catch (err) {
    list.textContent = err.message;
  }
}

function renderCleanupCandidates() {
  const list = document.querySelector('#cleanupCandidates');
  list.innerHTML = '';
  const groups = [
    ['已停止容器', state.cleanup.containers || []],
    ['悬空镜像', state.cleanup.images || []],
    ['未使用网络', state.cleanup.networks || []],
  ];
  for (const [label, items] of groups) {
    const section = document.createElement('section');
    section.className = 'cleanup-group';
    const heading = document.createElement('h3');
    heading.textContent = `${label} (${items.length})`;
    section.appendChild(heading);
    for (const item of items) {
      const row = document.createElement('label');
      row.className = 'cleanup-row';
      row.innerHTML = '<input type="checkbox" /><span data-name></span><span class="meta" data-detail></span>';
      const checkbox = row.querySelector('input');
      checkbox.dataset.cleanupType = item.type;
      checkbox.dataset.cleanupId = item.id;
      row.querySelector('[data-name]').textContent = item.name || item.id;
      row.querySelector('[data-detail]').textContent = item.detail || '';
      section.appendChild(row);
    }
    list.appendChild(section);
  }
}

async function executeCleanup() {
  const items = [...document.querySelectorAll('#cleanupCandidates input:checked')].map((input) => ({ type: input.dataset.cleanupType, id: input.dataset.cleanupId }));
  if (!items.length) {
    showOperationResult({ error: '请先选择清理项。' });
    return;
  }
  if (!confirm(`确认执行所选清理（${items.length} 项）？不会删除数据卷。`)) return;
  try {
    const results = await apiResult('/api/cleanup', jsonPost({ items }));
    showOperationResult(results);
    if (results.error) return;
    await loadProjects();
    await loadLocalImages({ silent: true });
    await loadCleanupPreview();
  } catch (err) {
    showOperationResult({ error: err.message });
  }
}

async function exportConfig() {
  try {
    const res = await fetch('/api/config/export', { headers: { Authorization: `Bearer ${state.token}` } });
    if (!res.ok) throw new Error(await res.text());
    const blob = await res.blob();
    const link = document.createElement('a');
    link.href = URL.createObjectURL(blob);
    link.download = 'dockertree-backup.yaml';
    link.click();
    URL.revokeObjectURL(link.href);
  } catch (err) {
    showOperationResult({ error: err.message });
  }
}

async function restoreConfig() {
  const file = document.querySelector('#restoreFile').files[0];
  if (!file) {
    showOperationResult({ error: '请选择 YAML 备份文件。' });
    return;
  }
  if (!confirm('确认恢复这份备份？当前 Token、监听地址和 LAN 设置会保留。')) return;
  try {
    const result = await api('/api/config/restore', {
      method: 'POST', headers: { 'Content-Type': 'application/yaml' }, body: await file.text(),
    });
    showOperationResult({ output: result.restartRecommended ? '备份已恢复，建议重启 Dockertree。' : '备份已恢复。' });
    await loadProjects();
  } catch (err) {
    showOperationResult({ error: err.message });
  }
}

async function loadTemplates() {
  try {
    state.templates = await api('/api/templates');
    renderTemplateSelect();
  } catch (err) {
    state.templates = [];
    renderTemplateSelect();
  }
}

function renderTemplateSelect() {
  const select = document.querySelector('#templateSelect');
  const selected = select.value;
  select.innerHTML = '<option value="">选择个人模板</option>';
  for (const template of state.templates) {
    const option = document.createElement('option');
    option.value = template.id;
    option.textContent = `${template.name} · ${template.mode === 'container' ? '镜像' : 'Compose'}`;
    select.appendChild(option);
  }
  select.value = state.templates.some((template) => template.id === selected) ? selected : '';
}

function currentDeployMode() {
  return document.querySelector('#imageDeployForm').classList.contains('hidden') ? 'compose' : 'container';
}

function currentTemplatePayload() {
  if (currentDeployMode() === 'container') {
    return { mode: 'container', container: containerDeployPayload() };
  }
  return { mode: 'compose', compose: composeDeployPayload() };
}

async function saveTemplate() {
  const name = document.querySelector('#templateName').value.trim();
  if (!name) {
    showOperationResult({ error: '请输入模板名称。' });
    return;
  }
  const selected = document.querySelector('#templateSelect').value;
  const payload = { id: selected || undefined, name, ...currentTemplatePayload() };
  try {
    const template = await api('/api/templates', jsonPost(payload));
    await loadTemplates();
    document.querySelector('#templateSelect').value = template.id;
    showOperationResult({ output: `模板 ${template.name} 已保存。` });
  } catch (err) {
    showOperationResult({ error: err.message });
  }
}

function applySelectedTemplate() {
  const template = state.templates.find((item) => item.id === document.querySelector('#templateSelect').value);
  if (!template) return;
  document.querySelector('#templateName').value = template.name;
  if (template.mode === 'container') {
    setDeployMode('image');
    applyContainerTemplate(template.container || {});
  } else {
    setDeployMode('compose');
    applyComposeTemplate(template.compose || {});
  }
}

async function deleteTemplate() {
  const id = document.querySelector('#templateSelect').value;
  const template = state.templates.find((item) => item.id === id);
  if (!template || !confirm(`确认删除模板 ${template.name}？`)) return;
  try {
    await api(`/api/templates/${encodeURIComponent(id)}`, { method: 'DELETE' });
    document.querySelector('#templateName').value = '';
    await loadTemplates();
  } catch (err) {
    showOperationResult({ error: err.message });
  }
}

function applyContainerTemplate(template) {
  document.querySelector('#imageQuery').value = template.image || '';
  document.querySelector('#containerName').value = template.name || '';
  document.querySelector('#containerName').dataset.touched = template.name ? 'true' : 'false';
  document.querySelector('#containerPorts').value = (template.ports || []).join('\n');
  document.querySelector('#containerEnv').value = (template.env || []).join('\n');
  document.querySelector('#containerVolumes').value = (template.volumes || []).join('\n');
  document.querySelector('#containerNetwork').value = template.network || '';
  document.querySelector('#containerRestartPolicy').value = template.restartPolicy || '';
}

function applyComposeTemplate(template) {
  document.querySelector('#composeName').value = template.name || '';
  document.querySelector('#composePath').value = template.composePath || '';
  document.querySelector('#composeContent').value = template.composeContent || '';
  state.composePreview = null;
  document.querySelector('#composeDiff').classList.add('hidden');
}

async function searchImages() {
  const image = document.querySelector('#imageQuery').value.trim();
  const resultsEl = document.querySelector('#imageSearchResults');
  resultsEl.innerHTML = '';
  imageSearchPaginationEl.innerHTML = '';
  state.imageSearchResults = [];
  state.pagination.imageSearch = 1;
  if (!image) {
    deployOutput.textContent = '请输入镜像名称或关键词。';
    return;
  }
  try {
    const results = await api(`/api/images/search?q=${encodeURIComponent(image)}`);
    if (!results.length) {
      deployOutput.textContent = '没有搜索结果。';
      return;
    }
    state.imageSearchResults = results;
    renderImageSearchResults();
    deployOutput.textContent = `找到 ${results.length} 个镜像结果。`;
  } catch (err) {
    deployOutput.textContent = err.message;
  }
}

function renderImageSearchResults() {
  const resultsEl = document.querySelector('#imageSearchResults');
  resultsEl.innerHTML = '';
  const paged = pageItems(state.imageSearchResults, state.pagination.imageSearch);
  state.pagination.imageSearch = paged.currentPage;
  for (const item of paged.items) {
    const row = document.createElement('div');
    row.className = 'search-result';
    row.innerHTML = '<strong></strong><span></span><button class="secondary" type="button">使用</button>';
    row.querySelector('strong').textContent = item.name;
    row.querySelector('span').textContent = `${item.description || ''} ${item.official ? '[official]' : ''} stars:${item.stars || 0}`;
    row.querySelector('button').addEventListener('click', () => {
      document.querySelector('#imageQuery').value = item.name;
      syncDerivedContainerName();
    });
    resultsEl.appendChild(row);
  }
  renderPagination(imageSearchPaginationEl, paged, (page) => {
    state.pagination.imageSearch = page;
    renderImageSearchResults();
  });
}

async function deployContainer(previewOnly) {
  syncDerivedContainerName();
  const payload = containerDeployPayload();
  const path = previewOnly ? '/api/deploy/container/preview' : '/api/deploy/container';
  if (!previewOnly && !confirm('确认部署这个容器？')) return;
  try {
    const result = await apiResult(path, jsonPost(payload));
    deployOutput.textContent = formatDeployResult(result);
    if (!previewOnly) await loadProjects();
  } catch (err) {
    deployOutput.textContent = err.message;
  }
}

function splitLines(value) {
  return value.split('\n').map((item) => item.trim()).filter(Boolean);
}

function containerDeployPayload() {
  return {
    name: document.querySelector('#containerName').value.trim(),
    image: document.querySelector('#imageQuery').value.trim(),
    ports: splitLines(document.querySelector('#containerPorts').value),
    env: splitLines(document.querySelector('#containerEnv').value),
    volumes: splitLines(document.querySelector('#containerVolumes').value),
    network: document.querySelector('#containerNetwork').value.trim(),
    restartPolicy: document.querySelector('#containerRestartPolicy').value,
  };
}

function syncDerivedContainerName() {
  const nameInput = document.querySelector('#containerName');
  if (nameInput.dataset.touched === 'true') return;
  nameInput.value = deriveContainerName(document.querySelector('#imageQuery').value);
}

document.querySelector('#containerName').addEventListener('input', () => {
  document.querySelector('#containerName').dataset.touched = 'true';
});

function deriveContainerName(image) {
  let value = image.trim();
  if (value.includes('@')) value = value.split('@')[0];
  const parts = value.replace(/^\/+|\/+$/g, '').split('/');
  let base = parts[parts.length - 1] || '';
  if (base.includes(':')) base = base.split(':')[0];
  base = base.toLowerCase().replace(/[^a-z0-9.-]+/g, '-').replace(/^[-.]+|[-.]+$/g, '');
  return base || 'container';
}

async function deployCompose(previewOnly) {
  const payload = composeDeployPayload();
  try {
    const preview = await api('/api/deploy/compose/preview', jsonPost(payload));
    state.composePreview = preview;
    renderComposePreview(preview);
    deployOutput.textContent = formatDeployResult(preview.plan);
    if (previewOnly) return;
    const prompt = preview.overwrites
      ? '确认使用标准化后的内容覆盖当前 Compose 文件并部署？'
      : '确认写入标准化后的 Compose 文件并部署？';
    if (!confirm(prompt)) return;
    payload.expectedExistingHash = preview.existingHash || '';
    const result = await apiResult('/api/deploy/compose', jsonPost(payload));
    deployOutput.textContent = formatDeployResult(result);
    showOperationResult(result);
    if (!result.error) await loadProjects();
  } catch (err) {
    deployOutput.textContent = err.message;
  }
}

function composeDeployPayload() {
  return {
    name: document.querySelector('#composeName').value.trim(),
    composePath: document.querySelector('#composePath').value.trim(),
    composeContent: document.querySelector('#composeContent').value,
  };
}

function renderComposePreview(preview) {
  const composeDiff = document.querySelector('#composeDiff');
  composeDiff.classList.remove('hidden');
  document.querySelector('#existingContent').textContent = preview.existingContent || '新文件';
  document.querySelector('#normalizedContent').textContent = preview.normalizedContent || '';
}

function jsonPost(payload) {
  return {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(payload),
  };
}

function jsonPatch(payload) {
  return {
    method: 'PATCH',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(payload),
  };
}

function jsonDelete(payload) {
  return {
    method: 'DELETE',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(payload),
  };
}

function formatDeployResult(result) {
  if (Array.isArray(result)) {
    return result.map(formatCommandResult).join('\n\n');
  }
  if (result.commands) {
    const lines = ['Commands:', ...result.commands.map((cmd) => `  ${cmd}`)];
    if (result.warnings && result.warnings.length) {
      lines.push('', 'Warnings:', ...result.warnings.map((warning) => `  ${warning}`));
    }
    return lines.join('\n');
  }
  if (result.command) {
    return formatCommandResult(result);
  }
  return JSON.stringify(result, null, 2);
}

function formatCommandResult(result) {
  const lines = [`$ ${result.command}`];
  if (result.output) {
    lines.push(result.output);
  }
  if (result.exitCode) {
    lines.push(`exitCode: ${result.exitCode}`);
  }
  if (result.error) {
    lines.push(`error: ${result.error}`);
  }
  if (!result.output && !result.error) {
    lines.push('ok');
  }
  return lines.join('\n');
}

function showOperationResult(result) {
  operationOutput.textContent = formatDeployResult(result);
}

syncAutoRefresh();
renderOverview();

if (state.token) {
  loadProjects();
  loadLocalImages();
  loadStats({ silent: true });
  loadTemplates();
}
