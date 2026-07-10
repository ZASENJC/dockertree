const state = {
  token: localStorage.getItem('dockertree.token') || '',
  theme: localStorage.getItem('dockertree.theme') || preferredTheme(),
  projects: [],
  localImages: [],
  imageSearchResults: [],
  selected: null,
  selectedContainer: null,
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
tokenInput.value = state.token;
setTheme(state.theme, false);

themeToggle.addEventListener('click', () => {
  setTheme(state.theme === 'dark' ? 'light' : 'dark', true);
});

document.querySelector('#saveToken').addEventListener('click', async () => {
  state.token = tokenInput.value.trim();
  localStorage.setItem('dockertree.token', state.token);
  await loadProjects();
  await loadLocalImages();
});

document.querySelector('#scan').addEventListener('click', async () => {
  try {
    const projects = await api('/api/scan', { method: 'POST' });
    showOperationResult({ output: `已扫描 ${projects.length} 个项目。` });
    await loadProjects();
    await loadLocalImages();
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

function preferredTheme() {
  return window.matchMedia && window.matchMedia('(prefers-color-scheme: dark)').matches ? 'dark' : 'light';
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
  for (const button of document.querySelectorAll('#viewTabs [data-view]')) {
    button.classList.toggle('active', button.dataset.view === view);
  }
  for (const panel of document.querySelectorAll('[data-view-panel]')) {
    const active = panel.dataset.viewPanel === view;
    panel.classList.toggle('active', active);
    panel.classList.toggle('hidden', !active);
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

function renderProjects() {
  projectsEl.innerHTML = '';
  if (!state.projects.length) {
    projectsEl.innerHTML = '<div class="empty">还没有项目。点击扫描读取本机 Docker 状态。</div>';
    projectPaginationEl.innerHTML = '';
    return;
  }
  const paged = pageItems(state.projects, state.pagination.projects);
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
    btn.querySelector('.name').textContent = project.name;
    btn.querySelector('.status').textContent = project.status || project.type;
    btn.querySelector('.meta').textContent = `${project.type} · ${(project.services || []).length} services`;
    btn.querySelector('.path').textContent = (project.configFiles || []).join(', ') || project.workingDir || 'standalone container';
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
  const containers = flattenContainers();
  if (!containers.length) {
    containersEl.innerHTML = '<div class="empty">没有容器。点击扫描读取本机 Docker 状态。</div>';
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
        <div class="container-row-actions"><span class="status"></span><button class="secondary" type="button" data-details>详情</button></div>
      </div>
      <div class="meta"></div>
      <div class="path"></div>
    `;
    btn.querySelector('.name').textContent = item.name;
    btn.querySelector('.status').textContent = item.state || item.projectType;
    btn.querySelector('.meta').textContent = item.image;
    btn.querySelector('.path').textContent = `${item.projectName} · ${(item.ports || []).join(', ') || 'no exposed ports'}`;
    btn.querySelector('[data-details]').addEventListener('click', (event) => {
      event.stopPropagation();
      toggleContainer(item);
    });
    btn.addEventListener('click', () => openContainerProject(item));
    btn.addEventListener('keydown', (event) => {
      if (event.target === btn && (event.key === 'Enter' || event.key === ' ')) {
        event.preventDefault();
        openContainerProject(item);
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
        <button class="secondary" type="button" data-project>查看项目</button>
        <button class="danger" type="button" data-delete>删除容器</button>
      </div>
    </div>
    <table class="table">
      <tbody>
        <tr><th>容器 ID</th><td data-container-id></td></tr>
        <tr><th>镜像</th><td data-image></td></tr>
        <tr><th>状态</th><td data-status></td></tr>
        <tr><th>端口</th><td data-ports></td></tr>
      </tbody>
    </table>
    <pre id="containerPlan">选择容器操作查看结果。</pre>
  `;
  detail.querySelector('h2').textContent = item.name;
  detail.querySelector('.path').textContent = `${item.projectName} · ${item.projectType}`;
  detail.querySelector('[data-container-id]').textContent = item.containerId || item.id;
  detail.querySelector('[data-image]').textContent = item.image || '-';
  detail.querySelector('[data-status]').textContent = item.status || item.state || '-';
  detail.querySelector('[data-ports]').textContent = (item.ports || []).join(', ') || 'no exposed ports';
  detail.querySelector('[data-start]').addEventListener('click', () => containerLifecycle(item, 'start'));
  detail.querySelector('[data-stop]').addEventListener('click', () => containerLifecycle(item, 'stop'));
  detail.querySelector('[data-restart]').addEventListener('click', () => containerLifecycle(item, 'restart'));
  detail.querySelector('[data-logs]').addEventListener('click', () => containerLogs(item));
  detail.querySelector('[data-project]').addEventListener('click', () => openContainerProject(item));
  detail.querySelector('[data-delete]').addEventListener('click', () => deleteContainer(item));
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
        ports: svc.ports || [],
      });
    }
  }
  return items.sort((a, b) => a.name.localeCompare(b.name));
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
      </div>
      <div class="actions">
        <button class="secondary" id="start">启动</button>
        <button class="secondary" id="stop">停止</button>
        <button class="secondary" id="restart">重启</button>
        <button class="secondary" id="logs">日志</button>
        <button class="secondary" id="preview">更新预览</button>
        <button class="danger" id="deploy">确认部署</button>
        <button class="danger" id="deleteProject"></button>
      </div>
    </div>
    <table class="table">
      <thead><tr><th>服务</th><th>镜像</th><th>状态</th><th>端口</th></tr></thead>
      <tbody></tbody>
    </table>
    <div class="servicePagination pagination"></div>
    <pre id="plan">选择更新预览查看将执行的命令。</pre>
  `;
  detail.querySelector('h2').textContent = project.name;
  detail.querySelector('.path').textContent = (project.configFiles || []).join(', ') || project.workingDir || project.type;
  detail.querySelector('#deleteProject').textContent = deleteLabel;
  const services = project.services || [];
  const pagedServices = pageItems(services, state.pagination.services);
  state.pagination.services = pagedServices.currentPage;
  const tbody = detail.querySelector('tbody');
  for (const svc of pagedServices.items) {
    const tr = document.createElement('tr');
    tr.innerHTML = '<td></td><td></td><td></td><td></td>';
    tr.children[0].textContent = svc.name;
    tr.children[1].textContent = svc.image;
    tr.children[2].textContent = svc.status || svc.state;
    tr.children[3].textContent = (svc.ports || []).join(', ');
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
  detail.querySelector('#deleteProject').addEventListener('click', () => {
    if (isStandaloneProject(project)) {
      deleteContainer(standaloneDeleteTarget(project));
      return;
    }
    deleteProject(project);
  });
  return detail;
}

function ensureProjectVisible(id) {
  const index = state.projects.findIndex((project) => project.id === id);
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
  const planEl = currentContainerPlan() || deployOutput;
  try {
    const res = await fetch(`/api/containers/${encodeURIComponent(id)}/logs`, {
      headers: { Authorization: `Bearer ${state.token}` },
    });
    if (!res.ok) throw new Error(await res.text());
    planEl.textContent = await res.text();
  } catch (err) {
    planEl.textContent = err.message;
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
  const planEl = currentProjectPlan() || deployOutput;
  try {
    const res = await fetch(`/api/projects/${encodeURIComponent(id)}/logs`, {
      headers: { Authorization: `Bearer ${state.token}` },
    });
    if (!res.ok) throw new Error(await res.text());
    planEl.textContent = await res.text();
  } catch (err) {
    planEl.textContent = err.message;
  }
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
  const payload = {
    name: document.querySelector('#containerName').value.trim(),
    image: document.querySelector('#imageQuery').value.trim(),
  };
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
  const payload = {
    name: document.querySelector('#composeName').value.trim(),
    composePath: document.querySelector('#composePath').value.trim(),
    composeContent: document.querySelector('#composeContent').value,
  };
  const path = previewOnly ? '/api/deploy/compose/preview' : '/api/deploy/compose';
  if (!previewOnly && !confirm('确认写入 Compose 文件并部署？')) return;
  try {
    const result = await apiResult(path, jsonPost(payload));
    deployOutput.textContent = formatDeployResult(result);
    if (!previewOnly) await loadProjects();
  } catch (err) {
    deployOutput.textContent = err.message;
  }
}

function jsonPost(payload) {
  return {
    method: 'POST',
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

if (state.token) {
  loadProjects();
  loadLocalImages();
}
