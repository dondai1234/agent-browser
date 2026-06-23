// Package main is the task-success-per-token benchmark for agent-browser.
//
// Snapshot size is the easy number; task success per token is the honest one.
// This harness runs a set of multi-step browser tasks (login, search+extract,
// form fill+select+submit, multi-page nav, lazy-list scroll) against one or more
// MCP browser tools and reports, per task + per tool:
//
//   - success: did the task's end-state assertion pass?
//   - tokens:  total tool I/O chars (sent args JSON + returned text) / 4, the
//              cost an LLM agent burns on tool round-trips for that task
//
// The "agent" is a deterministic scripted policy, NOT an LLM. This is deliberate:
// token cost is the tool surface (inputs + outputs), independent of which model
// drives it, so a fixed script makes the comparison fair + reproducible. A real
// LLM agent would add its own reasoning tokens on top, but those scale with the
// tool I/O it sees, so tool-I/O-per-success is the right primitive to compare.
//
// Tasks run against LOCAL HTTP fixtures (no network flakiness). Each task has a
// script per tool (their surfaces differ); the runner just calls CallTool and
// sums char counts.
package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
)

// fixtures is one local HTTP server serving all task pages. Each route is a tiny
// client-side page (vanilla JS, no framework) so behavior is deterministic.
type fixtures struct {
	*httptest.Server
	base string
}

func newFixtures() *fixtures {
	mux := http.NewServeMux()
	mux.HandleFunc("/", serveFixture)
	srv := httptest.NewServer(mux)
	return &fixtures{Server: srv, base: srv.URL}
}

// serveFixture routes by path. Pages use client-side JS so no server state is
// needed (deterministic, reset per navigation).
func serveFixture(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	switch r.URL.Path {
	case "/login":
		fmt.Fprint(w, `<!doctype html><html><head><title>Login</title></head><body>
		<h1>Sign in</h1>
		<form id="f" onsubmit="return doLogin(event)">
			<label for="u">Username</label>
			<input id="u" name="username" type="text"><br>
			<label for="p">Password</label>
			<input id="p" name="password" type="password"><br>
			<button type="submit">Sign in</button>
		</form>
		<script>
		function doLogin(e){
			e.preventDefault();
			var u=document.getElementById('u').value;
			var p=document.getElementById('p').value;
			if(u&&p){ location.href="/dashboard?u="+encodeURIComponent(u); }
			return false;
		}
		</script></body></html>`)
	case "/dashboard":
		u := r.URL.Query().Get("u")
		if u == "" {
			u = "user"
		}
		fmt.Fprintf(w, `<!doctype html><html><head><title>Dashboard</title></head><body>
		<h1>Welcome %s</h1>
		<p>You are signed in.</p>
		</body></html>`, u)
	case "/search":
		fmt.Fprint(w, `<!doctype html><html><head><title>Search</title></head><body>
		<h1>Search products</h1>
		<input id="q" name="q" type="search" placeholder="Search">
		<button id="go">Go</button>
		<div id="results"></div>
		<script>
		var items=["Running shoes","Leather shoes","Boot shoes","Sandal shoes","Slip-on shoes","Hiking shoes","Dress shoes"];
		function render(q){
			var d=document.getElementById('results');
			d.innerHTML='';
			items.forEach(function(it){ if(it.toLowerCase().indexOf(q.toLowerCase())>=0){
				var p=document.createElement('p'); p.className='result'; p.textContent=it; d.appendChild(p);
			}});
			var c=document.createElement('p'); c.id='count'; c.textContent=d.querySelectorAll('.result').length+' results';
			d.appendChild(c);
		}
		document.getElementById('go').addEventListener('click',function(){render(document.getElementById('q').value);});
		document.getElementById('q').addEventListener('keydown',function(e){if(e.key==='Enter')render(document.getElementById('q').value);});
		</script></body></html>`)
	case "/form":
		fmt.Fprint(w, `<!doctype html><html><head><title>Checkout</title></head><body>
		<h1>Checkout</h1>
		<form id="f" onsubmit="return submitForm(event)">
			<label for="name">Full name</label>
			<input id="name" name="name" type="text"><br>
			<label for="email">Email</label>
			<input id="email" name="email" type="email"><br>
			<label for="addr">Address</label>
			<input id="addr" name="address" type="text"><br>
			<label for="plan">Plan</label>
			<select id="plan" name="plan"><option value="free">Free</option><option value="pro">Pro</option><option value="team">Team</option></select><br>
			<button type="submit">Place order</button>
		</form>
		<p id="out"></p>
		<script>
		function submitForm(e){
			e.preventDefault();
			var n=document.getElementById('name').value;
			var em=document.getElementById('email').value;
			var a=document.getElementById('addr').value;
			var p=document.getElementById('plan').value;
			document.getElementById('out').textContent='Submitted: '+n+' / '+em+' / '+a+' / '+p;
			return false;
		}
		</script></body></html>`)
	case "/page1":
		fmt.Fprint(w, `<!doctype html><html><head><title>Page 1</title></head><body><h1>Step 1</h1><a href="/page2">Next</a></body></html>`)
	case "/page2":
		fmt.Fprint(w, `<!doctype html><html><head><title>Page 2</title></head><body><h1>Step 2</h1><a href="/page3">Next</a></body></html>`)
	case "/page3":
		fmt.Fprint(w, `<!doctype html><html><head><title>Page 3</title></head><body><h1>Step 3 - done</h1></body></html>`)
	case "/list":
		fmt.Fprint(w, `<!doctype html><html><head><title>List</title>
		<style>p.item{height:220px;margin:0;border-bottom:1px solid #ccc}</style>
		</head><body>
		<h1>Items</h1>
		<div id="list"></div>
		<script>
		var n=1;
		function add(k){ for(var i=0;i<k;i++){ var p=document.createElement('p'); p.className='item'; p.textContent='Item '+(n++); document.getElementById('list').appendChild(p); } }
		add(5);
		window.addEventListener('scroll', function(){
			if((window.innerHeight+window.scrollY) >= document.body.scrollHeight-80){ add(5); }
		});
		</script></body></html>`)
	default:
		http.NotFound(w, r)
	}
}

// taskResult is one task's outcome for one tool.
type taskResult struct {
	Task    string
	Tool    string
	Success bool
	Chars   int     // total tool I/O chars (sent args + returned text)
	Tokens  float64 // chars/4 (rough LLM token estimate)
	Err     string  // non-empty if the script itself errored (distinct from assertion fail)
	Steps   int
}

func (r taskResult) String() string {
	status := "FAIL"
	if r.Success {
		status = "ok"
	}
	if r.Err != "" {
		status = "ERR:" + r.Err
	}
	return fmt.Sprintf("%-22s %-14s %s  steps=%d chars=%d tokens=%.0f", r.Task, r.Tool, status, r.Steps, r.Chars, r.Tokens)
}

// report prints a side-by-side table of per-task results + per-tool totals.
func report(results []taskResult) {
	var b strings.Builder
	b.WriteString("\n=== task success per token ===\n")
	b.WriteString(fmt.Sprintf("%-22s %-14s %-10s %6s %8s %6s\n", "task", "tool", "result", "steps", "chars", "tokens"))
	b.WriteString(strings.Repeat("-", 70) + "\n")
	totals := map[string]*struct{ succ, chars int; tasks int }{}
	for _, r := range results {
		b.WriteString(fmt.Sprintf("%-22s %-14s %-10s %6d %8d %6.0f\n", r.Task, r.Tool, boolStr(r.Success, r.Err), r.Steps, r.Chars, r.Tokens))
		t, ok := totals[r.Tool]
		if !ok {
			t = &struct{ succ, chars int; tasks int }{}
			totals[r.Tool] = t
		}
		t.tasks++
		if r.Success {
			t.succ++
		}
		t.chars += r.Chars
	}
	b.WriteString(strings.Repeat("-", 70) + "\n")
	// per-tool summary line: success rate + total tokens + tokens-per-success.
	for _, tool := range toolOrder {
		t, ok := totals[tool]
		if !ok {
			continue
		}
		rate := 0
		if t.tasks > 0 {
			rate = t.succ * 100 / t.tasks
		}
		tokens := float64(t.chars) / 4
		b.WriteString(fmt.Sprintf("%-22s %-14s %d/%d (%d%%)        %8d %6.0f tokens\n", "TOTAL", tool, t.succ, t.tasks, rate, t.chars, tokens))
	}
	fmt.Print(b.String())
}

func boolStr(ok bool, errStr string) string {
	if errStr != "" {
		return "ERR"
	}
	if ok {
		return "ok"
	}
	return "FAIL"
}

var toolOrder []string // filled as runners register, keeps table order stable
