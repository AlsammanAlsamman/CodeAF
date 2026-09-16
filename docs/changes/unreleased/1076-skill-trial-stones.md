---
kind: added
title: A green check.sh no longer promotes a skill by itself — it has to survive being sabotaged first
pr: 1076
surface: [resident]
invalidates:
  - "installSkillTrial (skills.go) activated a skill candidate once its own check.sh passed a single time, in one clean, unused, empty directory (runSkillCheck). That is now necessary but not sufficient: the trial also runs runSkillStones, which re-runs the same check.sh in a directory it has already used, in a directory pre-seeded with unrelated files, and with unrelated environment variables set. Any one of those going red supersedes the candidate with the stone that caught it, exactly like a red check.sh always has."
---

A candidate's check.sh is written by whatever taught it the skill — it proves the
happy path it was written against, nothing more. That leaves a real gap: a check
that only ever runs once, in one directory it will never see again, has not shown
the skill survives the second run, a workspace that already has files in it, or an
environment it didn't expect. SkillJab (github.com/AlsammanAlsamman/skilljab)
names this class of bug precisely for data pipelines — a result silently moves and
nothing fires — and the fix generalizes directly: throw a few realistic, external
perturbations at the same check, and only trust a skill that stays green through
all of them. The three stones here are cheap and mechanical, not authored
knowledge about any particular skill, so this stays the trial's own evaluator —
execution, never self-judgment — same as the base check it now follows.
