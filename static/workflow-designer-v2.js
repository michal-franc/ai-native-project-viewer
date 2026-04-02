(function () {
  window.__workflowDesignerV2 = true;

  const INITIAL_WORKFLOW = JSON.parse(document.getElementById('workflow-json').textContent);
  const INITIAL_YAML = document.getElementById('workflow-yaml').textContent.trim();
  const STORAGE_KEY = 'workflow-designer:' + location.pathname;
  const SAVE_URL = location.pathname;
  const DATA_URL = location.pathname.replace(/\/workflow-designer$/, '/workflow-designer/data');

  const NODE_TYPES = [
    { type: 'status', icon: 'S', color: '#2563eb', title: 'Status', desc: 'Lifecycle status exported under `statuses:`.' },
    { type: 'validate', icon: 'V', color: '#d97706', title: 'Validate', desc: 'Validation rule executed during a transition.' },
    { type: 'append_section', icon: 'A', color: '#059669', title: 'Append Section', desc: 'Create or reuse a section and append content.' },
    { type: 'inject_prompt', icon: 'P', color: '#ea580c', title: 'Inject Prompt', desc: 'Transition guidance shown to the next agent.' },
    { type: 'approval', icon: 'H', color: '#db2777', title: 'Human Approval', desc: 'Require human approval before transition completion.' },
    { type: 'set_fields', icon: 'F', color: '#7c3aed', title: 'Set Fields', desc: 'Update assignee, priority, approval, or other frontmatter.' },
  ];

  const STARTERS = [
    { key: 'design-lifecycle', title: 'Workflow Skeleton', desc: 'Insert an idea-to-done status spine.' },
    { key: 'qa-lifecycle', title: 'QA Spine', desc: 'Insert backlog through done with testing stages.' },
  ];

  const VALIDATION_RULES = [
    { value: 'body_not_empty', label: 'Body Not Empty', description: 'Blocks the transition until the issue body has some content.' },
    { value: 'has_checkboxes', label: 'Has Checkboxes', description: 'Requires at least one checkbox anywhere in the issue body.' },
    { value: 'has_assignee', label: 'Has Assignee', description: 'Requires the issue to be claimed before the transition can continue.' },
    { value: 'all_checkboxes_checked', label: 'All Checkboxes Checked', description: 'Requires every checkbox in the issue body to be checked.' },
    { value: 'section_checkboxes_checked', label: 'Section Checkboxes Checked', description: 'Requires all checkboxes in one named section to be checked.', argLabel: 'Section Name', argPlaceholder: 'Implementation' },
    { value: 'has_test_plan', label: 'Has Test Plan', description: 'Requires a `## Test Plan` section with both `### Automated` and `### Manual` subsections.' },
    { value: 'has_comment_prefix', label: 'Has Comment Prefix', description: 'Requires at least one comment starting with a specific prefix.', argLabel: 'Comment Prefix', argPlaceholder: 'tests:' },
    { value: 'approved_for', label: 'Approved For', description: 'Requires the issue frontmatter approval field to match a target status.', argLabel: 'Approved Status', argPlaceholder: 'backlog' },
  ];

  const SET_FIELD_OPTIONS = [
    { value: 'assignee', label: 'Assignee', description: 'Assign or clear the issue assignee.' },
    { value: 'approved_for', label: 'Approved For', description: 'Update the human approval target status.' },
    { value: 'priority', label: 'Priority', description: 'Set or clear the issue priority.' },
    { value: 'status', label: 'Status', description: 'Override the resulting issue status.' },
  ];

  let state = {
    nodes: [],
    edges: [],
    systems: {},
    selectedId: null,
    selectedEdgeId: null,
    connectFrom: null,
    drag: null,
    connectDrag: null,
    pan: null,
    zoom: 1,
  };

  let exportState = { title: '', text: '', filename: '', mode: 'export' };
  let debugEvents = [];

  function makeId(prefix) {
    return prefix + '-' + Math.random().toString(36).slice(2, 9);
  }

  function logEvent(kind, message) {
    debugEvents.unshift({
      kind,
      message,
      at: new Date().toISOString(),
    });
    debugEvents = debugEvents.slice(0, 8);
  }

  function countIndent(line) {
    const match = line.match(/^ */);
    return match ? match[0].length : 0;
  }

  function yamlQuote(text) {
    return '"' + String(text ?? '').replace(/\\/g, '\\\\').replace(/"/g, '\\"') + '"';
  }

  function unquoteYAML(value) {
    const trimmed = String(value ?? '').trim();
    if ((trimmed.startsWith('"') && trimmed.endsWith('"')) || (trimmed.startsWith("'") && trimmed.endsWith("'"))) {
      return trimmed.slice(1, -1).replace(/\\"/g, '"').replace(/\\\\/g, '\\');
    }
    return trimmed;
  }

  function escapeHTML(value) {
    return String(value ?? '')
      .replace(/&/g, '&amp;')
      .replace(/</g, '&lt;')
      .replace(/>/g, '&gt;')
      .replace(/"/g, '&quot;');
  }

  function escapeAttr(value) {
    return escapeHTML(value).replace(/'/g, '&#39;');
  }

  function typeMeta(type) {
    return NODE_TYPES.find(item => item.type === type) || NODE_TYPES[0];
  }

  function colorForType(type) {
    return typeMeta(type).color;
  }

  function normalizeStatus(raw) {
    return {
      name: String(raw?.name ?? raw?.Name ?? '').trim(),
      description: String(raw?.description ?? raw?.Description ?? '').trim(),
    };
  }

  function normalizeAction(raw) {
    return {
      type: String(raw?.type ?? raw?.Type ?? '').trim(),
      rule: String(raw?.rule ?? raw?.Rule ?? '').trim(),
      status: String(raw?.status ?? raw?.Status ?? '').trim(),
      title: String(raw?.title ?? raw?.Title ?? '').trim(),
      body: String(raw?.body ?? raw?.Body ?? '').replace(/\r/g, '').replace(/\n+$/, ''),
      prompt: String(raw?.prompt ?? raw?.Prompt ?? '').replace(/\r/g, '').replace(/\n+$/, ''),
      field: String(raw?.field ?? raw?.Field ?? '').trim(),
      value: String(raw?.value ?? raw?.Value ?? '').trim(),
    };
  }

  function parseValidationRule(rule) {
    const normalized = String(rule || '').trim();
    const idx = normalized.indexOf(': ');
    if (idx === -1) {
      return { type: normalized, arg: '' };
    }
    return { type: normalized.slice(0, idx), arg: normalized.slice(idx + 2) };
  }

  function composeValidationRule(type, arg) {
    const trimmedType = String(type || '').trim();
    const trimmedArg = String(arg || '').trim();
    if (!trimmedType) return '';
    if (trimmedType === 'section_checkboxes_checked' || trimmedType === 'has_comment_prefix' || trimmedType === 'approved_for') {
      return trimmedArg ? `${trimmedType}: ${trimmedArg}` : trimmedType;
    }
    return trimmedType;
  }

  function validationRuleMeta(rule) {
    const parsed = parseValidationRule(rule);
    return VALIDATION_RULES.find(item => item.value === parsed.type) || null;
  }

  function validationRuleSummary(rule) {
    const parsed = parseValidationRule(rule);
    const meta = validationRuleMeta(rule);
    if (!meta) return rule || 'Validation rule';
    return parsed.arg ? `${meta.label}: ${parsed.arg}` : meta.label;
  }

  function setFieldMeta(field) {
    return SET_FIELD_OPTIONS.find(item => item.value === String(field || '').trim()) || null;
  }

  function normalizeTransition(raw) {
    return {
      from: String(raw?.from ?? raw?.From ?? '').trim(),
      to: String(raw?.to ?? raw?.To ?? '').trim(),
      actions: (raw?.actions ?? raw?.Actions ?? []).map(normalizeAction).filter(action => action.type),
    };
  }

  function normalizeSystems(raw) {
    const out = {};
    const source = raw || {};
    Object.keys(source).forEach(name => {
      const overlay = source[name] || {};
      out[name] = {
        statuses: (overlay.statuses ?? overlay.Statuses ?? []).map(normalizeStatus).filter(status => status.name),
        transitions: (overlay.transitions ?? overlay.Transitions ?? []).map(normalizeTransition).filter(transition => transition.from && transition.to),
      };
    });
    return out;
  }

  function normalizeWorkflow(raw) {
    return {
      statuses: (raw?.statuses ?? raw?.Statuses ?? []).map(normalizeStatus).filter(status => status.name),
      transitions: (raw?.transitions ?? raw?.Transitions ?? []).map(normalizeTransition).filter(transition => transition.from && transition.to),
      systems: normalizeSystems(raw?.systems ?? raw?.Systems),
    };
  }

  function actionNodeType(actionType) {
    return actionType === 'require_human_approval' ? 'approval' : actionType;
  }

  function normalizeNode(raw) {
    const node = {
      id: raw?.id || makeId(raw?.type || 'node'),
      type: String(raw?.type || 'status'),
      x: Number(raw?.x) || 120,
      y: Number(raw?.y) || 120,
      title: String(raw?.title || '').trim(),
      description: String(raw?.description || ''),
      rule: String(raw?.rule || ''),
      status: String(raw?.status || ''),
      titleText: String(raw?.titleText || ''),
      body: String(raw?.body || ''),
      prompt: String(raw?.prompt || ''),
      field: String(raw?.field || ''),
      value: String(raw?.value || ''),
    };
    if (!node.title) {
      node.title = node.type === 'status' ? 'new-status' : typeMeta(node.type).title;
    }
    return node;
  }

  function normalizeEdge(raw) {
    return {
      id: raw?.id || makeId('edge'),
      from: String(raw?.from || ''),
      to: String(raw?.to || ''),
    };
  }

  function actionFromNode(node) {
    switch (node.type) {
      case 'validate':
        return normalizeAction({ type: 'validate', rule: node.rule });
      case 'append_section':
        return normalizeAction({ type: 'append_section', title: node.titleText, body: node.body });
      case 'inject_prompt':
        return normalizeAction({ type: 'inject_prompt', prompt: node.prompt });
      case 'approval':
        return normalizeAction({ type: 'require_human_approval', status: node.status });
      case 'set_fields':
        return normalizeAction({ type: 'set_fields', field: node.field, value: node.value });
      default:
        return normalizeAction({ type: '' });
    }
  }

  function applyActionToNode(node, action) {
    const normalized = normalizeAction(action);
    node.rule = normalized.rule;
    node.status = normalized.status;
    node.titleText = normalized.title;
    node.body = normalized.body;
    node.prompt = normalized.prompt;
    node.field = normalized.field;
    node.value = normalized.value;
    return node;
  }

  function nodeSummary(node) {
    switch (node.type) {
      case 'status':
        return node.description || 'Workflow lifecycle status';
      case 'validate':
        return validationRuleSummary(node.rule);
      case 'append_section':
        return node.titleText ? `Append ${node.titleText}` : 'Append or create a section';
      case 'inject_prompt':
        return node.prompt || 'Transition guidance';
      case 'approval':
        return node.status ? `Requires approval for ${node.status}` : 'Requires human approval';
      case 'set_fields':
        return node.field ? `${(setFieldMeta(node.field)?.label || node.field)} = ${node.value || '""'}` : 'Set issue frontmatter';
      default:
        return 'Workflow node';
    }
  }

  function statusNodesInOrder() {
    return state.nodes.filter(node => node.type === 'status').slice().sort((a, b) => (a.x - b.x) || (a.y - b.y));
  }

  function workflowToDraft(workflow) {
    const normalized = normalizeWorkflow(workflow);
    const nodes = [];
    const edges = [];
    const byName = new Map();

    normalized.statuses.forEach((status, index) => {
      const node = normalizeNode({
        id: makeId('status'),
        type: 'status',
        x: 120 + index * 280,
        y: 150 + (index % 2) * 12,
        title: status.name,
        description: status.description,
      });
      nodes.push(node);
      byName.set(status.name, node);
    });

    normalized.transitions.forEach((transition, transitionIndex) => {
      const fromNode = byName.get(transition.from);
      const toNode = byName.get(transition.to);
      if (!fromNode || !toNode) return;

      let previous = fromNode;
      const span = Math.max(180, toNode.x - fromNode.x);
      const count = Math.max(transition.actions.length, 1);
      transition.actions.forEach((action, actionIndex) => {
        const node = applyActionToNode(normalizeNode({
          id: makeId(actionNodeType(action.type)),
          type: actionNodeType(action.type),
          x: fromNode.x + ((actionIndex + 1) * span) / (count + 1),
          y: 360 + (transitionIndex % 3) * 110,
        }), action);
        nodes.push(node);
        edges.push(normalizeEdge({ id: makeId('edge'), from: previous.id, to: node.id }));
        previous = node;
      });
      edges.push(normalizeEdge({ id: makeId('edge'), from: previous.id, to: toNode.id }));
    });

    return { nodes, edges, systems: normalized.systems };
  }

  function loadState() {
    const initial = workflowToDraft(INITIAL_WORKFLOW);
    state.nodes = initial.nodes;
    state.edges = initial.edges;
    state.systems = initial.systems;
    state.zoom = 1;
    state.selectedId = state.nodes[0]?.id || null;
    state.selectedEdgeId = null;
    state.connectFrom = null;
    state.connectDrag = null;
    state.pan = null;

    try {
      const saved = localStorage.getItem(STORAGE_KEY);
      if (!saved) {
        logEvent('load', 'Loaded workflow from server snapshot');
        return;
      }
      const parsed = JSON.parse(saved);
      if (parsed.version !== 3) {
        logEvent('warn', `Ignored local draft with unsupported version ${parsed.version ?? 'unknown'}`);
        return;
      }
      state.nodes = Array.isArray(parsed.nodes) ? parsed.nodes.map(normalizeNode) : state.nodes;
      state.edges = Array.isArray(parsed.edges) ? parsed.edges.map(normalizeEdge) : state.edges;
      state.systems = normalizeSystems(parsed.systems || state.systems);
      state.zoom = Number(parsed.zoom) || 1;
      state.selectedId = state.nodes[0]?.id || null;
      logEvent('load', `Loaded local draft with ${state.nodes.filter(node => node.type === 'status').length} statuses and ${state.edges.length} edges`);
    } catch (error) {
      logEvent('error', `Failed to parse local draft: ${error.message}`);
    }
  }

  function persistState() {
    localStorage.setItem(STORAGE_KEY, JSON.stringify({
      version: 3,
      nodes: state.nodes,
      edges: state.edges,
      systems: state.systems,
      zoom: state.zoom,
    }));
  }

  function findNode(id) {
    return state.nodes.find(node => node.id === id);
  }

  function findEdge(id) {
    return state.edges.find(edge => edge.id === id);
  }

  function outgoingEdges(nodeId) {
    return state.edges.filter(edge => edge.from === nodeId);
  }

  function incomingEdges(nodeId) {
    return state.edges.filter(edge => edge.to === nodeId);
  }

  function pointerToCanvas(event) {
    const wrap = document.getElementById('canvas-wrap');
    const rect = wrap.getBoundingClientRect();
    return {
      x: (event.clientX - rect.left + wrap.scrollLeft) / state.zoom,
      y: (event.clientY - rect.top + wrap.scrollTop) / state.zoom,
    };
  }

  function pointToViewportFromMinimap(localX, localY, scale, minX, minY, offsetX, offsetY) {
    return {
      sceneX: ((localX - offsetX) / scale) + minX - 20,
      sceneY: ((localY - offsetY) / scale) + minY - 20,
    };
  }

  function addNode(type, position) {
    const node = normalizeNode({
      id: makeId(type),
      type,
      x: position?.x ?? 160 + state.nodes.length * 28,
      y: position?.y ?? (type === 'status' ? 160 : 360 + (state.nodes.length % 3) * 110),
    });
    state.nodes.push(node);
    state.selectedId = node.id;
    state.selectedEdgeId = null;
    persistState();
    render();
  }

  function insertStarter(key) {
    const names = key === 'qa-lifecycle'
      ? ['backlog', 'in progress', 'testing', 'human-testing', 'documentation', 'done']
      : ['idea', 'in design', 'backlog', 'in progress', 'testing', 'human-testing', 'documentation', 'done'];
    const anchorX = 120 + state.nodes.length * 18;
    const anchorY = 150 + (state.nodes.length % 2) * 20;
    const created = names.map((name, index) => normalizeNode({
      id: makeId('status'),
      type: 'status',
      x: anchorX + index * 260,
      y: anchorY + (index % 2) * 14,
      title: name,
    }));
    state.nodes.push(...created);
    for (let i = 0; i < created.length - 1; i++) {
      state.edges.push(normalizeEdge({ id: makeId('edge'), from: created[i].id, to: created[i + 1].id }));
    }
    state.selectedId = created[0]?.id || null;
    state.selectedEdgeId = null;
    persistState();
    render();
  }

  function deleteNode(id) {
    state.nodes = state.nodes.filter(node => node.id !== id);
    state.edges = state.edges.filter(edge => edge.from !== id && edge.to !== id);
    if (state.selectedId === id) state.selectedId = state.nodes[0]?.id || null;
    if (state.selectedEdgeId && !findEdge(state.selectedEdgeId)) state.selectedEdgeId = null;
    if (state.connectFrom === id) state.connectFrom = null;
    persistState();
    render();
  }

  function deleteEdge(id) {
    state.edges = state.edges.filter(edge => edge.id !== id);
    if (state.selectedEdgeId === id) state.selectedEdgeId = null;
    persistState();
    render();
  }

  function toggleConnection(fromId, toId) {
    if (!fromId || !toId || fromId === toId) return;
    const existing = state.edges.find(edge => edge.from === fromId && edge.to === toId);
    if (existing) {
      state.selectedEdgeId = existing.id;
      state.selectedId = null;
      state.connectFrom = null;
      render();
      return;
    }
    state.edges.push(normalizeEdge({ id: makeId('edge'), from: fromId, to: toId }));
    state.selectedEdgeId = state.edges[state.edges.length - 1].id;
    state.selectedId = null;
    state.connectFrom = null;
    persistState();
    render();
  }

  function collectTransitions() {
    const statuses = statusNodesInOrder();
    const transitions = [];
    const seen = new Set();

    function visit(startNode, currentId, actions, visited) {
      outgoingEdges(currentId).forEach(edge => {
        const next = findNode(edge.to);
        if (!next || visited.has(next.id)) return;
        if (next.type === 'status') {
          if (next.id === startNode.id) return;
          const transition = {
            from: startNode.title.trim(),
            to: next.title.trim(),
            actions: actions.map(normalizeAction),
          };
          const key = JSON.stringify(transition);
          if (!seen.has(key)) {
            seen.add(key);
            transitions.push(transition);
          }
          return;
        }
        visit(startNode, next.id, actions.concat([actionFromNode(next)]), new Set([...visited, next.id]));
      });
    }

    statuses.forEach(status => visit(status, status.id, [], new Set([status.id])));
    return transitions.sort((a, b) => {
      const aFrom = statuses.findIndex(status => status.title.trim() === a.from);
      const bFrom = statuses.findIndex(status => status.title.trim() === b.from);
      if (aFrom !== bFrom) return aFrom - bFrom;
      return a.to.localeCompare(b.to);
    });
  }

  function collectTransitionPaths() {
    const statuses = statusNodesInOrder();
    const paths = [];
    const seen = new Set();

    function visit(startNode, currentId, actionNodeIds, visited) {
      outgoingEdges(currentId).forEach(edge => {
        const next = findNode(edge.to);
        if (!next || visited.has(next.id)) return;
        if (next.type === 'status') {
          if (next.id === startNode.id) return;
          const path = {
            fromId: startNode.id,
            toId: next.id,
            from: startNode.title.trim(),
            to: next.title.trim(),
            actionNodeIds: actionNodeIds.slice(),
          };
          const key = JSON.stringify(path);
          if (!seen.has(key)) {
            seen.add(key);
            paths.push(path);
          }
          return;
        }
        visit(startNode, next.id, actionNodeIds.concat([next.id]), new Set([...visited, next.id]));
      });
    }

    statuses.forEach(status => visit(status, status.id, [], new Set([status.id])));
    return paths.sort((a, b) => {
      const aFrom = statuses.findIndex(status => status.id === a.fromId);
      const bFrom = statuses.findIndex(status => status.id === b.fromId);
      if (aFrom !== bFrom) return aFrom - bFrom;
      const aTo = statuses.findIndex(status => status.id === a.toId);
      const bTo = statuses.findIndex(status => status.id === b.toId);
      return aTo - bTo;
    });
  }

  function draftToWorkflow() {
    return {
      statuses: statusNodesInOrder().map(node => ({
        name: node.title.trim(),
        description: node.description.trim(),
      })).filter(status => status.name),
      transitions: collectTransitions(),
      systems: state.systems,
    };
  }

  function emitBlock(lines, indent, key, value) {
    lines.push(`${indent}${key}: |`);
    value.replace(/\r/g, '').split('\n').forEach(line => {
      lines.push(`${indent}  ${line}`);
    });
  }

  function emitAction(lines, indent, action) {
    const normalized = normalizeAction(action);
    lines.push(`${indent}- type: ${yamlQuote(normalized.type)}`);
    if (normalized.rule) lines.push(`${indent}  rule: ${yamlQuote(normalized.rule)}`);
    if (normalized.status) lines.push(`${indent}  status: ${yamlQuote(normalized.status)}`);
    if (normalized.title) lines.push(`${indent}  title: ${yamlQuote(normalized.title)}`);
    if (normalized.body) emitBlock(lines, `${indent}  `, 'body', normalized.body);
    if (normalized.prompt) emitBlock(lines, `${indent}  `, 'prompt', normalized.prompt);
    if (normalized.field) lines.push(`${indent}  field: ${yamlQuote(normalized.field)}`);
    if (normalized.value) lines.push(`${indent}  value: ${yamlQuote(normalized.value)}`);
  }

  function workflowToYAML(workflow) {
    const normalized = normalizeWorkflow(workflow);
    const lines = ['statuses:'];
    normalized.statuses.forEach(status => {
      lines.push(`  - name: ${yamlQuote(status.name)}`);
      if (status.description) lines.push(`    description: ${yamlQuote(status.description)}`);
    });

    lines.push('');
    lines.push('transitions:');
    normalized.transitions.forEach(transition => {
      lines.push(`  - from: ${yamlQuote(transition.from)}`);
      lines.push(`    to: ${yamlQuote(transition.to)}`);
      lines.push('    actions:');
      if (!transition.actions.length) {
        lines.push('      []');
      } else {
        transition.actions.forEach(action => emitAction(lines, '      ', action));
      }
    });

    const systemNames = Object.keys(normalized.systems || {});
    if (systemNames.length) {
      lines.push('');
      lines.push('systems:');
      systemNames.forEach(name => {
        const overlay = normalized.systems[name] || { statuses: [], transitions: [] };
        lines.push(`  ${name}:`);
        if (overlay.statuses?.length) {
          lines.push('    statuses:');
          overlay.statuses.forEach(status => {
            lines.push(`      - name: ${yamlQuote(status.name)}`);
            if (status.description) lines.push(`        description: ${yamlQuote(status.description)}`);
          });
        }
        lines.push('    transitions:');
        if (!overlay.transitions?.length) {
          lines.push('      []');
        } else {
          overlay.transitions.forEach(transition => {
            lines.push(`      - from: ${yamlQuote(transition.from)}`);
            lines.push(`        to: ${yamlQuote(transition.to)}`);
            lines.push('        actions:');
            if (!transition.actions.length) {
              lines.push('          []');
            } else {
              transition.actions.forEach(action => emitAction(lines, '          ', action));
            }
          });
        }
      });
    }

    return lines.join('\n');
  }

  function parseActions(lines, startIndex, actionIndent) {
    const actions = [];
    let i = startIndex;
    while (i < lines.length) {
      const line = lines[i];
      const trimmed = line.trim();
      const indent = countIndent(line);
      if (!trimmed || trimmed.startsWith('#')) {
        i++;
        continue;
      }
      if (indent < actionIndent) break;
      if (indent === actionIndent && trimmed === '[]') {
        i++;
        break;
      }
      if (indent !== actionIndent || !trimmed.startsWith('- type:')) break;

      const action = normalizeAction({ type: unquoteYAML(trimmed.slice('- type:'.length)) });
      i++;
      while (i < lines.length) {
        const inner = lines[i];
        const innerTrimmed = inner.trim();
        const innerIndent = countIndent(inner);
        if (!innerTrimmed || innerTrimmed.startsWith('#')) {
          i++;
          continue;
        }
        if (innerIndent <= actionIndent) break;
        if (innerIndent !== actionIndent + 2) {
          i++;
          continue;
        }
        const block = innerTrimmed.match(/^([a-z_]+):\s*\|\s*$/);
        const scalar = innerTrimmed.match(/^([a-z_]+):\s*(.+)$/);
        if (block) {
          const field = block[1];
          i++;
          const body = [];
          while (i < lines.length) {
            const bodyLine = lines[i];
            const bodyTrimmed = bodyLine.trim();
            const bodyIndent = countIndent(bodyLine);
            if (!bodyTrimmed && bodyIndent >= actionIndent + 4) {
              body.push('');
              i++;
              continue;
            }
            if (bodyIndent < actionIndent + 4) break;
            body.push(bodyLine.slice(actionIndent + 4));
            i++;
          }
          action[field] = body.join('\n').replace(/\n+$/, '');
          continue;
        }
        if (scalar) {
          action[scalar[1]] = unquoteYAML(scalar[2]);
        }
        i++;
      }
      actions.push(normalizeAction(action));
    }
    return [actions, i];
  }

  function parseTransition(lines, startIndex, transitionIndent, actionIndent) {
    const transition = normalizeTransition({
      from: unquoteYAML(lines[startIndex].trim().slice('- from:'.length)),
      to: '',
      actions: [],
    });
    let i = startIndex + 1;
    while (i < lines.length) {
      const line = lines[i];
      const trimmed = line.trim();
      const indent = countIndent(line);
      if (!trimmed || trimmed.startsWith('#')) {
        i++;
        continue;
      }
      if (indent < transitionIndent + 2) break;
      if (indent === transitionIndent && trimmed.startsWith('- from:')) break;
      if (indent === transitionIndent + 2 && trimmed.startsWith('to:')) {
        transition.to = unquoteYAML(trimmed.slice('to:'.length));
        i++;
        continue;
      }
      if (indent === transitionIndent + 2 && trimmed === 'actions:') {
        const parsed = parseActions(lines, i + 1, actionIndent);
        transition.actions = parsed[0];
        i = parsed[1];
        continue;
      }
      i++;
    }
    return [transition, i];
  }

  function parseYAMLWorkflow(text) {
    const lines = text.replace(/\r/g, '').split('\n');
    const workflow = { statuses: [], transitions: [], systems: {} };
    let section = '';
    let i = 0;
    while (i < lines.length) {
      const line = lines[i];
      const trimmed = line.trim();
      const indent = countIndent(line);
      if (!trimmed || trimmed.startsWith('#')) {
        i++;
        continue;
      }
      if (indent === 0) {
        if (trimmed === 'statuses:') {
          section = 'statuses';
          i++;
          continue;
        }
        if (trimmed === 'transitions:') {
          section = 'transitions';
          i++;
          continue;
        }
        if (trimmed === 'systems:') {
          section = 'systems';
          i++;
          continue;
        }
      }

      if (section === 'statuses' && indent === 2 && trimmed.startsWith('- name:')) {
        const status = normalizeStatus({ name: unquoteYAML(trimmed.slice('- name:'.length)) });
        i++;
        while (i < lines.length) {
          const inner = lines[i];
          const innerTrimmed = inner.trim();
          const innerIndent = countIndent(inner);
          if (!innerTrimmed || innerTrimmed.startsWith('#')) {
            i++;
            continue;
          }
          if (innerIndent <= 2) break;
          if (innerIndent === 4 && innerTrimmed.startsWith('description:')) {
            status.description = unquoteYAML(innerTrimmed.slice('description:'.length));
          }
          i++;
        }
        workflow.statuses.push(status);
        continue;
      }

      if (section === 'transitions' && indent === 2 && trimmed.startsWith('- from:')) {
        const parsed = parseTransition(lines, i, 2, 6);
        workflow.transitions.push(parsed[0]);
        i = parsed[1];
        continue;
      }

      if (section === 'systems' && indent === 2 && trimmed.endsWith(':')) {
        const systemName = trimmed.slice(0, -1);
        const overlay = { statuses: [], transitions: [] };
        i++;
        while (i < lines.length) {
          const inner = lines[i];
          const innerTrimmed = inner.trim();
          const innerIndent = countIndent(inner);
          if (!innerTrimmed || innerTrimmed.startsWith('#')) {
            i++;
            continue;
          }
          if (innerIndent <= 2) break;
          if (innerIndent === 4 && innerTrimmed === 'statuses:') {
            i++;
            while (i < lines.length) {
              const statusLine = lines[i];
              const statusTrimmed = statusLine.trim();
              const statusIndent = countIndent(statusLine);
              if (!statusTrimmed || statusTrimmed.startsWith('#')) {
                i++;
                continue;
              }
              if (statusIndent < 6) break;
              if (statusIndent === 6 && statusTrimmed.startsWith('- name:')) {
                const status = normalizeStatus({ name: unquoteYAML(statusTrimmed.slice('- name:'.length)) });
                i++;
                while (i < lines.length) {
                  const descLine = lines[i];
                  const descTrimmed = descLine.trim();
                  const descIndent = countIndent(descLine);
                  if (!descTrimmed || descTrimmed.startsWith('#')) {
                    i++;
                    continue;
                  }
                  if (descIndent <= 6) break;
                  if (descIndent === 8 && descTrimmed.startsWith('description:')) {
                    status.description = unquoteYAML(descTrimmed.slice('description:'.length));
                  }
                  i++;
                }
                overlay.statuses.push(status);
                continue;
              }
              break;
            }
            continue;
          }
          if (innerIndent === 4 && innerTrimmed === 'transitions:') {
            i++;
            while (i < lines.length) {
              const transitionLine = lines[i];
              const transitionTrimmed = transitionLine.trim();
              const transitionIndent = countIndent(transitionLine);
              if (!transitionTrimmed || transitionTrimmed.startsWith('#')) {
                i++;
                continue;
              }
              if (transitionIndent < 6) break;
              if (transitionIndent === 6 && transitionTrimmed.startsWith('- from:')) {
                const parsed = parseTransition(lines, i, 6, 10);
                overlay.transitions.push(parsed[0]);
                i = parsed[1];
                continue;
              }
              break;
            }
            continue;
          }
          i++;
        }
        workflow.systems[systemName] = overlay;
        continue;
      }
      i++;
    }
    return normalizeWorkflow(workflow);
  }

  function importDraft(text) {
    const trimmed = text.trim();
    if (!trimmed) return;
    let parsed;
    if (trimmed.startsWith('{') || trimmed.startsWith('[')) {
      parsed = JSON.parse(trimmed);
    } else {
      parsed = parseYAMLWorkflow(trimmed);
    }
    if (parsed.statuses || parsed.Statuses) {
      const next = workflowToDraft(parsed);
      if (!next.nodes.length) {
        throw new Error('Imported workflow did not contain any statuses');
      }
      state.nodes = next.nodes;
      state.edges = next.edges;
      state.systems = next.systems;
      state.selectedId = next.nodes[0]?.id || null;
    } else {
      state.nodes = (parsed.nodes || []).map(normalizeNode);
      state.edges = (parsed.edges || []).map(normalizeEdge);
      state.systems = normalizeSystems(parsed.systems || {});
      state.selectedId = state.nodes[0]?.id || null;
    }
    state.selectedEdgeId = null;
    state.connectFrom = null;
    state.connectDrag = null;
    state.zoom = 1;
    persistState();
    logEvent('import', `Imported workflow with ${state.nodes.filter(node => node.type === 'status').length} statuses`);
    render();
    showToast(`Imported workflow: ${state.nodes.filter(node => node.type === 'status').length} statuses, ${state.edges.length} edges`, false);
  }

  function showModal(title, text, filename, mode) {
    exportState = { title, text, filename, mode: mode || 'export' };
    document.getElementById('modal-title').textContent = title;
    document.getElementById('modal-text').value = text;
    document.getElementById('download-modal-btn').textContent = exportState.mode === 'import' ? 'Apply Import' : 'Download';
    document.getElementById('copy-modal-btn').style.display = exportState.mode === 'import' ? 'none' : '';
    document.getElementById('pick-file-btn').style.display = exportState.mode === 'import' ? '' : 'none';
    document.getElementById('export-modal').classList.add('open');
  }

  function closeModal() {
    document.getElementById('export-modal').classList.remove('open');
  }

  function downloadText(filename, text) {
    const blob = new Blob([text], { type: 'text/plain;charset=utf-8' });
    const url = URL.createObjectURL(blob);
    const a = document.createElement('a');
    a.href = url;
    a.download = filename;
    document.body.appendChild(a);
    a.click();
    a.remove();
    URL.revokeObjectURL(url);
  }

  function renderPalette() {
    const root = document.getElementById('palette-root');
    root.innerHTML = NODE_TYPES.map(item => `
      <button class="palette-item" data-type="${item.type}">
        <span class="palette-icon" style="background:${item.color}">${item.icon}</span>
        <span>
          <div class="palette-item-title">${item.title}</div>
          <div class="palette-item-desc">${item.desc}</div>
        </span>
      </button>
    `).join('');
    root.querySelectorAll('[data-type]').forEach(button => {
      button.addEventListener('click', () => addNode(button.dataset.type));
    });

    const starterRoot = document.getElementById('starter-root');
    starterRoot.innerHTML = STARTERS.map(item => `
      <button class="starter-item" data-starter="${item.key}">
        <span>
          <div class="starter-item-title">${item.title}</div>
          <div class="starter-item-desc">${item.desc}</div>
        </span>
      </button>
    `).join('');
    starterRoot.querySelectorAll('[data-starter]').forEach(button => {
      button.addEventListener('click', () => insertStarter(button.dataset.starter));
    });
  }

  function getReadiness() {
    const items = [];
    const statuses = state.nodes.filter(node => node.type === 'status');
    const names = new Set();
    const dupes = new Set();
    statuses.forEach(node => {
      const key = node.title.trim().toLowerCase();
      if (!key) return;
      if (names.has(key)) dupes.add(node.title.trim());
      names.add(key);
    });
    if (!statuses.length) {
      items.push({ level: 'error', title: 'No statuses', desc: 'Add at least one status node to define the workflow lifecycle.' });
    } else {
      items.push({ level: 'good', title: `${statuses.length} statuses`, desc: 'Statuses export in left-to-right order.' });
    }
    items.push({ level: 'good', title: `${state.nodes.length - statuses.length} action nodes`, desc: 'Action nodes become transition actions when placed between statuses.' });
    if (dupes.size) {
      items.push({ level: 'error', title: 'Duplicate status names', desc: Array.from(dupes).join(', ') + ' should be unique.' });
    }
    const orphanActions = state.nodes.filter(node => node.type !== 'status' && (!incomingEdges(node.id).length || !outgoingEdges(node.id).length));
    if (orphanActions.length) {
      items.push({ level: 'warn', title: 'Orphan action nodes', desc: orphanActions.map(node => node.title).join(', ') + ' are not fully connected.' });
    }
    if (Object.keys(state.systems || {}).length) {
      items.push({ level: 'good', title: `${Object.keys(state.systems).length} subsystem overlays preserved`, desc: 'Existing `systems:` YAML stays intact on save.' });
    }
    return items;
  }

  function renderReadiness() {
    const items = getReadiness();
    const errors = items.filter(item => item.level === 'error').length;
    const warnings = items.filter(item => item.level === 'warn').length;
    const level = errors ? 'error' : warnings ? 'warn' : 'good';
    const label = errors ? `${errors} issue${errors === 1 ? '' : 's'}` : warnings ? `${warnings} warning${warnings === 1 ? '' : 's'}` : 'Ready';
    document.getElementById('readiness-root').innerHTML = `
      <div class="readiness-pill ${level}">${label}</div>
      <div class="readiness-list">
        ${items.map(item => `
          <div class="readiness-item ${item.level}">
            <div class="palette-item-title">${item.title}</div>
            <div class="readiness-item-desc">${item.desc}</div>
          </div>
        `).join('')}
      </div>
    `;
    document.getElementById('status-count-badge').textContent = `${state.nodes.filter(node => node.type === 'status').length} statuses / ${state.edges.length} edges`;
  }

  function renderDebug() {
    const root = document.getElementById('debug-root');
    if (!root) return;

    const source = document.getElementById('workflow-source-badge')?.textContent || 'unknown';
    const target = document.getElementById('workflow-target-badge')?.textContent || 'unknown';
    const normalizedInitial = normalizeWorkflow(INITIAL_WORKFLOW);
    const currentStatuses = state.nodes.filter(node => node.type === 'status').length;
    const currentActions = state.nodes.filter(node => node.type !== 'status').length;
    const currentTransitions = collectTransitions().length;

    let draftInfo = 'No local draft';
    const rawDraft = localStorage.getItem(STORAGE_KEY);
    if (rawDraft) {
      try {
        const parsed = JSON.parse(rawDraft);
        draftInfo = `Local draft v${parsed.version ?? 'unknown'} present`;
      } catch (_) {
        draftInfo = 'Local draft present but unreadable';
      }
    }

    root.innerHTML = `
      <div class="readiness-list">
        <div class="readiness-item good">
          <div class="palette-item-title">Server Source</div>
          <div class="readiness-item-desc">${escapeHTML(source)}</div>
        </div>
        <div class="readiness-item good">
          <div class="palette-item-title">Save Target</div>
          <div class="readiness-item-desc">${escapeHTML(target)}</div>
        </div>
        <div class="readiness-item ${rawDraft ? 'warn' : 'good'}">
          <div class="palette-item-title">Draft State</div>
          <div class="readiness-item-desc">${escapeHTML(draftInfo)}</div>
        </div>
        <div class="readiness-item good">
          <div class="palette-item-title">Server Workflow</div>
          <div class="readiness-item-desc">${normalizedInitial.statuses.length} statuses, ${normalizedInitial.transitions.length} transitions, ${Object.keys(normalizedInitial.systems || {}).length} systems</div>
        </div>
        <div class="readiness-item good">
          <div class="palette-item-title">Current Draft</div>
          <div class="readiness-item-desc">${currentStatuses} statuses, ${currentActions} action nodes, ${currentTransitions} exported transitions</div>
        </div>
        ${debugEvents.map(event => `
          <div class="readiness-item ${event.kind === 'error' ? 'error' : event.kind === 'warn' ? 'warn' : 'good'}">
            <div class="palette-item-title">${escapeHTML(event.kind.toUpperCase())}</div>
            <div class="readiness-item-desc">${escapeHTML(event.message)}</div>
          </div>
        `).join('')}
      </div>
    `;
  }

  function renderEdges() {
    const svg = document.getElementById('edge-layer');
    const stage = document.getElementById('canvas-scene');
    const width = Math.max(stage.scrollWidth, stage.clientWidth, 1400);
    const height = Math.max(stage.scrollHeight, stage.clientHeight, 900);
    svg.setAttribute('viewBox', `0 0 ${width} ${height}`);
    svg.setAttribute('width', width);
    svg.setAttribute('height', height);

    svg.innerHTML = state.edges.map(edge => {
      const from = findNode(edge.from);
      const to = findNode(edge.to);
      if (!from || !to) return '';
      const startX = from.x + 236;
      const startY = from.y + 48;
      const endX = to.x;
      const endY = to.y + 48;
      const delta = Math.max(90, Math.abs(endX - startX) / 2);
      const path = `M ${startX} ${startY} C ${startX + delta} ${startY}, ${endX - delta} ${endY}, ${endX} ${endY}`;
      return `
        <g data-edge-id="${edge.id}">
          <path class="workflow-edge-hit" d="${path}" fill="none" stroke="transparent" stroke-width="16" stroke-linecap="round"></path>
          <path class="workflow-edge-path ${edge.id === state.selectedEdgeId ? 'selected' : ''}" d="${path}" fill="none" stroke="#94a3b8" stroke-width="2.5" stroke-linecap="round"></path>
        </g>
      `;
    }).join('');

    if (state.connectDrag && state.connectFrom && findNode(state.connectFrom)) {
      const from = findNode(state.connectFrom);
      const startX = from.x + 236;
      const startY = from.y + 48;
      const endX = state.connectDrag.x;
      const endY = state.connectDrag.y;
      const delta = Math.max(90, Math.abs(endX - startX) / 2);
      const path = `M ${startX} ${startY} C ${startX + delta} ${startY}, ${endX - delta} ${endY}, ${endX} ${endY}`;
      svg.innerHTML += `<path class="workflow-edge-path workflow-ghost-edge" d="${path}" fill="none" stroke="#60a5fa" stroke-width="2" stroke-dasharray="7 6" stroke-linecap="round"></path>`;
    }

    svg.querySelectorAll('[data-edge-id]').forEach(group => {
      group.addEventListener('click', event => {
        event.stopPropagation();
        state.selectedEdgeId = group.dataset.edgeId;
        state.selectedId = null;
        state.connectFrom = null;
        state.connectDrag = null;
        render();
      });
    });
  }

  function renderNodes() {
    const stage = document.getElementById('canvas-scene');
    stage.querySelectorAll('.workflow-node').forEach(node => node.remove());
    document.getElementById('canvas-empty').style.display = state.nodes.length ? 'none' : 'block';

    state.nodes.forEach(node => {
      const meta = typeMeta(node.type);
      const el = document.createElement('div');
      el.className = 'workflow-node'
        + (node.id === state.selectedId ? ' selected' : '')
        + (state.connectFrom && state.connectFrom !== node.id ? ' connect-target' : '')
        + (state.connectFrom === node.id ? ' connect-from' : '');
      el.dataset.id = node.id;
      el.style.left = node.x + 'px';
      el.style.top = node.y + 'px';
      el.style.setProperty('--node-bg', colorForType(node.type));
      el.innerHTML = `
        <div class="workflow-node-header">
          <div>
            <div class="workflow-node-type">${escapeHTML(meta.title)}</div>
            <div class="workflow-node-title">${escapeHTML(node.title)}</div>
          </div>
          <div class="workflow-node-actions">
            <button class="workflow-node-action" data-action="connect" title="Connect">+</button>
            <button class="workflow-node-action" data-action="delete" title="Delete">×</button>
          </div>
        </div>
        <div class="workflow-node-body">
          <div class="workflow-node-summary">${escapeHTML(nodeSummary(node))}</div>
          <div class="workflow-node-meta">
            <span class="workflow-chip">${incomingEdges(node.id).length} in</span>
            <span class="workflow-chip">${outgoingEdges(node.id).length} out</span>
          </div>
        </div>
        <div class="workflow-node-handle input" data-handle="input" title="Incoming connection"></div>
        <div class="workflow-node-handle output" data-handle="output" title="Drag to connect"></div>
      `;

      el.addEventListener('pointerdown', event => {
        if (event.target.closest('[data-action]') || event.target.closest('[data-handle]')) return;
        event.stopPropagation();
        state.selectedId = node.id;
        state.selectedEdgeId = null;
        const point = pointerToCanvas(event);
        state.drag = {
          id: node.id,
          offsetX: point.x - node.x,
          offsetY: point.y - node.y,
        };
        el.setPointerCapture(event.pointerId);
        renderInspector();
      });

      el.addEventListener('pointermove', event => {
        if (!state.drag || state.drag.id !== node.id) return;
        const point = pointerToCanvas(event);
        node.x = Math.max(20, point.x - state.drag.offsetX);
        node.y = Math.max(20, point.y - state.drag.offsetY);
        renderEdges();
        renderMinimap();
        el.style.left = node.x + 'px';
        el.style.top = node.y + 'px';
      });

      el.addEventListener('pointerup', event => {
        if (state.drag && state.drag.id === node.id) {
          state.drag = null;
          persistState();
          render();
        }
        el.releasePointerCapture?.(event.pointerId);
      });

      el.addEventListener('click', event => {
        if (event.target.closest('[data-handle]')) return;
        const action = event.target.closest('[data-action]')?.dataset.action;
        if (action === 'delete') {
          deleteNode(node.id);
          return;
        }
        if (action === 'connect') {
          if (state.connectFrom && state.connectFrom !== node.id) {
            toggleConnection(state.connectFrom, node.id);
          } else {
            state.connectFrom = node.id;
            render();
          }
          return;
        }
        if (state.connectFrom && state.connectFrom !== node.id) {
          toggleConnection(state.connectFrom, node.id);
          return;
        }
        state.selectedId = node.id;
        state.selectedEdgeId = null;
        render();
      });

      const outputHandle = el.querySelector('[data-handle="output"]');
      outputHandle.addEventListener('pointerdown', event => {
        event.stopPropagation();
        const point = pointerToCanvas(event);
        state.selectedId = node.id;
        state.selectedEdgeId = null;
        state.connectFrom = node.id;
        state.connectDrag = { fromId: node.id, x: point.x, y: point.y };
        outputHandle.setPointerCapture(event.pointerId);
        render();
      });
      outputHandle.addEventListener('pointermove', event => {
        if (!state.connectDrag || state.connectDrag.fromId !== node.id) return;
        const point = pointerToCanvas(event);
        state.connectDrag.x = point.x;
        state.connectDrag.y = point.y;
        renderEdges();
      });
      outputHandle.addEventListener('pointerup', event => {
        if (state.connectDrag && state.connectDrag.fromId === node.id) {
          state.connectDrag = null;
          render();
        }
        outputHandle.releasePointerCapture?.(event.pointerId);
      });

      stage.appendChild(el);
    });
  }

  function renderInspector() {
    const root = document.getElementById('inspector-root');
    const edge = findEdge(state.selectedEdgeId);
    if (edge) {
      const from = findNode(edge.from);
      const to = findNode(edge.to);
      root.innerHTML = `
        <div class="inspector-section">
          <div class="inspector-chip-row">
            <span class="inspector-mini-chip">From: ${escapeHTML(from?.title || edge.from)}</span>
            <span class="inspector-mini-chip">To: ${escapeHTML(to?.title || edge.to)}</span>
          </div>
          <div class="inspector-help">Connections only define the path. Transition semantics come from the action nodes on that path.</div>
        </div>
        <div class="inspector-section">
          <button class="designer-btn" id="flip-edge-btn">Reverse Direction</button>
          <button class="designer-btn" id="delete-edge-btn">Delete Connection</button>
        </div>
      `;
      root.querySelector('#flip-edge-btn').addEventListener('click', () => {
        const nextFrom = edge.to;
        edge.to = edge.from;
        edge.from = nextFrom;
        persistState();
        render();
      });
      root.querySelector('#delete-edge-btn').addEventListener('click', () => deleteEdge(edge.id));
      return;
    }

    const node = findNode(state.selectedId);
    if (!node) {
      root.innerHTML = `<div class="inspector-empty">Select a status or action node to edit it. Status nodes export under <code>statuses:</code>. Action nodes export into transition <code>actions:</code> based on the path between statuses.</div>`;
      return;
    }

    const isStatus = node.type === 'status';
    const parsedRule = parseValidationRule(node.rule);
    const ruleMeta = validationRuleMeta(node.rule) || VALIDATION_RULES[0];
    const fieldMeta = setFieldMeta(node.field) || SET_FIELD_OPTIONS[0];
    root.innerHTML = `
      <div class="inspector-section">
        <div class="inspector-chip-row">
          <span class="inspector-mini-chip">${escapeHTML(typeMeta(node.type).title)}</span>
        </div>

        <label class="inspector-label">${isStatus ? 'Status Name' : 'Node Title'}</label>
        <input class="inspector-input" id="field-title" value="${escapeAttr(node.title)}">

        ${isStatus ? `
          <label class="inspector-label">Description</label>
          <textarea class="inspector-textarea" id="field-description">${escapeHTML(node.description || '')}</textarea>
        ` : ''}

        ${node.type === 'validate' ? `
          <label class="inspector-label">Validation Rule</label>
          <select class="inspector-select" id="field-rule-type">
            ${VALIDATION_RULES.map(item => `<option value="${item.value}" ${item.value === (parsedRule.type || ruleMeta.value) ? 'selected' : ''}>${item.label}</option>`).join('')}
          </select>
          <div class="inspector-help">${escapeHTML(ruleMeta.description)}</div>
          ${ruleMeta.argLabel ? `
            <label class="inspector-label">${escapeHTML(ruleMeta.argLabel)}</label>
            <input class="inspector-input" id="field-rule-arg" value="${escapeAttr(parsedRule.arg || '')}" placeholder="${escapeAttr(ruleMeta.argPlaceholder || '')}">
          ` : ''}
        ` : ''}

        ${node.type === 'append_section' ? `
          <label class="inspector-label">Section Title</label>
          <input class="inspector-input" id="field-title-text" value="${escapeAttr(node.titleText || '')}" placeholder="Implementation">
          <label class="inspector-label">Body</label>
          <textarea class="inspector-textarea" id="field-body">${escapeHTML(node.body || '')}</textarea>
        ` : ''}

        ${node.type === 'inject_prompt' ? `
          <label class="inspector-label">Prompt</label>
          <textarea class="inspector-textarea" id="field-prompt">${escapeHTML(node.prompt || '')}</textarea>
        ` : ''}

        ${node.type === 'approval' ? `
          <label class="inspector-label">Approved Status</label>
          <input class="inspector-input" id="field-status" value="${escapeAttr(node.status || '')}" placeholder="backlog">
        ` : ''}

        ${node.type === 'set_fields' ? `
          <label class="inspector-label">Field</label>
          <select class="inspector-select" id="field-field">
            ${SET_FIELD_OPTIONS.map(item => `<option value="${item.value}" ${item.value === (node.field || fieldMeta.value) ? 'selected' : ''}>${item.label}</option>`).join('')}
          </select>
          <div class="inspector-help">${escapeHTML(fieldMeta.description)}</div>
          <label class="inspector-label">Value</label>
          <input class="inspector-input" id="field-value" value="${escapeAttr(node.value || '')}">
        ` : ''}
      </div>

      <div class="inspector-section">
        <div class="inspector-row">
          <div>
            <label class="inspector-label">X</label>
            <input class="inspector-input" id="field-x" type="number" value="${Math.round(node.x)}">
          </div>
          <div>
            <label class="inspector-label">Y</label>
            <input class="inspector-input" id="field-y" type="number" value="${Math.round(node.y)}">
          </div>
        </div>
      </div>

      <div class="inspector-section">
        <button class="designer-btn" id="duplicate-node-btn">Duplicate Node</button>
        <button class="designer-btn" id="delete-node-btn">Delete Node</button>
      </div>
    `;

    root.querySelectorAll('input, textarea, select').forEach(field => {
      field.addEventListener('input', () => {
        const nextRuleType = root.querySelector('#field-rule-type')?.value || parsedRule.type || '';
        const nextRuleArg = root.querySelector('#field-rule-arg')?.value || '';
        node.title = root.querySelector('#field-title')?.value || node.title;
        node.description = root.querySelector('#field-description')?.value || '';
        node.rule = composeValidationRule(nextRuleType, nextRuleArg);
        node.titleText = root.querySelector('#field-title-text')?.value || '';
        node.body = root.querySelector('#field-body')?.value || '';
        node.prompt = root.querySelector('#field-prompt')?.value || '';
        node.status = root.querySelector('#field-status')?.value || '';
        node.field = root.querySelector('#field-field')?.value || '';
        node.value = root.querySelector('#field-value')?.value || '';
        node.x = Number(root.querySelector('#field-x')?.value) || node.x;
        node.y = Number(root.querySelector('#field-y')?.value) || node.y;
        persistState();
        render();
      });
    });

    root.querySelector('#duplicate-node-btn').addEventListener('click', () => {
      const copy = normalizeNode(JSON.parse(JSON.stringify(node)));
      copy.id = makeId(copy.type);
      copy.x += 42;
      copy.y += 36;
      state.nodes.push(copy);
      state.selectedId = copy.id;
      persistState();
      render();
    });
    root.querySelector('#delete-node-btn').addEventListener('click', () => deleteNode(node.id));
  }

  function showToast(message, isError) {
    const toast = document.getElementById('designer-toast');
    toast.textContent = message;
    toast.className = 'designer-toast show' + (isError ? ' error' : '');
    logEvent(isError ? 'error' : 'info', message);
    clearTimeout(showToast.timer);
    showToast.timer = setTimeout(() => {
      toast.className = 'designer-toast';
    }, 2400);
  }

  function updateZoom(nextZoom) {
    state.zoom = Math.min(1.8, Math.max(0.5, Math.round(nextZoom * 100) / 100));
    persistState();
    renderViewport();
  }

  function renderViewport() {
    const scene = document.getElementById('canvas-scene');
    const stage = document.getElementById('canvas-stage');
    scene.style.transform = `scale(${state.zoom})`;
    stage.style.minWidth = Math.max(1400, Math.ceil(1400 * state.zoom)) + 'px';
    stage.style.minHeight = Math.max(900, Math.ceil(900 * state.zoom)) + 'px';
    document.getElementById('zoom-readout').textContent = Math.round(state.zoom * 100) + '%';
    document.getElementById('minimap-scale').textContent = state.zoom.toFixed(2) + 'x';
    renderMinimap();
  }

  function renderMinimap() {
    const root = document.getElementById('minimap-canvas');
    const wrap = document.getElementById('canvas-wrap');
    const width = root.clientWidth || 210;
    const height = root.clientHeight || 117;
    if (!state.nodes.length) {
      root.innerHTML = '';
      return;
    }

    const minX = Math.min(...state.nodes.map(node => node.x));
    const minY = Math.min(...state.nodes.map(node => node.y));
    const maxX = Math.max(...state.nodes.map(node => node.x + 250));
    const maxY = Math.max(...state.nodes.map(node => node.y + 140));
    const contentWidth = Math.max(1, maxX - minX + 40);
    const contentHeight = Math.max(1, maxY - minY + 40);
    const scale = Math.min(width / contentWidth, height / contentHeight);
    const offsetX = (width - contentWidth * scale) / 2;
    const offsetY = (height - contentHeight * scale) / 2;

    root.innerHTML = state.nodes.map(node => {
      const left = offsetX + (node.x - minX + 20) * scale;
      const top = offsetY + (node.y - minY + 20) * scale;
      return `<div class="minimap-node ${node.id === state.selectedId ? 'selected' : ''}" style="left:${left}px;top:${top}px;width:${Math.max(10, 236 * scale)}px;height:${Math.max(8, 96 * scale)}px;background:${colorForType(node.type)}"></div>`;
    }).join('') + `<div class="minimap-viewport"></div>`;

    const viewport = root.querySelector('.minimap-viewport');
    viewport.style.left = `${offsetX + ((wrap.scrollLeft / state.zoom) - minX + 20) * scale}px`;
    viewport.style.top = `${offsetY + ((wrap.scrollTop / state.zoom) - minY + 20) * scale}px`;
    viewport.style.width = `${Math.min(width, (wrap.clientWidth / state.zoom) * scale)}px`;
    viewport.style.height = `${Math.min(height, (wrap.clientHeight / state.zoom) * scale)}px`;

    root.onpointerdown = event => {
      event.preventDefault();
      const rect = root.getBoundingClientRect();
      const updateViewport = clientEvent => {
        const point = pointToViewportFromMinimap(clientEvent.clientX - rect.left, clientEvent.clientY - rect.top, scale, minX, minY, offsetX, offsetY);
        wrap.scrollTo({
          left: Math.max(0, point.sceneX * state.zoom - wrap.clientWidth / 2),
          top: Math.max(0, point.sceneY * state.zoom - wrap.clientHeight / 2),
          behavior: 'auto',
        });
      };
      updateViewport(event);
      root.setPointerCapture?.(event.pointerId);
      root.onpointermove = moveEvent => updateViewport(moveEvent);
      root.onpointerup = upEvent => {
        root.onpointermove = null;
        root.onpointerup = null;
        root.releasePointerCapture?.(upEvent.pointerId);
      };
    };
  }

  async function saveWorkflowToProject() {
    const yaml = workflowToYAML(draftToWorkflow());
    try {
      const res = await fetch(SAVE_URL, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ yaml }),
      });
      const payload = await res.json().catch(() => ({}));
      if (!res.ok) throw new Error(payload.error || 'Save failed');
      showToast(`Workflow saved to ${payload.path || 'workflow.yaml'}`, false);
      logEvent('save', `Saved workflow to ${payload.path || 'workflow.yaml'}`);
    } catch (error) {
      showToast(error.message || 'Save failed', true);
    }
  }

  async function reloadWorkflowFromServer() {
    try {
      const res = await fetch(DATA_URL, { headers: { Accept: 'application/json' } });
      const payload = await res.json().catch(() => ({}));
      if (!res.ok) throw new Error(payload.error || 'Failed to reload workflow');
      const next = workflowToDraft(payload.workflow || {});
      state.nodes = next.nodes;
      state.edges = next.edges;
      state.systems = next.systems;
      state.zoom = 1;
      state.selectedId = state.nodes[0]?.id || null;
      state.selectedEdgeId = null;
      state.connectFrom = null;
      state.connectDrag = null;
      localStorage.removeItem(STORAGE_KEY);
      const sourceBadge = document.getElementById('workflow-source-badge');
      const targetBadge = document.getElementById('workflow-target-badge');
      if (sourceBadge && payload.source) sourceBadge.textContent = payload.source;
      if (targetBadge && payload.target) targetBadge.textContent = payload.target;
      logEvent('load', `Reloaded workflow from server file source ${payload.source || 'unknown'}`);
      render();
      showToast(`Reloaded workflow from ${payload.source || 'server'}`, false);
    } catch (error) {
      showToast(error.message || 'Failed to reload workflow', true);
    }
  }

  function render() {
    renderPalette();
    renderReadiness();
    renderDebug();
    renderNodes();
    renderEdges();
    renderInspector();
    document.getElementById('connect-mode-btn').classList.toggle('active', Boolean(state.connectFrom));
    document.getElementById('canvas-hint').style.display = state.nodes.length ? '' : 'none';
    renderViewport();
  }

  document.getElementById('save-workflow-btn').addEventListener('click', saveWorkflowToProject);
  document.getElementById('add-status-btn').addEventListener('click', () => addNode('status'));
  document.getElementById('connect-mode-btn').addEventListener('click', () => {
    state.connectFrom = state.connectFrom ? null : state.selectedId;
    if (state.connectFrom) state.selectedEdgeId = null;
    render();
  });
  document.getElementById('auto-layout-btn').addEventListener('click', () => {
    const statuses = statusNodesInOrder();
    const paths = collectTransitionPaths();
    const positionedActionNodes = new Set();

    statuses.forEach((node, index) => {
      node.x = 120 + index * 280;
      node.y = 150;
    });

    paths.forEach((path, pathIndex) => {
      const fromNode = findNode(path.fromId);
      const toNode = findNode(path.toId);
      if (!fromNode || !toNode || !path.actionNodeIds.length) return;

      const span = Math.max(180, toNode.x - fromNode.x);
      const laneY = 360 + pathIndex * 140;
      path.actionNodeIds.forEach((nodeId, actionIndex) => {
        const node = findNode(nodeId);
        if (!node || positionedActionNodes.has(node.id)) return;
        node.x = fromNode.x + ((actionIndex + 1) * span) / (path.actionNodeIds.length + 1);
        node.y = laneY;
        positionedActionNodes.add(node.id);
      });
    });

    const unpositioned = state.nodes
      .filter(node => node.type !== 'status' && !positionedActionNodes.has(node.id))
      .sort((a, b) => a.x - b.x);

    unpositioned.forEach((node, index) => {
      node.x = 180 + (index % 4) * 220;
      node.y = 360 + (paths.length + Math.floor(index / 4)) * 140;
    });

    logEvent('layout', `Auto-laid out ${statuses.length} statuses across ${paths.length} transition paths`);
    persistState();
    render();
  });
  document.getElementById('focus-selection-btn').addEventListener('click', () => {
    const wrap = document.getElementById('canvas-wrap');
    const node = findNode(state.selectedId);
    if (node) {
      wrap.scrollTo({ left: Math.max(0, node.x - 180), top: Math.max(0, node.y - 140), behavior: 'smooth' });
      return;
    }
    const edge = findEdge(state.selectedEdgeId);
    if (!edge) return;
    const from = findNode(edge.from);
    const to = findNode(edge.to);
    if (!from || !to) return;
    wrap.scrollTo({ left: Math.max(0, ((from.x + to.x) / 2) - 220), top: Math.max(0, ((from.y + to.y) / 2) - 160), behavior: 'smooth' });
  });
  document.getElementById('clear-local-btn').addEventListener('click', () => {
    localStorage.removeItem(STORAGE_KEY);
    logEvent('warn', 'Cleared local draft');
    loadState();
    render();
  });
  document.getElementById('zoom-in-btn').addEventListener('click', () => updateZoom(state.zoom + 0.1));
  document.getElementById('zoom-out-btn').addEventListener('click', () => updateZoom(state.zoom - 0.1));
  document.getElementById('zoom-reset-btn').addEventListener('click', () => updateZoom(1));
  document.getElementById('reset-btn').addEventListener('click', () => {
    logEvent('warn', 'Requested reload from server file');
    reloadWorkflowFromServer();
  });
  document.getElementById('import-btn').addEventListener('click', () => {
    showModal('Import Workflow', INITIAL_YAML || workflowToYAML(draftToWorkflow()), 'workflow.yaml', 'import');
  });
  document.getElementById('export-json-btn').addEventListener('click', () => {
    showModal('Export Draft JSON', JSON.stringify({ version: 3, nodes: state.nodes, edges: state.edges, systems: state.systems }, null, 2), 'workflow-designer-draft.json', 'export');
  });
  document.getElementById('export-yaml-btn').addEventListener('click', () => {
    showModal('Export Workflow YAML', workflowToYAML(draftToWorkflow()), 'workflow.yaml', 'export');
  });
  document.getElementById('close-modal-btn').addEventListener('click', closeModal);
  document.getElementById('copy-modal-btn').addEventListener('click', async () => {
    try {
      await navigator.clipboard.writeText(document.getElementById('modal-text').value);
    } catch (_) {}
  });
  document.getElementById('pick-file-btn').addEventListener('click', () => {
    document.getElementById('workflow-file-input').click();
  });
  document.getElementById('workflow-file-input').addEventListener('change', event => {
    const file = event.target.files && event.target.files[0];
    if (!file) return;
    const reader = new FileReader();
    reader.onload = () => {
      document.getElementById('modal-text').value = String(reader.result || '');
      logEvent('import', `Loaded file ${file.name} into import modal`);
      showToast(`Loaded ${file.name}`, false);
    };
    reader.onerror = () => {
      showToast(`Failed to read ${file.name}`, true);
    };
    reader.readAsText(file);
    event.target.value = '';
  });
  document.getElementById('download-modal-btn').addEventListener('click', () => {
    const text = document.getElementById('modal-text').value;
    if (exportState.mode === 'import') {
      try {
        importDraft(text);
        closeModal();
      } catch (error) {
        logEvent('error', `Import failed: ${error.message}`);
        alert('Import failed: ' + error.message);
      }
      return;
    }
    downloadText(exportState.filename || 'workflow.txt', text);
  });
  document.getElementById('export-modal').addEventListener('click', event => {
    if (event.target.id === 'export-modal') closeModal();
  });
  document.getElementById('canvas-wrap').addEventListener('click', event => {
    if (event.target.closest('.workflow-node') || event.target.closest('[data-edge-id]')) return;
    state.selectedId = null;
    state.selectedEdgeId = null;
    state.connectFrom = null;
    state.connectDrag = null;
    render();
  });
  document.getElementById('canvas-wrap').addEventListener('pointerdown', event => {
    if (event.button !== 0) return;
    if (event.target.closest('.workflow-node') || event.target.closest('[data-edge-id]') || event.target.closest('.minimap') || event.target.closest('.canvas-toolbar')) return;
    event.preventDefault();
    const wrap = event.currentTarget;
    state.pan = {
      startX: event.clientX,
      startY: event.clientY,
      scrollLeft: wrap.scrollLeft,
      scrollTop: wrap.scrollTop,
    };
    wrap.classList.add('panning');
    wrap.setPointerCapture?.(event.pointerId);
  });
  document.getElementById('canvas-wrap').addEventListener('pointermove', event => {
    if (!state.pan) return;
    event.preventDefault();
    const wrap = event.currentTarget;
    wrap.scrollLeft = state.pan.scrollLeft - (event.clientX - state.pan.startX);
    wrap.scrollTop = state.pan.scrollTop - (event.clientY - state.pan.startY);
    renderMinimap();
  });
  document.getElementById('canvas-wrap').addEventListener('pointerup', event => {
    if (!state.pan) return;
    const wrap = event.currentTarget;
    state.pan = null;
    wrap.classList.remove('panning');
    wrap.releasePointerCapture?.(event.pointerId);
  });
  document.getElementById('canvas-wrap').addEventListener('scroll', renderMinimap);

  renderPalette();
  loadState();
  render();
})();
