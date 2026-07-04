package browser

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/chromedp"
)

// dismissOverlaysJS detects + dismisses a cookie/consent banner (the #1
// real-world blocker: it overlays the page, intercepts clicks, and bloats the
// AX tree with "Accept"/"Reject" noise). It does NOT touch generic modals -
// only high-confidence consent UIs (OneTrust/Didomi/Quantcast/TrustArc/
// Cookiebot + cookie-context scoring), so it won't dismiss a real dialog.
//
// Returns a short label of what it clicked ("" if nothing), e.g.
// "rejected cookies (OneTrust)". It prioritizes a true Reject/Decline over
// Accept (privacy-correct + the banner that won't reappear), falls back to a
// Close/dismiss, and as a last resort accepts (only if a cookie-context
// container is blocking the page). Capped at 3 clicks per scan. The Go side
// re-snapshots after, so the verdict reflects the cleared page.
//
// Adapted from the cookie-pop-up-auto-rejector scoring approach (MIT), trimmed
// to the high-confidence paths so false positives stay near zero.
const dismissOverlaysJS = `(() => {
  const CLICKABLE = 'button, [role="button"], a[href], input[type="submit"], input[type="button"], summary';
  const CONTAINERS = [
    '#onetrust-banner-sdk','#onetrust-consent-sdk','.onetrust-pc-dark-filter',
    '#didomi-host','#didomi-popup','.qc-cmp2-container',
    '[id*="cookie" i]','[class*="cookie" i]','[id*="consent" i]','[class*="consent" i]',
    '[id*="gdpr" i]','[class*="gdpr" i]','[id*="sp_message" i]','[class*="sp_message" i]',
    '[id*="trustarc" i]','[class*="trustarc" i]','[aria-label*="cookie" i]','[role="dialog"]','dialog'
  ];
  const KNOWN_REJECT = [
    '#onetrust-reject-all-handler','button#onetrust-reject-all-handler',
    'button[aria-label*="reject" i]','[id*="didomi-notice-disagree" i]','[id*="disagree" i]',
    '[id*="reject" i][id*="cookie" i]','[class*="reject" i][class*="cookie" i]',
    '[data-testid*="reject" i]','[aria-label*="decline" i]','button[title*="decline" i]',
    '.qc-cmp2-summary-buttons button[mode="secondary"]'
  ];
  const STRONG = /reject all|decline all|deny all|only necessary|only essential|necessary cookies only|essential cookies only|continue without accepting|do not accept|do not allow|do not sell|tout refuser|alles ablehnen|rechazar todo/i;
  const REJECT = /reject|decline|deny|refuse|disagree|opt ?out|no thanks?|ablehnen|refuser|rechazar|rifiuta/i;
  const DISMISS = /close|dismiss|skip|continue without accepting|^×$/i;
  const CTX = /cookies?|consent|gdpr|tracking|advertising|personal data|vendors?|do not sell/i;
  const CMP = /onetrust|didomi|trustarc|quantcast|qc-cmp|cookiebot|iubenda|usercentrics|sp_message/i;

  const visible = (el) => {
    if (!el || !el.isConnected) return false;
    const s = getComputedStyle(el);
    if (s.display === 'none' || s.visibility === 'hidden' || Number(s.opacity) === 0) return false;
    const r = el.getBoundingClientRect();
    return r.width > 1 && r.height > 1;
  };
  const clickable = (el) => visible(el) && el.getAttribute('disabled') === null && el.getAttribute('aria-disabled') !== 'true';
  const signals = (el) => {
    const t = (el.innerText || el.textContent || '').replace(/\s+/g, ' ').trim().toLowerCase();
    const a = [el.getAttribute('aria-label'), el.getAttribute('title'), el.getAttribute('value'), el.id, (typeof el.className === 'string' ? el.className : (el.className && el.className.baseVal) || '')].filter(Boolean).join(' ').toLowerCase();
    return (t + ' ' + a);
  };
  const inCtx = (el) => {
    let cur = el, d = 0;
    while (cur && d < 6) {
      if (CTX.test(signals(cur)) || CMP.test(signals(cur))) return true;
      cur = cur.parentElement; d++;
    }
    return false;
  };

  const containers = new Set();
  try { document.querySelectorAll(CONTAINERS.join(',')).forEach(el => { if (visible(el) && (CTX.test(signals(el)) || CMP.test(signals(el)))) containers.add(el); }); } catch(e) {}
  const fallback = '[role="dialog"], dialog, aside, section, form, div[aria-modal="true"]';
  try { document.querySelectorAll(fallback).forEach(el => { if (visible(el) && (CTX.test(signals(el)) || CMP.test(signals(el)))) containers.add(el); }); } catch(e) {}

  const isKnown = (el) => KNOWN_REJECT.some(sel => { try { return el.matches(sel); } catch(e) { return false; } });
  const score = (el) => {
    const t = signals(el);
    if (!t) return -Infinity;
    let s = 0;
    if (STRONG.test(t)) s += 12;
    if (REJECT.test(t)) s += 8;
    if (/accept|allow|agree|consent/i.test(t)) s -= 10;
    if (inCtx(el)) s += 7;
    if (/\b(all|alles|todo|tous)\b/i.test(t)) s += 2;
    if (/\b(only|essential|necessary)\b/i.test(t)) s += 2;
    if (el.tagName === 'BUTTON' || el.getAttribute('role') === 'button') s += 2;
    return s;
  };

  const candidates = [];
  // 1. known reject selectors anywhere on the page.
  KNOWN_REJECT.forEach(sel => { try { document.querySelectorAll(sel).forEach(el => { if (clickable(el)) candidates.push(el); }); } catch(e) {} });
  // 2. clickables inside detected cookie containers.
  containers.forEach(c => { try { c.querySelectorAll(CLICKABLE).forEach(el => { if (clickable(el)) candidates.push(el); }); } catch(e) {} });

  const uniq = [...new Set(candidates)].filter(clickable).map(el => ({ el, t: signals(el), s: score(el), ctx: inCtx(el) }))
    .filter(x => x.s >= 8 && (x.ctx || STRONG.test(x.t)))
    .sort((a, b) => b.s - a.s);

  // Prefer a strong/reject candidate. Fallback to a dismiss button inside a
  // container. Last resort: an accept button ONLY if a container is blocking
  // the page (so we don't auto-accept consent without a banner present).
  const fire = (el, label) => {
    try { el.focus({ preventScroll: true }); } catch(e) {}
    for (const type of ['pointerdown','mousedown','pointerup','mouseup','click']) { try { el.dispatchEvent(new MouseEvent(type, { bubbles: true, cancelable: true, view: window, buttons: 1 })); } catch(e) {} }
    try { el.click(); } catch(e) {}
    return label;
  };

  if (uniq.length > 0) {
    const top = uniq[0];
    fire(top.el);  // actually click the reject button (returning the label without clicking was a silent no-op)
    const label = STRONG.test(top.t) ? 'rejected cookies' : (REJECT.test(top.t) ? 'rejected cookies' : 'dismissed consent');
    const cmp = CMP.exec(signals(top.el));
    return cmp ? label + ' (' + cmp[0] + ')' : label;
  }
  // dismiss fallback (an X/close inside a cookie container)
  for (const c of containers) {
    try { for (const el of c.querySelectorAll(CLICKABLE)) { if (clickable(el) && DISMISS.test(signals(el))) return fire(el, 'dismissed consent (close)'); } } catch(e) {}
  }
  // last-resort accept only if a container is present
  if (containers.size > 0) {
    for (const c of containers) {
      try { for (const el of c.querySelectorAll(CLICKABLE)) { if (clickable(el) && /accept|allow|agree|consent|ok|got it/i.test(signals(el))) return fire(el, 'accepted cookies'); } } catch(e) {}
    }
  }
  return '';
})()`

// dismissOverlaysLocked runs the cookie/consent dismissal once on the current
// tab. Returns a short label ("" if no banner was found/dismissed). Caller must
// hold s.mu. Cheap: one Evaluate; the click sequence runs in-page. No-op when
// the overlay-dismiss feature is disabled (cfg.NoOverlayDismiss) or there's no
// page. Bounded by opTimeout via s.run.
func (s *Session) dismissOverlaysLocked(t *tab) string {
	if s.cfg.NoOverlayDismiss || t == nil {
		return ""
	}
	var label string
	_ = s.run(t, chromedp.ActionFunc(func(ctx context.Context) error {
		res, exc, err := runtime.Evaluate(dismissOverlaysJS).WithReturnByValue(true).Do(ctx)
		if err != nil || exc != nil {
			return nil
		}
		if res != nil && len(res.Value) > 0 {
			var s2 string
			_ = json.Unmarshal(res.Value, &s2)
			label = strings.TrimSpace(s2)
		}
		return nil
	}))
	return label
}

// overlaySettle is the brief settle after a dismiss so the banner's removal
// animation + any re-layout completes before the tree rebuild.
const overlaySettle = 350 * time.Millisecond
