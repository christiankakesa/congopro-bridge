# AGENTS.md

Working rules for AI agents contributing to this repository.

## Skills

Apply the skill definitions in `.claude/skills/*/SKILL.md` as standing
working rules for all work in this repo:

- `golang` — Go review/writing standards and the default review output format.
- `database-skills` — schema, indexing, transaction, and migration guidance.
- `localization` — i18n/l10n architecture rules (see the caveat below).
- `web-design` — semantics, accessibility, security, responsive rules.

Adaptation notes, so the skills serve the product rather than fight it:

- The site's design language is restrained and Google-like, tuned for a
  low-bandwidth mobile audience. Follow `web-design`'s accessibility,
  semantics, and security rules strictly; treat its "playful glassmorphism"
  aesthetic as inspiration, not a mandate — clarity and page weight win.
- The app is French-first with hardcoded strings in the UI today; the
  `localization` skill's full regime (message catalogs, no hardcoded
  strings) is a tracked debt item, not the current state. New user-visible
  surfaces should stay consistent with the existing French-first approach
  until i18n is deliberately scoped (see docs/TODO.md) — but do not make
  the debt worse than the surrounding code already is.
