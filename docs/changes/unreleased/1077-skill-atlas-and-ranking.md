---
kind: added
title: The competence map draws itself, and ranks a scope's skills by the evidence it already has
pr: 1077
surface: [resident, build]
invalidates:
  - "CompetenceMap had one reader: writeCompetence's grouped text report. It now has two — `codeaf competence --html <path>` renders the same map as an interactive atlas (docs/DESIGN-LANGUAGE.md's own dark palette, a force-directed graph, click a scope for its installed skills) instead of printing the text report. `store.scopesTouch`, previously private to addTerritoryCompetence, is also `store.ScopesTouch`: the atlas clusters territory scopes by the exact rule the competence map already counts evidence by, not a second guess at what 'related' means."
  - "A scope with more than one installed skill had no ordering at all beyond alphabetical. `CompetenceMap.RankSkills()` orders scopes by class (strong, then frontier, then stale, then weak), then success rate, then sample count — a small, cheap statistic over evidence the journal already produced, not a new one. It does not order the skills within one scope against each other: no outcome is yet attributed to one skill rather than another sharing a scope, so pretending otherwise would be fabricated evidence, not measured."
---

docs/LEARNING.md 3.3 already named the gap: skills exist on the shelf, and
nothing reads them for anything beyond an alphabetical PATH listing — "the
contract writer consumes skills" is written down as a thing that should
happen and doesn't yet. RankSkills is the first reader that could feed a
future contract writer or resident scope selection a real preference between
two skills that both claim the same territory, and it is built entirely from
CompetenceMap's existing derivation: no new store table, no new event kind,
no new trial ledger. That last part was a deliberate no — Fact.Uses and
Fact.LastUsed are documented ("Nothing ranks on them... a value the journal
cannot rebuild may inform a human reading the table, never a retrieval
deciding what a model sees") precisely to keep popularity out of a decision
that must stay execution-evidenced, and a second, parallel record of "which
skill actually ran and how it went" would be exactly the second truth
docs/LEARNING.md 3.13 rules out before it existed.

The atlas is the other half: a human's way to look the same shelf over
without reading rows of grouped text. Territory scopes that touch each other
(the same repo/file nesting the competence map already keys evidence by)
draw as a connected cluster; a scope's class colors its node using the
product's own dark palette, so this reads as codeaf, not a bolted-on
dashboard; clicking a scope opens its sample count, success rate, and
installed skills. It ships as one self-contained HTML file — no CDN script,
open it offline on any machine, same as everything else codeaf writes to
disk.
