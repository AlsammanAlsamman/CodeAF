package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/Agent-Field/codeaf/internal/store"
)

// atlasNode and atlasEdge are the whole interface between Go and the page's
// JS: plain data, no behavior crosses the boundary. The page never re-derives
// class or rate — it draws exactly what CompetenceMap already computed.
type atlasNode struct {
	ID          string   `json:"id"`
	Kind        string   `json:"kind"`
	Class       string   `json:"class"`
	Samples     int      `json:"samples"`
	SuccessRate float64  `json:"successRate"`
	Skills      []string `json:"skills"`
}

type atlasEdge struct {
	Source string `json:"source"`
	Target string `json:"target"`
}

type atlasData struct {
	Nodes       []atlasNode `json:"nodes"`
	Edges       []atlasEdge `json:"edges"`
	GeneratedAt string      `json:"generatedAt"`
}

// renderCompetenceAtlas is the visual reader `codeaf competence --html` adds
// beside the existing text report: the same CompetenceMap, drawn as a graph
// instead of grouped lines. Scopes are the nodes (territory and profile
// alike); territory scopes gain an edge wherever store.ScopesTouch already
// says two scopes are the same cluster — the repo/file nesting the
// competence map itself counts evidence by. A scope's installed skills ride
// along as a label, never a separately-ranked row: RankSkills is the ordering
// that means something; this page is for a human to look the shelf over, not
// for the model to read.
func renderCompetenceAtlas(competence store.CompetenceMap, now time.Time) string {
	nodes := make([]atlasNode, 0, len(competence.Scopes))
	for _, scope := range competence.Scopes {
		nodes = append(nodes, atlasNode{
			ID:          scope.Scope,
			Kind:        string(scope.Kind),
			Class:       string(scope.Class),
			Samples:     scope.Samples,
			SuccessRate: scope.SuccessRate,
			Skills:      append([]string(nil), scope.InstalledSkills...),
		})
	}
	edges := make([]atlasEdge, 0)
	for i := 0; i < len(competence.Scopes); i++ {
		left := competence.Scopes[i]
		if left.Kind != store.CompetenceTerritory {
			continue
		}
		for j := i + 1; j < len(competence.Scopes); j++ {
			right := competence.Scopes[j]
			if right.Kind != store.CompetenceTerritory {
				continue
			}
			if store.ScopesTouch(left.Scope, right.Scope) {
				edges = append(edges, atlasEdge{Source: left.Scope, Target: right.Scope})
			}
		}
	}
	data := atlasData{Nodes: nodes, Edges: edges, GeneratedAt: now.UTC().Format(time.RFC3339)}
	payload, err := json.Marshal(data)
	if err != nil {
		// Every field above is a plain string, float, or int slice — Marshal
		// cannot fail on this shape. A future field that could change that
		// must not ship silently, so this stays a hard failure, not a blank
		// page.
		panic(fmt.Sprintf("render competence atlas: %v", err))
	}
	page := strings.ReplaceAll(atlasHTMLTemplate, "__ATLAS_DATA__", string(payload))
	return page
}

// The palette below is docs/DESIGN-LANGUAGE.md's own dark ladder — the same
// ink, accent, and signal hues every other codeaf surface draws with — so
// this page reads as the product, not a bolted-on dashboard. No CDN script or
// stylesheet: the file this writes must open offline, on any machine, same
// as everything else codeaf ships.
const atlasHTMLTemplate = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<title>codeaf — skill atlas</title>
<meta name="viewport" content="width=device-width, initial-scale=1">
<style>
  :root {
    --ground: #101014;
    --ink: #D8DEE9;
    --muted: #7FA6C9;
    --dim: #6B7280;
    --accent: #9DC3E6;
    --strong: #A3BE8C;
    --frontier: #EBCB8B;
    --weak: #C67173;
    --stale: #6B7280;
  }
  * { box-sizing: border-box; }
  html, body {
    margin: 0; height: 100%; background: var(--ground); color: var(--ink);
    font: 13px/1.5 -apple-system, "Segoe UI", ui-sans-serif, system-ui, sans-serif;
  }
  #atlas { position: fixed; inset: 0; display: block; }
  #hud {
    position: fixed; top: 0; left: 0; right: 0; padding: 14px 18px;
    display: flex; align-items: baseline; gap: 18px; pointer-events: none;
  }
  #hud h1 { font-size: 13px; font-weight: 600; margin: 0; color: var(--ink); letter-spacing: .02em; }
  #hud .stamp { color: var(--dim); font-size: 11px; }
  #legend {
    position: fixed; bottom: 16px; left: 18px; display: flex; gap: 14px;
    color: var(--muted); font-size: 11px;
  }
  #legend span { display: inline-flex; align-items: center; gap: 6px; }
  #legend i { width: 8px; height: 8px; border-radius: 50%; display: inline-block; }
  #panel {
    position: fixed; top: 0; right: 0; bottom: 0; width: 300px;
    background: rgba(16,16,20,.92); padding: 20px; overflow-y: auto;
    transform: translateX(100%); transition: transform .15s ease-out;
  }
  #panel.open { transform: translateX(0); }
  #panel h2 { font-size: 14px; margin: 0 0 4px; color: var(--ink); word-break: break-word; }
  #panel .class { font-size: 11px; margin-bottom: 14px; }
  #panel dl { margin: 0 0 16px; }
  #panel dt { color: var(--dim); font-size: 11px; }
  #panel dd { margin: 0 0 8px; color: var(--muted); }
  #panel ul { margin: 0; padding-left: 16px; color: var(--ink); }
  #panel li { margin: 2px 0; }
  #panel .empty { color: var(--dim); font-style: italic; }
  #hint { position: fixed; bottom: 16px; right: 18px; color: var(--dim); font-size: 11px; }
</style>
</head>
<body>
<div id="hud"><h1>skill atlas</h1><span class="stamp"></span></div>
<canvas id="atlas"></canvas>
<div id="legend">
  <span><i style="background:var(--strong)"></i>strong</span>
  <span><i style="background:var(--frontier)"></i>frontier</span>
  <span><i style="background:var(--weak)"></i>weak</span>
  <span><i style="background:var(--stale)"></i>stale</span>
</div>
<div id="hint">click a scope for its skills</div>
<div id="panel">
  <h2 id="panel-scope"></h2>
  <div class="class" id="panel-class"></div>
  <dl>
    <dt>samples</dt><dd id="panel-samples"></dd>
    <dt>success rate</dt><dd id="panel-rate"></dd>
  </dl>
  <div id="panel-skills"></div>
</div>
<script>
(function () {
  "use strict";
  var data = __ATLAS_DATA__;
  document.querySelector("#hud .stamp").textContent = "as of " + data.generatedAt;

  var classColor = { strong: "#A3BE8C", frontier: "#EBCB8B", weak: "#C67173", stale: "#6B7280" };
  function colorFor(cls) { return classColor[cls] || "#7FA6C9"; }
  function radiusFor(samples) {
    var r = 7 + Math.log2(samples + 1) * 4;
    return Math.max(7, Math.min(r, 30));
  }

  var byID = {};
  var nodes = data.nodes.map(function (n) {
    var node = {
      id: n.id, kind: n.kind, cls: n.class, samples: n.samples,
      rate: n.successRate, skills: n.skills || [],
      x: Math.random() * 800, y: Math.random() * 600, vx: 0, vy: 0,
      r: radiusFor(n.samples),
    };
    byID[n.id] = node;
    return node;
  });
  var edges = data.edges
    .map(function (e) { return { a: byID[e.source], b: byID[e.target] }; })
    .filter(function (e) { return e.a && e.b; });

  var canvas = document.getElementById("atlas");
  var ctx = canvas.getContext("2d");
  var width = 0, height = 0, dpr = window.devicePixelRatio || 1;
  function resize() {
    width = window.innerWidth; height = window.innerHeight;
    canvas.width = width * dpr; canvas.height = height * dpr;
    canvas.style.width = width + "px"; canvas.style.height = height + "px";
    ctx.setTransform(dpr, 0, 0, dpr, 0, 0);
  }
  window.addEventListener("resize", resize);
  resize();

  // A small, dependency-free force layout: pairwise repulsion (nodes push
  // apart), spring attraction along edges (touching scopes pull together),
  // and a gentle pull toward the canvas center so an unconnected node
  // doesn't drift off screen. Good enough for the tens of scopes a shelf
  // realistically holds; this is a picture for a human, not a simulator.
  function step() {
    var cx = width / 2, cy = height / 2;
    for (var i = 0; i < nodes.length; i++) {
      var n = nodes[i];
      n.vx += (cx - n.x) * 0.0025;
      n.vy += (cy - n.y) * 0.0025;
      for (var j = i + 1; j < nodes.length; j++) {
        var m = nodes[j];
        var dx = n.x - m.x, dy = n.y - m.y;
        var distSq = dx * dx + dy * dy || 0.01;
        var dist = Math.sqrt(distSq);
        var push = Math.min(2400 / distSq, 6);
        var fx = (dx / dist) * push, fy = (dy / dist) * push;
        n.vx += fx; n.vy += fy;
        m.vx -= fx; m.vy -= fy;
      }
    }
    for (var e = 0; e < edges.length; e++) {
      var a = edges[e].a, b = edges[e].b;
      var dx = b.x - a.x, dy = b.y - a.y;
      var dist = Math.sqrt(dx * dx + dy * dy) || 0.01;
      var target = 140;
      var pull = (dist - target) * 0.01;
      var fx = (dx / dist) * pull, fy = (dy / dist) * pull;
      a.vx += fx; a.vy += fy;
      b.vx -= fx; b.vy -= fy;
    }
    for (var k = 0; k < nodes.length; k++) {
      var node = nodes[k];
      node.vx *= 0.82; node.vy *= 0.82;
      node.x += node.vx; node.y += node.vy;
      var r = node.r + 4;
      node.x = Math.max(r, Math.min(width - r, node.x));
      node.y = Math.max(r, Math.min(height - r, node.y));
    }
  }

  function draw() {
    ctx.clearRect(0, 0, width, height);
    ctx.strokeStyle = "rgba(157,195,230,.18)";
    ctx.lineWidth = 1;
    edges.forEach(function (e) {
      ctx.beginPath();
      ctx.moveTo(e.a.x, e.a.y);
      ctx.lineTo(e.b.x, e.b.y);
      ctx.stroke();
    });
    nodes.forEach(function (n) {
      ctx.beginPath();
      ctx.arc(n.x, n.y, n.r, 0, Math.PI * 2);
      ctx.fillStyle = colorFor(n.cls);
      ctx.globalAlpha = n.id === hovered ? 1 : 0.85;
      ctx.fill();
      ctx.globalAlpha = 1;
      if (n === selected) {
        ctx.lineWidth = 2;
        ctx.strokeStyle = "#D8DEE9";
        ctx.stroke();
      }
      ctx.fillStyle = "#D8DEE9";
      ctx.font = "11px -apple-system, 'Segoe UI', sans-serif";
      ctx.textAlign = "center";
      ctx.fillText(n.id, n.x, n.y + n.r + 13);
    });
  }

  var selected = null, hovered = null;
  var settleFrames = 260;
  function loop() {
    if (settleFrames > 0) { step(); settleFrames--; }
    draw();
    requestAnimationFrame(loop);
  }
  loop();

  function nodeAt(px, py) {
    for (var i = nodes.length - 1; i >= 0; i--) {
      var n = nodes[i];
      var dx = px - n.x, dy = py - n.y;
      if (dx * dx + dy * dy <= n.r * n.r) return n;
    }
    return null;
  }

  var panel = document.getElementById("panel");
  function openPanel(n) {
    selected = n;
    document.getElementById("panel-scope").textContent = n.id;
    var cls = document.getElementById("panel-class");
    cls.textContent = n.cls + (n.kind === "profile" ? " · model profile" : "");
    cls.style.color = colorFor(n.cls);
    document.getElementById("panel-samples").textContent = n.samples;
    document.getElementById("panel-rate").textContent =
      n.samples > 0 ? Math.round(n.rate * 100) + "%" : "no runs yet";
    var skillsHost = document.getElementById("panel-skills");
    skillsHost.innerHTML = "";
    if (n.skills.length === 0) {
      var empty = document.createElement("div");
      empty.className = "empty";
      empty.textContent = "no installed skills touch this scope";
      skillsHost.appendChild(empty);
    } else {
      var list = document.createElement("ul");
      n.skills.forEach(function (skill) {
        var item = document.createElement("li");
        item.textContent = skill;
        list.appendChild(item);
      });
      skillsHost.appendChild(list);
    }
    panel.classList.add("open");
  }

  canvas.addEventListener("click", function (evt) {
    var rect = canvas.getBoundingClientRect();
    var n = nodeAt(evt.clientX - rect.left, evt.clientY - rect.top);
    if (n) { openPanel(n); } else { selected = null; panel.classList.remove("open"); }
  });
  canvas.addEventListener("mousemove", function (evt) {
    var rect = canvas.getBoundingClientRect();
    var n = nodeAt(evt.clientX - rect.left, evt.clientY - rect.top);
    hovered = n ? n.id : null;
    canvas.style.cursor = n ? "pointer" : "default";
  });
})();
</script>
</body>
</html>
`
