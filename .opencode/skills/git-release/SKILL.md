---
name: git-release
description: Handle the full git release workflow for this project: commit pending changes, bump version, create annotated tag (vX.Y.Z), push commits and tags to origin, and trigger the GitHub Actions release pipeline. Use this skill whenever the user asks to make a release, tag a version, publish a new version, push a release, trigger CI release, or deploy to production. Also use when the user says "release this", "ship it", "cut a release", or asks about the release process.
---

# Git Release Skill - sing-box-operator

## Overview

This project uses **GitHub Actions** for release automation. The release pipeline (`.github/workflows/release.yml`) triggers on any tag matching `v[0-9]+.[0-9]+.[0-9]+` or `v[0-9]+.[0-9]+.[0-9]+-*`.

The pipeline builds multi-arch Docker images (amd64 + arm64), pushes a multi-arch manifest, packages + pushes a Helm chart to OCI registry, and creates a GitHub Release with auto-generated notes.

## Prerequisites

- Clean or intentionally dirty working tree (will be staged)
- Write access to the remote `origin`
- Latest tags fetched: `git fetch --tags`

## Release Workflow

### Step 1: Determine the new version

Check existing tags to determine the next version:

```bash
git tag --sort=-v:refname | head -5
```

This project uses **semantic versioning** (`vMAJOR.MINOR.PATCH`). The version number to use depends on the nature of changes:
- **patch bump** (default): bug fixes, minor improvements, docs — bump PATCH
- **minor bump**: new features, non-breaking changes — bump MINOR, reset PATCH to 0
- **major bump**: breaking changes — bump MAJOR, reset MINOR and PATCH to 0

For pre-release versions, append a suffix: `v0.3.19-rc1`, `v0.4.0-alpha1`, etc.

Ask the user what the new version should be, or suggest the next patch version based on the latest tag. Do NOT bump without confirming.

### Step 2: Commit pending changes

Before creating a tag, ensure all changes are committed. The release tag should point to a known commit.

```bash
# Check what's pending
git status --short

# Stage all changes
git add -A

# Commit with a release message
git commit -m "chore(release): prepare v<VERSION>"
```

If the working tree is completely clean (nothing to commit), skip this step.

### Step 3: Create and push the tag

Create an **annotated** tag (not lightweight) so the tag carries a message. This matches git best practices and ensures the tag is visible in `git describe`.

```bash
# Create annotated tag with version message
git tag -a v<VERSION> -m "Release v<VERSION>"

# Push the commit (if any) and the tag
git push origin HEAD --follow-tags
```

If commits were already pushed, just push the tag:

```bash
git push origin v<VERSION>
```

### Step 4: Verify the release

After pushing, the GitHub Actions workflow triggers automatically. Tell the user to monitor:

```
https://github.com/<owner>/sing-box-operator/actions
```

The release workflow runs these jobs:
1. `docker-amd64` — builds and pushes amd64 image
2. `docker-arm64` — builds and pushes arm64 image
3. `docker-manifest` — creates multi-arch manifest (needs both arch jobs)
4. `helm` — packages and pushes Helm chart to OCI registry
5. `release` — creates GitHub Release with auto-generated notes

Key outputs:
- Docker image: `ghcr.io/<owner>/sing-box-operator:<version>`
- Multi-arch tags (stable only): `ghcr.io/<owner>/sing-box-operator:<MAJOR>`, `<MAJOR.MINOR>`
- Helm chart: `oci://ghcr.io/<owner>/charts/sing-box-operator --version <version>`
- GitHub Release: auto-generated release notes with download links

## Tag Naming Convention

| Pattern | Example | Triggers Release? |
|---|---|---|
| `vMAJOR.MINOR.PATCH` | `v0.3.19` | ✅ Yes |
| `vMAJOR.MINOR.PATCH-suffix` | `v0.3.19-rc1` | ✅ Yes |
| Anything else | `test`, `release` | ❌ No |

## Commands to NEVER run

- **NEVER** delete existing tags without user's explicit request
- **NEVER** force-push tags (`git push --force --tags`)
- **NEVER** skip CI with `[skip ci]` in release commits
- **NEVER** use lightweight tags for releases — always annotated (`-a`)

## Rollback (if needed)

If a release needs to be rolled back:

```bash
# Delete the remote tag
git push origin --delete v<VERSION>

# Delete the local tag
git tag -d v<VERSION>
```

Note: this does NOT delete the GitHub Release or Docker images — those must be handled manually via GitHub UI / gh CLI.
