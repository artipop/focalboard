package webtest

// The model perceives the page as text, not as a picture: snapshotJS walks the
// DOM, stamps every interactive element with data-fbref="e12" and returns an
// indented outline. Acting on the page is then just
// document.querySelector('[data-fbref="e12"]') — no handle bookkeeping on the
// Go side, and the model never has to invent a CSS selector from a screenshot.
//
// Refs are only valid until the next snapshot (or a re-render); a stale ref
// fails loudly and the model is told to take a fresh snapshot.

// snapshotMaxLines caps the outline so a huge page cannot blow up the context.
const snapshotMaxLines = 400

const snapshotJS = `(maxLines) => {
  document.querySelectorAll('[data-fbref]').forEach((e) => e.removeAttribute('data-fbref'));

  const out = [];
  let refs = 0;

  const trunc = (s, max) => {
    s = (s || '').replace(/\s+/g, ' ').trim();
    return s.length > max ? s.slice(0, max) + '…' : s;
  };

  const INTERACTIVE = new Set(['a', 'button', 'input', 'select', 'textarea', 'summary', 'option']);
  const ROLES = new Set(['button', 'link', 'checkbox', 'radio', 'tab', 'menuitem', 'menuitemcheckbox',
    'switch', 'textbox', 'combobox', 'option', 'slider', 'searchbox']);

  const hidden = (el) => {
    const s = window.getComputedStyle(el);
    if (s.display === 'none' || s.visibility === 'hidden' || s.opacity === '0') return true;
    if (el.getAttribute('aria-hidden') === 'true') return true;
    const tag = el.tagName.toLowerCase();
    return tag === 'script' || tag === 'style' || tag === 'noscript' || tag === 'template';
  };

  const empty = (el) => {
    const r = el.getBoundingClientRect();
    return r.width === 0 && r.height === 0 && el.children.length === 0;
  };

  const interactive = (el) => {
    const tag = el.tagName.toLowerCase();
    if (INTERACTIVE.has(tag)) return true;
    if (el.isContentEditable) return true;
    if (el.hasAttribute('onclick')) return true;
    if (el.tabIndex >= 0 && el.hasAttribute('tabindex')) return true;
    return ROLES.has((el.getAttribute('role') || '').toLowerCase());
  };

  const describe = (el) => {
    const tag = el.tagName.toLowerCase();
    const role = el.getAttribute('role');
    const parts = [role ? tag + '/' + role : tag];
    let name = el.getAttribute('aria-label') || el.getAttribute('placeholder') || el.getAttribute('title') || '';
    if (!name && (tag === 'input' || tag === 'textarea')) name = el.getAttribute('name') || el.getAttribute('type') || '';
    if (!name) name = el.innerText || el.value || '';
    name = trunc(name, 80);
    if (name) parts.push('"' + name + '"');
    if (tag === 'input' || tag === 'textarea' || tag === 'select') {
      const value = trunc(el.value, 40);
      if (value && value !== name) parts.push('value="' + value + '"');
      if (el.type === 'checkbox' || el.type === 'radio') parts.push(el.checked ? 'checked' : 'unchecked');
    }
    if (el.disabled) parts.push('disabled');
    if (tag === 'a' && el.getAttribute('href')) parts.push('href=' + trunc(el.getAttribute('href'), 60));
    return parts.join(' ');
  };

  const ownText = (el) => {
    let text = '';
    for (const node of el.childNodes) {
      if (node.nodeType === Node.TEXT_NODE) text += node.textContent;
    }
    return trunc(text, 120);
  };

  const walk = (el, depth) => {
    if (out.length >= maxLines || hidden(el) || empty(el)) return;
    const pad = '  '.repeat(Math.min(depth, 12));
    let descend = depth;
    if (interactive(el)) {
      const ref = 'e' + ++refs;
      el.setAttribute('data-fbref', ref);
      out.push(pad + describe(el) + ' [' + ref + ']');
      descend = depth + 1;
    } else {
      const tag = el.tagName.toLowerCase();
      const text = ownText(el);
      if (text) {
        out.push(pad + (/^h[1-6]$/.test(tag) ? tag : 'text') + ' "' + text + '"');
        descend = depth + 1;
      }
    }
    for (const child of el.children) walk(child, descend);
  };

  if (document.body) {
    for (const child of document.body.children) walk(child, 0);
  }

  let body = out.join('\n') || '(на странице нет видимых элементов)';
  if (out.length >= maxLines) body += '\n… (снимок обрезан, уточни область другим способом)';
  return 'url: ' + location.href + '\ntitle: ' + document.title + '\n\n' + body;
}`

// refSelector is the CSS selector behind a ref.
func refSelector(ref string) string {
	return `[data-fbref="` + ref + `"]`
}
