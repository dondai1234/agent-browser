package browser

import (
	"context"
	"math/rand"
	"strings"
	"time"

	"github.com/chromedp/cdproto/input"
	"github.com/chromedp/chromedp"
)

// stealthInitScript runs before every page script (via CDP
// Page.addScriptToEvaluateOnNewDocument) to mask the common static automation
// tells: navigator.webdriver, userAgentData, plugins, languages, window.chrome,
// the Notification/Permissions inconsistency, WebGL vendor/renderer, and
// hardware specs. Table-stakes - without it you're caught at the first gate;
// with it you pass the basic static checks. It does NOT beat the CDP runtime
// signal, GPU rendering hashes, or behavioral/entropy analysis (see README).
const stealthInitScript = `(() => {
  try { Object.defineProperty(navigator, 'webdriver', { get: () => undefined }); } catch(e) {}
  if (navigator.userAgentData) {
    try {
      Object.defineProperty(navigator, 'userAgentData', { get: () => ({
        brands: [{brand:'Chromium',version:'148'},{brand:'Google Chrome',version:'148'},{brand:'Not:A-Brand',version:'99'}],
        mobile: false, platform: 'Windows',
        getHighEntropyValues: async () => ({ architecture:'x86', bitness:'64', model:'', platformVersion:'15.0.0',
          fullVersionList:[{brand:'Chromium',version:'148.0.7778.96'},{brand:'Google Chrome',version:'148.0.7778.96'}] })
      }) });
    } catch(e) {}
  }
  try { Object.defineProperty(navigator, 'languages', { get: () => ['en-US','en'] }); } catch(e) {}
  try {
    const names = ['PDF Viewer','Chrome PDF Viewer','Chromium PDF Viewer','Microsoft Edge PDF Viewer','WebKit built-in PDF'];
    Object.defineProperty(navigator, 'plugins', { get: () => names.map(n => ({ name: n, filename: 'internal-pdf-viewer', description: 'Portable Document Format' })) });
  } catch(e) {}
  if (!window.chrome) { window.chrome = { runtime: {}, loadTimes: () => {}, csi: () => {} }; }
  else if (!window.chrome.runtime) { window.chrome.runtime = {}; }
  try { Object.defineProperty(navigator, 'hardwareConcurrency', { get: () => 8 }); } catch(e) {}
  try { Object.defineProperty(navigator, 'deviceMemory', { get: () => 8 }); } catch(e) {}
  try {
    const get = WebGLRenderingContext.prototype.getParameter;
    WebGLRenderingContext.prototype.getParameter = function(p) {
      if (p === 37445) return 'Intel Inc.';
      if (p === 37446) return 'Intel Iris OpenGL Engine';
      return get.call(this, p);
    };
    if (window.WebGL2RenderingContext) {
      const get2 = WebGL2RenderingContext.prototype.getParameter;
      WebGL2RenderingContext.prototype.getParameter = function(p) {
        if (p === 37445) return 'Intel Inc.';
        if (p === 37446) return 'Intel Iris OpenGL Engine';
        return get2.call(this, p);
      };
    }
  } catch(e) {}
  try { if (window.Notification) { Object.defineProperty(Notification, 'permission', { get: () => 'default' }); } } catch(e) {}
})();`

// detectChallengeTitleURL returns a non-empty challenge label if the page title
// or URL looks like a bot-check interstitial (Cloudflare/DataDome "Just a
// moment", generic JS challenge). Cheap (no DOM query) - called on every
// snapshot. DOM-based captcha detection (reCAPTCHA/hCaptcha) is done on
// navigation via detectChallengeDOMLocked.
func detectChallengeTitleURL(url, title string) string {
	t := strings.ToLower(title)
	u := strings.ToLower(url)
	switch {
	case strings.Contains(t, "just a moment"), strings.Contains(t, "attention required!"), strings.Contains(u, "/cdn-cgi/challenge-platform"):
		return "Cloudflare/managed challenge (\"Just a moment...\"). It may clear after a few seconds (wait); hard targets need --proxy-server (residential) + a captcha solver."
	case strings.Contains(t, "ddos protection by cloudflare"), strings.Contains(t, "checking your browser"):
		return "Bot-check interstitial (Cloudflare/DataDome-style). Wait, or use --proxy-server + a solver."
	}
	return ""
}

// detectChallengeDOMLocked runs one quick evaluate for reCAPTCHA/hCaptcha/
// Turnstile markers. Called only on navigation (not every snapshot) to keep
// actions cheap. Caller must hold s.mu.
func (s *Session) detectChallengeDOMLocked(t *tab) string {
	var hit string
	_ = s.run(t, chromedp.Evaluate(`(function(){var s=document.querySelector('.g-recaptcha,.h-captcha,iframe[src*="recaptcha"],iframe[src*="hcaptcha"],#cf-challenge-running,.cf-turnstile');return s?'captcha':'';})()`, &hit))
	if hit == "captcha" {
		return "CAPTCHA detected (reCAPTCHA/hCaptcha/Turnstile). Needs a solver (start the server with --captcha-solver-key) or a human to solve it."
	}
	return ""
}

// waitForChallengeClearLocked polls the page title+url for up to max, returning
// true once it's no longer a managed-challenge interstitial. Cloudflare/DataDome
// challenges often auto-clear after a few seconds when the fingerprint passes,
// so navigate waits for the real page before surfacing a CHALLENGE to the agent.
// Caller must hold s.mu.
func (s *Session) waitForChallengeClearLocked(t *tab, max time.Duration) bool {
	deadline := time.Now().Add(max)
	for time.Now().Before(deadline) {
		time.Sleep(time.Second)
		var title, loc string
		if err := s.run(t, chromedp.Title(&title), chromedp.Location(&loc)); err != nil {
			return false
		}
		if detectChallengeTitleURL(loc, title) == "" {
			return true
		}
	}
	return false
}

// moveMousePath moves the real mouse from a random off-target point to (x, y)
// along a short jittered smoothstep path, dispatching several mouseMoved
// events with small variable delays. Real humans emit a noisy mouse-move
// stream before acting; a single mouseMoved (or none) is a bot tell. Sub-pixel
// jitter + variable timing add input entropy that defeats the "no mouse-move
// before action" and the integer-pixel/identical-timing checks.
func moveMousePath(ctx context.Context, x, y float64) error {
	r := rand.New(rand.NewSource(time.Now().UnixNano()))
	startX := x + (r.Float64()*240 - 120)
	startY := y + (r.Float64()*240 - 120)
	steps := 5 + r.Intn(3) // 5-7
	for i := 0; i <= steps; i++ {
		frac := float64(i) / float64(steps)
		e := frac * frac * (3 - 2*frac) // smoothstep ease
		px := startX + (x-startX)*e + (r.Float64()-0.5)*2.5
		py := startY + (y-startY)*e + (r.Float64()-0.5)*2.5
		if err := input.DispatchMouseEvent(input.MouseMoved, px, py).Do(ctx); err != nil {
			return err
		}
		time.Sleep(time.Duration(8+r.Intn(18)) * time.Millisecond)
	}
	return nil
}
