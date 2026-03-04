# forge

A CLI tool that scaffolds projects from your private GitHub template repos.
Always fast (local cache), always fresh (auto-syncs when online).

---

## Installation

```bash
curl -fsSL https://github.com/SimpleNiQue/forge/releases/latest/download/install.sh | bash
```

---

## First-time setup

After installing, log in once with your GitHub PAT:

```bash
forge auth login
```

**To create a token:**
1. Go to: https://github.com/settings/tokens/new
2. Select scope: `repo`
3. Paste it when prompted

---

## Usage

```bash
# Scaffold a new project
forge start --template django --name my_api
forge start --template golang --name my_service
forge start --template nextjs --name my_app

# Shorthand flags work too
forge start -t fastapi -n my_project

# List available templates
forge templates

# Update forge to the latest version
forge update

# Auth management
forge auth login
forge auth logout
forge auth status

# Cache management
forge sync                           # sync all templates
forge sync --template django         # sync one template
forge cache info                     # show cache status
forge cache clear                    # clear all caches
forge cache clear --template golang  # clear one cache
```

---

## Available templates

| Template  | Repo                                            |
|-----------|-------------------------------------------------|
| `django`  | github.com/SimpleNiQue/django-backend-template  |
| `golang`  | github.com/SimpleNiQue/golang-backend-template  |
| `nextjs`  | github.com/SimpleNiQue/nextjs-template          |
| `fastapi` | github.com/SimpleNiQue/fastapi-backend-template |

---

## How it works

```
First run (online)   → downloads template repo → caches locally → scaffolds
Later runs (online)  → checks GitHub for changes → updates only changed files → scaffolds
Later runs (offline) → uses local cache directly → scaffolds instantly
```

Each template has its own independent cache:
- **Linux**: `~/.cache/forge/<template>/`
- **macOS**: `~/Library/Caches/forge/<template>/`
- **Windows**: `%APPDATA%\forge\<template>\`

---

## Adding a new template

1. Create a new private repo under `SimpleNiQue/` with your boilerplate
2. Add one line to `main.go`:

```bash
var templates = map[string]string{
"django":  "django-backend-template",
"golang":  "golang-backend-template",
"nextjs":  "nextjs-template",
"fastapi": "fastapi-backend-template",
"react":   "react-template",           // ← add this
}
```

3. Tag a new release — GitHub Actions handles the rest:

```bash
git tag v1.1.0
git push origin v1.1.0
```

---

## Releasing a new version

```bash
git tag v1.0.0
git push origin v1.0.0
```

GitHub Actions automatically builds binaries for Linux, macOS (Intel + Apple Silicon),
and Windows and attaches them to the release.

---

## Repo structure

```
forge/
├── main.go                      # entire CLI — single file
├── go.mod
├── install.sh                   # installer (also lives as a public gist)
└── .github/
    └── workflows/
        └── release.yml          # builds & releases on git tag push
```