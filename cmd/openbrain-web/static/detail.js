(function() {
  // Inject backdrop
  let backdrop = document.getElementById('detail-backdrop');
  if (!backdrop) {
    backdrop = document.createElement('div');
    backdrop.id = 'detail-backdrop';
    backdrop.className = 'detail-backdrop';
    document.body.appendChild(backdrop);
  }

  // Inject panel
  let panel = document.getElementById('detail-panel');
  if (!panel) {
    panel = document.createElement('div');
    panel.id = 'detail-panel';
    panel.className = 'no-transition';
    panel.setAttribute('role', 'dialog');
    panel.setAttribute('aria-modal', 'true');
    panel.setAttribute('aria-label', 'Note detail');
    panel.setAttribute('aria-hidden', 'true');
    panel.innerHTML = `
      <div id="detail-resize-handle" role="separator" aria-label="Resize panel" tabindex="0" aria-valuenow="480" aria-valuemin="360" aria-valuemax="1100"></div>
      <div id="detail-head">
        <h2 id="detail-title">Note</h2>
        <button id="detail-delete" class="result-btn" title="Delete this thought permanently" style="display:none">Delete</button>
        <button id="detail-close" aria-label="Close panel">✕</button>
      </div>
      <div id="detail-content"></div>
    `;
    document.body.appendChild(panel);

    // Force browser reflow to apply translateX(100%) immediately without transition animation
    panel.offsetHeight;

    panel.classList.remove('no-transition');
  }

  const resizeHandle = document.getElementById('detail-resize-handle');
  const closeBtn = document.getElementById('detail-close');
  const deleteBtn = document.getElementById('detail-delete');
  const contentEl = document.getElementById('detail-content');
  const titleEl = document.getElementById('detail-title');

  const DETAIL_WIDTH_KEY = 'openbrain.detailWidth';
  const DETAIL_MIN_WIDTH = 360;
  const DETAIL_MAX_WIDTH = 1100;
  let lastFocused = null;

  function clampWidth(w) {
    const max = Math.max(DETAIL_MIN_WIDTH, Math.min(DETAIL_MAX_WIDTH, window.innerWidth * 0.95));
    return Math.round(Math.max(DETAIL_MIN_WIDTH, Math.min(max, w)));
  }

  function setWidth(w, persist) {
    const clamped = clampWidth(Number(w));
    panel.style.setProperty('--detail-w', clamped + 'px');
    if (persist) {
      try { localStorage.setItem(DETAIL_WIDTH_KEY, String(clamped)); } catch (_) {}
    }
    return clamped;
  }

  function restoreWidth() {
    try {
      const saved = Number(localStorage.getItem(DETAIL_WIDTH_KEY));
      if (Number.isFinite(saved) && saved > 0) setWidth(saved, false);
    } catch (_) {}
  }

  restoreWidth();
  window.addEventListener('resize', () => {
    const w = panel.getBoundingClientRect().width;
    if (w > 0) setWidth(w, false);
  });

  let resizing = false;
  let activePointer = null;

  function finishResize() {
    if (!resizing) return;
    resizing = false;
    document.body.classList.remove('resizing');
    if (resizeHandle.releasePointerCapture && activePointer !== null) {
      resizeHandle.releasePointerCapture(activePointer);
    }
  }

  resizeHandle.addEventListener('pointerdown', (e) => {
    if (window.innerWidth <= 640) return;
    resizing = true;
    activePointer = e.pointerId;
    resizeHandle.setPointerCapture(e.pointerId);
    document.body.classList.add('resizing');
    e.preventDefault();
  });

  resizeHandle.addEventListener('pointermove', (e) => {
    if (!resizing) return;
    setWidth(window.innerWidth - e.clientX, true);
  });

  resizeHandle.addEventListener('pointerup', finishResize);
  resizeHandle.addEventListener('pointercancel', finishResize);
  resizeHandle.addEventListener('keydown', (e) => {
    const current = panel.getBoundingClientRect().width;
    if (e.key === 'ArrowLeft') { setWidth(current + 24, true); e.preventDefault(); }
    if (e.key === 'ArrowRight') { setWidth(current - 24, true); e.preventDefault(); }
    if (e.key === 'Home') { setWidth(DETAIL_MIN_WIDTH, true); e.preventDefault(); }
    if (e.key === 'End') { setWidth(DETAIL_MAX_WIDTH, true); e.preventDefault(); }
  });

  function closeDetail() {
    panel.classList.remove('open');
    backdrop.classList.remove('open');
    panel.setAttribute('aria-hidden', 'true');
    if (lastFocused && lastFocused.focus) lastFocused.focus();
    window.dispatchEvent(new CustomEvent('openbrain:detail-closed'));
  }

  closeBtn.addEventListener('click', closeDetail);
  backdrop.addEventListener('click', closeDetail);
  document.addEventListener('keydown', (e) => {
    if (e.key === 'Escape' && panel.classList.contains('open')) closeDetail();
  });

  // Shared DOM & Markdown rendering helpers
  window.el = window.el || function(tag, cls) {
    const e = document.createElement(tag);
    if (cls) e.className = cls;
    return e;
  };

  window.renderMarkdownSafe = window.renderMarkdownSafe || function(text, container) {
    const parts = text.split(/(\*\*[^*]+\*\*|\*[^*]+\*|`[^`]+`)/);
    parts.forEach(function(part) {
      if (part.startsWith('**') && part.endsWith('**')) {
        const s = el('span', 'md-bold');
        s.textContent = part.slice(2, -2);
        container.appendChild(s);
      } else if (part.startsWith('*') && part.endsWith('*') && part.length > 2) {
        const s = el('span', 'md-em');
        s.textContent = part.slice(1, -1);
        container.appendChild(s);
      } else if (part.startsWith('`') && part.endsWith('`') && part.length > 2) {
        const s = el('span', 'md-code');
        s.textContent = part.slice(1, -1);
        container.appendChild(s);
      } else {
        container.appendChild(document.createTextNode(part));
      }
    });
  };

  window.renderNoteBody = window.renderNoteBody || function(text, container) {
    const lines = String(text == null ? '' : text).replace(/\r\n/g, '\n').split('\n');
    let i = 0;

    const flushParagraph = function(buf) {
      if (!buf.length) return;
      const p = el('p');
      buf.forEach(function(line, idx) {
        if (idx > 0) p.appendChild(el('br'));
        renderMarkdownSafe(line, p);
      });
      container.appendChild(p);
    };

    while (i < lines.length) {
      const line = lines[i];

      const heading = line.match(/^\s*(#{1,6})\s+(.+?)\s*#*\s*$/);
      if (heading) {
        const level = Math.min(heading[1].length, 4);
        const title = el('h' + level);
        renderMarkdownSafe(heading[2], title);
        container.appendChild(title);
        i++;
        continue;
      }

      const fence = line.match(/^\s*(`{2,})([^`]*)$/);
      if (fence) {
        const fenceLength = fence[1].length;
        i++;
        const code = [];
        const closingFence = new RegExp('^\\s*`{' + fenceLength + ',}\\s*$');
        while (i < lines.length && !closingFence.test(lines[i])) {
          code.push(lines[i]);
          i++;
        }
        if (i < lines.length) i++;
        const pre = el('pre');
        const codeEl = el('code');
        codeEl.textContent = code.join('\n');
        pre.appendChild(codeEl);
        container.appendChild(pre);
        continue;
      }

      if (/^\s*[-*•]\s+/.test(line)) {
        const ul = el('ul');
        while (i < lines.length && /^\s*[-*•]\s+/.test(lines[i])) {
          const li = el('li');
          renderMarkdownSafe(lines[i].replace(/^\s*[-*•]\s+/, ''), li);
          ul.appendChild(li);
          i++;
        }
        container.appendChild(ul);
        continue;
      }

      if (/^\s*$/.test(line)) {
        i++;
        continue;
      }

      const buf = [];
      while (i < lines.length && !/^\s*$/.test(lines[i]) &&
             !/^\s*[-*•]\s+/.test(lines[i]) && !/^\s*`{2,}[^`]*$/.test(lines[i]) &&
             !/^\s*#{1,6}\s+/.test(lines[i])) {
        buf.push(lines[i]);
        i++;
      }
      flushParagraph(buf);
    }

    if (!container.childNodes.length) {
      container.appendChild(document.createTextNode(''));
    }
  };

  // Expose public API on window
  window.OpenBrainDetail = {
    panel,
    backdrop,
    content: contentEl,
    title: titleEl,
    deleteBtn,
    close: closeDetail,
    open: function(title) {
      lastFocused = document.activeElement;
      panel.classList.add('open');
      backdrop.classList.add('open');
      panel.setAttribute('aria-hidden', 'false');
      titleEl.textContent = title || 'Note';
      contentEl.innerHTML = '';
      closeBtn.focus();
    }
  };
})();
