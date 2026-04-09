# Frontend Context — Clockwork Manifold

> Project-specific frontend conventions. Loaded by the frontend agent role when working in this project.

## Current State

Active development. React SPA with Vite build, shadcn/ui components, Tailwind CSS 4.

## Stack

- **Framework:** React 19 (SPA, no Next.js)
- **Build:** Vite with `@vitejs/plugin-react`
- **Language:** TypeScript (strict)
- **Styling:** Tailwind CSS v4 via `@tailwindcss/vite`
- **Components:** shadcn/ui v4
- **Icons:** lucide-react
- **Routing:** react-router-dom v7
- **Module type:** ESM (`"type": "module"`)

> **Note:** The `react` role assumes Next.js App Router. Ignore that — this is a pure Vite SPA. No server components, no App Router, no `"use client"` directives.

## Project Structure

```
apps/gui/
├── package.json
├── vite.config.ts
├── components.json        # shadcn config
└── src/
    ├── ...
```

## Patterns

- **shadcn first** — check `src/components/ui/` before building custom. Install missing with `npx shadcn@latest add <component>`.
- **Tailwind for all styling** — no inline `style={{}}` except dynamic values.
- **Named exports** for all components.
