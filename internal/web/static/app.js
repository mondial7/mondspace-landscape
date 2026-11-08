// Mondspace Landscape UI — zoomable C4-style views over the analysis API.
// Everything rendered from analysis data goes through textContent (never
// innerHTML): repository content is untrusted input.
(() => {
  'use strict';

  const KIND_LABEL = {
    person: 'Person', system: 'System', container: 'Container',
    component: 'Component', file: 'File', external: 'External system',
  };
  const LEVEL_LABEL = { '': 'Context', system: 'Containers', container: 'Components', component: 'Code' };
  const EDGE_KINDS = ['depends', 'http', 'data', 'uses', 'runtime'];
  const POLL_MS = 1000;

  // ---------- tiny DOM helpers ----------
  const $ = (sel) => document.querySelector(sel);
  function h(tag, attrs = {}, ...children) {
    const el = document.createElement(tag);
    for (const [k, v] of Object.entries(attrs)) {
      if (v == null || v === false) continue;
      if (k === 'class') el.className = v;
      else if (k.startsWith('on')) el.addEventListener(k.slice(2), v);
      else el.setAttribute(k, v === true ? '' : v);
    }
    for (const c of children.flat()) {
      if (c == null || c === false) continue;
      el.append(c instanceof Node ? c : document.createTextNode(String(c)));
    }
    return el;
  }

  // ---------- API ----------
  const api = {
    async json(url, opts) {
      const res = await fetch(url, opts);
      const body = await res.json().catch(() => ({}));
      if (!res.ok) throw Object.assign(new Error(body.error || res.statusText), { status: res.status });
      return body;
    },
    config: () => api.json('/api/config'),
    list: () => api.json('/api/analyses'),
    get: (id) => api.json(`/api/analyses/${encodeURIComponent(id)}`),
    view: (id, focus) => api.json(`/api/analyses/${encodeURIComponent(id)}/view?focus=${encodeURIComponent(focus || '')}`),
    graph: (id) => api.json(`/api/analyses/${encodeURIComponent(id)}/graph`),
    analyze: (source) => api.json('/api/analyses', {
      method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ source }),
    }),
  };

  // ---------- state ----------
  const state = {
    job: null,          // current analysis job
    focus: '',          // focused node id ('' = top level)
    view: null,         // last view payload
    graph: null,        // full graph (lazy, for search & details)
    selected: null,     // {type:'node'|'edge', id}
    rankDir: 'TB',
    labels: true,
    hiddenKinds: new Set(),
    showTests: false,
    pollTimer: null,
    viewVersion: '',
  };

  // ---------- routing (#/a/<id>/<focus>) ----------
  function route() {
    const m = location.hash.match(/^#\/a\/([^/]+)(?:\/(.*))?$/);
    return m ? { id: m[1], focus: m[2] ? decodeURIComponent(m[2]) : '' } : null;
  }
  function navigate(id, focus, replace) {
    const hash = `#/a/${id}` + (focus ? `/${encodeURIComponent(focus)}` : '');
    if (location.hash === hash) return;
    if (replace) history.replaceState(null, '', hash); else history.pushState(null, '', hash);
  }

  // ---------- theme-aware colors for cytoscape ----------
  function colors() {
    const cs = getComputedStyle(document.documentElement);
    const v = (n) => cs.getPropertyValue(n).trim();
    return {
      text: v('--text'), muted: v('--muted'), surface: v('--surface'), border: v('--border'),
      person: v('--n-person'), system: v('--n-system'), container: v('--n-container'),
      component: v('--n-component'), file: v('--n-file'), fileText: v('--n-file-text'),
      external: v('--n-external'), nodeText: v('--n-text'), accent: v('--accent'),
      depends: v('--e-depends'), http: v('--e-http'), data: v('--e-data'), uses: v('--e-uses'), runtime: v('--e-runtime'),
    };
  }

  function stylesheet(c) {
    return [
      { selector: 'node', style: {
        shape: 'round-rectangle', 'background-color': (n) => c[n.data('kind')] || c.container,
        width: 'data(w)', height: 'data(h)',
        label: 'data(label)', color: (n) => n.data('kind') === 'file' ? c.fileText : c.nodeText,
        'font-family': 'ui-sans-serif, -apple-system, Segoe UI, Inter, sans-serif',
        'font-size': 11, 'text-wrap': 'wrap', 'text-max-width': (n) => n.data('w') - 22,
        'text-valign': 'center', 'text-halign': 'center', 'line-height': 1.3,
        'border-width': 0, 'corner-radius': 10,
      } },
      { selector: 'node[kind = "person"]', style: { shape: 'round-rectangle', 'corner-radius': 28 } },
      { selector: 'node[kind = "external"]', style: { 'border-width': 1.5, 'border-style': 'dashed', 'border-color': c.surface } },
      { selector: 'node.context', style: { opacity: 0.78, 'border-width': 2, 'border-style': 'dashed', 'border-color': c.muted } },
      { selector: 'node.expandable', style: { 'border-width': 3, 'border-color': c.accent, 'border-style': 'double' } },
      { selector: 'node.context.expandable', style: { 'border-style': 'dashed', 'border-width': 2 } },
      { selector: 'node:selected', style: { 'overlay-color': c.accent, 'overlay-opacity': 0.18, 'overlay-padding': 6 } },
      { selector: 'node.match', style: { 'overlay-color': c.data, 'overlay-opacity': 0.35, 'overlay-padding': 8 } },
      { selector: 'node.faded, edge.faded', style: { opacity: 0.18 } },
      { selector: 'edge', style: {
        width: 'data(width)', 'curve-style': 'bezier', 'line-color': (e) => c[e.data('kind')] || c.depends,
        'target-arrow-color': (e) => c[e.data('kind')] || c.depends, 'target-arrow-shape': 'triangle', 'arrow-scale': 0.9,
        label: (e) => state.labels ? e.data('label') : '', 'font-size': 9.5, color: c.muted,
        'text-background-color': c.surface, 'text-background-opacity': 0.9, 'text-background-padding': 2,
        'text-background-shape': 'roundrectangle', 'text-wrap': 'wrap', 'text-max-width': 180,
        'text-rotation': 'none',
      } },
      { selector: 'edge[kind = "runtime"]', style: { 'line-style': 'dashed' } },
      { selector: 'edge[kind = "depends"]', style: { 'line-style': 'solid' } },
      { selector: 'edge:selected', style: { width: (e) => e.data('width') + 2, 'z-index': 10 } },
    ];
  }

  // ---------- cytoscape ----------
  const cy = cytoscape({
    container: $('#cy'), wheelSensitivity: 0.25, minZoom: 0.1, maxZoom: 3,
    boxSelectionEnabled: false, style: stylesheet(colors()),
  });
  matchMedia('(prefers-color-scheme: dark)').addEventListener('change', () => cy.style(stylesheet(colors())));

  function truncate(s, n) { return !s ? '' : (s.length > n ? s.slice(0, n - 1).trimEnd() + '…' : s); }

  function nodeLabel(n) {
    const kind = KIND_LABEL[n.kind] || n.kind;
    const meta = n.technology ? `[${kind}: ${truncate(n.technology, 38)}]` : `[${kind}]`;
    const lines = [n.name, meta];
    const desc = truncate(n.description, n.kind === 'file' ? 0 : 120);
    if (desc) lines.push('', desc);
    if (n.expandable) lines.push('', `⊕ ${n.children} inside`);
    return lines.join('\n');
  }

  function nodeSize(n) {
    const w = n.kind === 'file' ? 170 : n.kind === 'person' ? 180 : 230;
    const charsPerLine = Math.floor((w - 22) / 6.1);
    const desc = truncate(n.description, n.kind === 'file' ? 0 : 120);
    let lines = 2 + (n.name.length > charsPerLine ? 1 : 0);
    if (desc) lines += 1 + Math.ceil(desc.length / charsPerLine);
    if (n.expandable) lines += 2;
    return { w, h: Math.max(56, 22 + lines * 14.5) };
  }

  function edgeLabel(e) {
    const labels = e.labels || [];
    if (!labels.length) return '';
    const shown = labels.slice(0, 3).join('\n');
    return labels.length > 3 ? `${shown}\n+${labels.length - 3} more` : shown;
  }

  function primaryKind(kinds) {
    for (const k of ['http', 'data', 'runtime', 'uses', 'depends']) if (kinds.includes(k)) return k;
    return kinds[0] || 'depends';
  }

  function elements(view) {
    const isTest = (n) => (n.tags || []).includes('test');
    const visible = new Set((view.nodes || []).filter((n) => state.showTests || !isTest(n)).map((n) => n.id));
    const nodes = (view.nodes || []).filter((n) => visible.has(n.id)).map((n) => {
      const { w, h: ht } = nodeSize(n);
      return {
        group: 'nodes',
        data: { id: n.id, kind: n.kind, label: nodeLabel(n), w, h: ht, raw: n },
        classes: [n.context ? 'context' : '', n.expandable ? 'expandable' : ''].join(' '),
      };
    });
    const edges = (view.edges || [])
      .filter((e) => visible.has(e.from) && visible.has(e.to))
      .filter((e) => e.kinds.some((k) => !state.hiddenKinds.has(k)))
      .map((e) => ({
        group: 'edges',
        data: {
          id: `${e.from}→${e.to}`, source: e.from, target: e.to, kind: primaryKind(e.kinds),
          label: edgeLabel(e), width: Math.min(1.2 + Math.log2(e.weight + 1), 6), raw: e,
        },
      }));
    return [...nodes, ...edges];
  }

  const MAX_FIT_ZOOM = 1.2; // small views should not be blown up

  function layout(animate) {
    const l = cy.layout({
      name: 'dagre', rankDir: state.rankDir, nodeSep: 40, rankSep: 90, edgeSep: 16,
      ranker: 'network-simplex', animate, animationDuration: 250, fit: true, padding: 40,
    });
    l.one('layoutstop', () => {
      if (cy.zoom() > MAX_FIT_ZOOM) { cy.zoom(MAX_FIT_ZOOM); cy.center(); }
    });
    l.run();
  }

  // Render a view. When only descriptions changed (same node/edge ids),
  // update data in place so the user's camera and positions are kept.
  function render(view, { relayout = true } = {}) {
    state.view = view;
    const els = elements(view);
    const ids = els.map((e) => e.data.id).sort().join('|');
    $('#empty').hidden = true;
    if (!relayout && ids === state.viewVersion) {
      for (const el of els) cy.getElementById(el.data.id).data(el.data);
      refreshPanel();
      return;
    }
    state.viewVersion = ids;
    cy.elements().remove();
    cy.add(els);
    layout(false);
    renderCrumbs(view);
    refreshPanel();
  }

  function renderCrumbs(view) {
    const crumbs = $('#crumbs');
    crumbs.replaceChildren(
      h('li', {}, h('a', { onclick: () => zoomTo('') }, 'Landscape')),
      ...(view.breadcrumb || []).map((b) => h('li', {}, h('a', { onclick: () => zoomTo(b.id), title: b.name }, b.name))),
    );
    const focusKind = view.focus ? view.focus.kind : '';
    $('#level').textContent = LEVEL_LABEL[focusKind] || 'Detail';
    $('#up').disabled = !view.focus;
  }

  // ---------- navigation ----------
  async function zoomTo(focus, { push = true } = {}) {
    if (!state.job) return;
    try {
      const view = await api.view(state.job.id, focus);
      state.focus = focus;
      state.selected = null;
      if (push) navigate(state.job.id, focus);
      render(view);
    } catch (err) {
      showStatus(err.message, 'error');
    }
  }

  function zoomOut() {
    const crumbs = (state.view && state.view.breadcrumb) || [];
    if (!crumbs.length) return;
    zoomTo(crumbs.length > 1 ? crumbs[crumbs.length - 2].id : '');
  }

  cy.on('dbltap', 'node', (evt) => {
    const n = evt.target.data('raw');
    if (n.expandable) zoomTo(n.id);
    else if (n.context) zoomTo(n.parent || '');
  });
  cy.on('tap', 'node', (evt) => select({ type: 'node', id: evt.target.id() }));
  cy.on('tap', 'edge', (evt) => select({ type: 'edge', id: evt.target.id() }));
  cy.on('tap', (evt) => { if (evt.target === cy) select(null); });

  // ---------- selection & details panel ----------
  function select(sel) {
    state.selected = sel;
    cy.elements().removeClass('faded');
    if (sel) {
      const el = cy.getElementById(sel.id);
      const keep = sel.type === 'node' ? el.closedNeighborhood() : el.union(el.connectedNodes());
      cy.elements().not(keep).addClass('faded');
    }
    refreshPanel();
  }

  function refreshPanel() {
    const panel = $('#panel');
    const sel = state.selected;
    if (!sel || !cy.getElementById(sel.id).length) {
      panel.replaceChildren(summaryPanel());
      return;
    }
    const el = cy.getElementById(sel.id);
    panel.replaceChildren(sel.type === 'node' ? nodePanel(el.data('raw')) : edgePanel(el.data('raw')));
  }

  function nameOf(id) {
    const n = state.view && state.view.nodes.find((x) => x.id === id);
    return n ? n.name : id;
  }

  function summaryPanel() {
    const job = state.job;
    if (!job || !job.summary) {
      return h('div', { class: 'panel-empty' }, 'Select a box or an arrow to see what it does and how it connects.');
    }
    const s = job.summary;
    const langs = Object.entries(s.languages || {}).sort((a, b) => b[1] - a[1]).slice(0, 8);
    return h('div', {},
      h('span', { class: 'badge system' }, 'Analysis'),
      h('h2', {}, s.name),
      h('div', { class: 'tech mono' }, job.remote || job.dir || job.source),
      h('h3', {}, 'Size'),
      h('div', { class: 'metrics' },
        metric(s.files, 'files'), metric(s.lines.toLocaleString(), 'lines'), metric(s.containers, 'containers'),
        metric(s.components, 'components'), metric(s.externals, 'external'), metric(langs.length, 'languages')),
      h('h3', {}, 'Languages (files)'),
      h('div', { class: 'tags' }, langs.map(([l, n]) => h('span', { class: 'tag' }, `${l} ${n}`))),
      (s.warnings || []).length ? [h('h3', {}, 'Warnings'), h('ul', { class: 'list warnings' }, s.warnings.map((w) => h('li', {}, w)))] : null,
      h('p', { class: 'panel-empty' }, 'Select a box or an arrow for details. Double-click a box with ⊕ to zoom in.'),
    );
  }

  function metric(v, label) { return h('div', { class: 'metric' }, h('b', {}, v), h('span', {}, label)); }

  function nodePanel(n) {
    const details = n.details || {};
    const conns = (state.view ? state.view.edges : []).filter((e) => e.from === n.id || e.to === n.id);
    const m = n.metrics || {};
    return h('div', {},
      h('span', { class: `badge ${n.kind}` }, KIND_LABEL[n.kind] || n.kind),
      n.context ? h('span', { class: 'ai' }, 'outside this view') : null,
      h('h2', {}, n.name),
      n.technology ? h('div', { class: 'tech' }, n.technology) : null,
      n.description ? h('p', { class: 'desc' }, n.description, details.ai ? h('span', { class: 'ai', title: 'Written by a local model' }, `✦ ${details.ai}`) : null) : h('p', { class: 'desc panel-empty' }, 'No description.'),
      details.doc ? h('p', { class: 'doc' }, details.doc) : null,
      n.path ? h('div', { class: 'mono' }, n.path) : null,
      Object.keys(m).length ? [h('h3', {}, 'Metrics'), h('div', { class: 'metrics' }, Object.entries(m).map(([k, v]) => metric(v.toLocaleString(), k)))] : null,
      (n.tags || []).length ? [h('h3', {}, 'Tags'), h('div', { class: 'tags' }, n.tags.map((t) => h('span', { class: 'tag' }, t)))] : null,
      details.deploy ? [h('h3', {}, 'Deployment'), h('div', {}, details.deploy)] : null,
      details.routes ? [h('h3', {}, 'HTTP routes served'), h('ul', { class: 'list mono' }, details.routes.split('\n').map((r) => h('li', {}, r)))] : null,
      details.symbols ? [h('h3', {}, 'Exported symbols'), h('div', { class: 'mono' }, details.symbols)] : null,
      conns.length ? [h('h3', {}, 'Connections in this view'), h('ul', { class: 'list' }, conns.map((e) => connItem(e, n.id)))] : null,
      h('div', { class: 'actions' },
        n.expandable ? h('button', { class: 'primary', onclick: () => zoomTo(n.id) }, `Zoom in (${n.children})`) : null,
        n.context && n.parent !== undefined ? h('button', { onclick: () => zoomTo(n.parent || '') }, 'Go to its level') : null,
        h('button', { onclick: () => copy(referenceFor(n)) }, 'Copy reference'),
      ),
    );
  }

  function connItem(e, selfId) {
    const out = e.from === selfId;
    const other = out ? e.to : e.from;
    return h('li', { class: 'link', onclick: () => select({ type: 'edge', id: `${e.from}→${e.to}` }) },
      e.kinds.map((k) => h('span', { class: `conn-kind ${k}` }, k)),
      out ? `→ ${nameOf(other)}` : `← ${nameOf(other)}`,
      e.labels && e.labels.length ? h('span', { class: 'conn-label' }, truncate(e.labels.join(' · '), 160)) : null,
    );
  }

  function edgePanel(e) {
    return h('div', {},
      h('span', { class: 'badge' }, 'Relationship'),
      h('h2', {}, `${nameOf(e.from)} → ${nameOf(e.to)}`),
      h('div', { class: 'tags' }, e.kinds.map((k) => h('span', { class: `conn-kind ${k}` }, k))),
      h('h3', {}, 'Strength'),
      h('div', {}, `${e.weight} underlying relationship${e.weight === 1 ? '' : 's'}`),
      e.labels && e.labels.length ? [h('h3', {}, 'Details'), h('ul', { class: 'list mono' }, e.labels.map((l) => h('li', {}, l)))] : null,
      h('div', { class: 'actions' },
        h('button', { onclick: () => select({ type: 'node', id: e.from }) }, `Source: ${truncate(nameOf(e.from), 22)}`),
        h('button', { onclick: () => select({ type: 'node', id: e.to }) }, `Target: ${truncate(nameOf(e.to), 22)}`),
      ),
    );
  }

  function referenceFor(n) {
    const path = n.path ? ` (${n.path})` : '';
    return `${KIND_LABEL[n.kind] || n.kind} "${n.name}"${path}${n.description ? ' — ' + n.description : ''}`;
  }

  async function copy(text) {
    try { await navigator.clipboard.writeText(text); showStatus('Copied to clipboard', 'info', 1500); }
    catch { showStatus('Clipboard unavailable', 'error', 2000); }
  }

  // ---------- status ----------
  let statusTimer = null;
  function showStatus(text, type = 'info', ttl = 0, spinner = false) {
    const el = $('#status');
    clearTimeout(statusTimer);
    if (!text) { el.hidden = true; return; }
    el.className = `status ${type}`;
    el.replaceChildren(spinner ? h('span', { class: 'spinner' }) : '', text);
    el.hidden = false;
    if (ttl) statusTimer = setTimeout(() => { el.hidden = true; }, ttl);
  }

  function statusText(job) {
    switch (job.status) {
      case 'queued': case 'resolving': return job.remote || /github/.test(job.source) ? 'Fetching repository…' : 'Opening repository…';
      case 'analyzing': return 'Analyzing code…';
      case 'enriching': {
        const p = job.progress;
        return p ? `Writing descriptions with ${job.llm} — ${p.stage} ${p.done}/${p.total}` : `Writing descriptions with ${job.llm}…`;
      }
      default: return '';
    }
  }

  // ---------- job lifecycle ----------
  async function openJob(id, focus = '', { push = false } = {}) {
    clearTimeout(state.pollTimer);
    state.graph = null;
    state.viewVersion = '';
    try {
      state.job = await api.get(id);
    } catch (err) {
      showStatus(`Analysis not found (${err.message})`, 'error', 4000);
      return;
    }
    $('#source').value = state.job.remote || state.job.dir || state.job.source;
    if (push) navigate(id, focus); else navigate(id, focus, true);
    await poll(focus);
  }

  async function poll(focus) {
    const job = state.job = await api.get(state.job.id).catch(() => state.job);
    if (job.dir || job.remote) $('#source').value = job.remote || job.dir;
    if (job.status === 'failed') {
      showStatus(`Analysis failed: ${job.error}`, 'error');
      $('#analyze-btn').disabled = false;
      return;
    }
    const hasModel = job.status === 'enriching' || job.status === 'ready';
    if (hasModel) {
      const firstRender = !state.view || state.viewVersion === '';
      try {
        const view = await api.view(job.id, firstRender ? focus : state.focus);
        if (firstRender) state.focus = focus;
        render(view, { relayout: firstRender });
      } catch (err) {
        if (err.status === 404 && focus) { await zoomTo('', { push: false }); } else { showStatus(err.message, 'error'); }
      }
    }
    if (job.status === 'ready') {
      showStatus(job.llm ? `Ready · descriptions by ${job.llm}` : 'Ready', 'info', 2500);
      $('#analyze-btn').disabled = false;
      refreshPanel();
      loadHistory();
      return;
    }
    showStatus(statusText(job), 'info', 0, true);
    state.pollTimer = setTimeout(() => poll(focus), job.status === 'enriching' ? POLL_MS * 2 : POLL_MS);
  }

  $('#analyze-form').addEventListener('submit', async (ev) => {
    ev.preventDefault();
    const source = $('#source').value.trim();
    if (!source) { $('#source').focus(); return; }
    $('#analyze-btn').disabled = true;
    state.view = null;
    try {
      const job = await api.analyze(source);
      await openJob(job.id, '', { push: true });
    } catch (err) {
      showStatus(err.message, 'error', 5000);
      $('#analyze-btn').disabled = false;
    }
  });

  async function loadHistory() {
    const jobs = await api.list().catch(() => []);
    const sel = $('#history');
    sel.replaceChildren(h('option', { value: '' }, 'Recent…'),
      ...jobs.map((j) => h('option', { value: j.id }, `${j.summary ? j.summary.name : j.source} · ${j.status}`)));
  }
  $('#history').addEventListener('change', (ev) => {
    const id = ev.target.value;
    ev.target.value = '';
    if (id) { state.view = null; openJob(id, '', { push: true }); }
  });

  // ---------- search (whole graph) ----------
  let searchActive = -1;
  async function ensureGraph() {
    if (!state.graph && state.job) state.graph = await api.graph(state.job.id).catch(() => null);
    return state.graph;
  }
  $('#search').addEventListener('input', async (ev) => {
    const q = ev.target.value.trim().toLowerCase();
    const list = $('#search-results');
    if (!q) { list.hidden = true; return; }
    const g = await ensureGraph();
    if (!g) return;
    const hits = g.nodes.filter((n) => n.name.toLowerCase().includes(q) || (n.path || '').toLowerCase().includes(q)
      || (n.description || '').toLowerCase().includes(q)).slice(0, 30);
    searchActive = -1;
    list.replaceChildren(...hits.map((n) => h('li', { onclick: () => jumpTo(n) },
      h('span', {}, n.name), h('span', { class: 'k' }, `${KIND_LABEL[n.kind] || n.kind}${n.path ? ' · ' + n.path : ''}`))));
    list.hidden = hits.length === 0;
  });
  $('#search').addEventListener('keydown', (ev) => {
    const items = [...$('#search-results').children];
    if (!items.length) return;
    if (ev.key === 'ArrowDown' || ev.key === 'ArrowUp') {
      ev.preventDefault();
      searchActive = (searchActive + (ev.key === 'ArrowDown' ? 1 : -1) + items.length) % items.length;
      items.forEach((li, i) => li.classList.toggle('active', i === searchActive));
    } else if (ev.key === 'Enter') {
      ev.preventDefault();
      (items[searchActive] || items[0]).click();
    } else if (ev.key === 'Escape') {
      $('#search-results').hidden = true;
    }
  });
  document.addEventListener('click', (ev) => { if (!ev.target.closest('.search')) $('#search-results').hidden = true; });

  async function jumpTo(n) {
    $('#search-results').hidden = true;
    $('#search').value = '';
    await zoomTo(n.parent || '');
    const el = cy.getElementById(n.id);
    if (el.length) {
      select({ type: 'node', id: n.id });
      el.addClass('match');
      cy.animate({ center: { eles: el }, zoom: Math.max(cy.zoom(), 1) }, { duration: 250 });
      setTimeout(() => el.removeClass('match'), 1600);
    }
  }

  // ---------- toolbar ----------
  $('#up').addEventListener('click', zoomOut);
  $('#fit').addEventListener('click', () => cy.animate({ fit: { padding: 40 } }, { duration: 200 }));
  $('#dir').addEventListener('click', (ev) => {
    state.rankDir = state.rankDir === 'TB' ? 'LR' : 'TB';
    ev.target.textContent = state.rankDir === 'TB' ? '↓ TB' : '→ LR';
    layout(true);
  });
  $('#labels').addEventListener('click', (ev) => {
    state.labels = !state.labels;
    ev.target.setAttribute('aria-pressed', String(state.labels));
    cy.style(stylesheet(colors()));
  });
  document.querySelectorAll('.seg input').forEach((cb) => cb.addEventListener('change', () => {
    state.hiddenKinds = new Set([...document.querySelectorAll('.seg input[data-kind]')].filter((x) => !x.checked).map((x) => x.dataset.kind));
    state.showTests = $('#tests').checked;
    if (state.view) { state.viewVersion = ''; render(state.view); }
  }));
  $('#png').addEventListener('click', () => {
    if (!cy.elements().length) return;
    const bg = getComputedStyle(document.body).backgroundColor;
    const a = h('a', { href: cy.png({ full: true, scale: 2, bg }), download: `${(state.job && state.job.summary && state.job.summary.name) || 'landscape'}${state.focus ? '-' + state.focus.replace(/[^a-z0-9]+/gi, '_') : ''}.png` });
    a.click();
  });
  document.addEventListener('keydown', (ev) => {
    if (ev.target.closest('input, select, textarea')) return;
    if (ev.key === 'Backspace' || ev.key === 'Escape') { ev.preventDefault(); if (state.selected) select(null); else zoomOut(); }
    if (ev.key === 'f' || ev.key === 'F') cy.animate({ fit: { padding: 40 } }, { duration: 200 });
    if (ev.key === 'Enter' && state.selected && state.selected.type === 'node') {
      const n = cy.getElementById(state.selected.id).data('raw');
      if (n && n.expandable) zoomTo(n.id);
    }
    if (ev.key === '/') { ev.preventDefault(); $('#search').focus(); }
  });

  window.addEventListener('popstate', () => {
    const r = route();
    if (!r) return;
    if (state.job && r.id === state.job.id) zoomTo(r.focus, { push: false });
    else { state.view = null; openJob(r.id, r.focus); }
  });

  // ---------- boot ----------
  (async function boot() {
    const cfg = await api.config().catch(() => ({}));
    const pill = $('#llm');
    pill.textContent = cfg.llm ? `AI: ${cfg.llm}` : 'AI off';
    pill.classList.toggle('on', !!cfg.llm);
    pill.title = cfg.llm ? 'Descriptions are written by this local model' : 'No local model found (start Ollama or LM Studio); static analysis only';
    loadHistory();
    const r = route();
    if (r) openJob(r.id, r.focus);
    else if (cfg.initial) openJob(cfg.initial, '');
    else $('#source').focus();
  })();
})();
