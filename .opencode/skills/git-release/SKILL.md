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
- `gh` CLI authenticated (for CI monitoring in Step 4). If unavailable, fall back to `curl` + GitHub API.
- `kubectl` and `helm` available on the local machine (for optional deployment in Step 5)

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

**Important: image tag has NO `v` prefix.** The release workflow extracts `VERSION="${GITHUB_REF_NAME#v}"` (strips the `v`), so:
- Git tag: `v0.4.0`
- Docker image tag: `0.4.0`
- Helm chart version: `0.4.0`

Never use `v0.4.0` as an image tag — it will fail with `manifest unknown`.

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

### Step 4: Monitor the GitHub Actions workflow

After pushing, the GitHub Actions release workflow triggers automatically. **Step 5 (deployment) MUST NOT run until Step 4 completes successfully.**

#### 4a: Locate the workflow run

Use the `gh` CLI to find the workflow run triggered by the new tag:

```bash
gh run list \
  --repo <owner>/<repo> \
  --workflow release.yml \
  --branch <TAG_NAME> \
  --limit 1 \
  --json databaseId,status,conclusion,url,headBranch \
  --jq '.[0]'
```

If `gh` is not authenticated, fall back to fetching the run via the GitHub API:

```bash
curl -s "https://api.github.com/repos/<owner>/<repo>/actions/workflows/release.yml/runs?branch=<TAG_NAME>&per_page=1" \
  | python3 -c "import sys,json; runs=json.load(sys.stdin); r=runs['workflow_runs'][0] if runs.get('workflow_runs') else None; print(json.dumps({'id':r['id'],'status':r['status'],'conclusion':r['conclusion'],'url':r['html_url']},indent=2) if r else 'NOT_FOUND')"
```

Report the run URL to the user:
```
https://github.com/<owner>/<repo>/actions/runs/<RUN_ID>
```

#### 4b: Monitor until completion

The release workflow has 5 jobs that run with dependencies:

| Job | Depends on | Typical Duration |
|---|---|---|
| `docker-amd64` | — | 5-10 min |
| `docker-arm64` | — | 5-10 min |
| `docker-manifest` | amd64 + arm64 | <1 min |
| `helm` | docker-manifest | 1-2 min |
| `release` | docker-manifest + helm | <1 min |

Poll for completion every 60 seconds. Use `gh`:

```bash
gh run watch <RUN_ID> --repo <owner>/<repo> --exit-status 2>&1
```

Or poll manually with the API:

```bash
# Poll loop — run every 60s until conclusion is non-null
while true; do
  STATUS=$(curl -s "https://api.github.com/repos/<owner>/<repo>/actions/runs/<RUN_ID>" \
    | python3 -c "import sys,json; r=json.load(sys.stdin); print(r.get('status',''), r.get('conclusion',''))")
  echo "$(date): $STATUS"
  CONCLUSION=$(echo "$STATUS" | awk '{print $2}')
  [ "$CONCLUSION" != "None" ] && [ -n "$CONCLUSION" ] && break
  sleep 60
done
```

**Tell the user that you are monitoring and will report back.** The polling loop can take 10-20 minutes — set appropriate timeouts.

#### 4c: Evaluate the result

**If conclusion is `success`:**

Report a summary table of all jobs:

```bash
gh run view <RUN_ID> --repo <owner>/<repo> --json jobs --jq '.jobs[] | {name, status, conclusion, url: .html_url}'
```

Then proceed to **Step 5**.

**If conclusion is `failure`:**

1. Do NOT proceed to Step 5. Deployment is BLOCKED.
2. Fetch the failed job logs to diagnose the root cause:

```bash
# List failed jobs
gh run view <RUN_ID> --repo <owner>/<repo> --json jobs \
  --jq '.jobs[] | select(.conclusion=="failure") | {name, url: .html_url, id: .databaseId}'

# Fetch logs for a specific failed job
gh run view --repo <owner>/<repo> --job <FAILED_JOB_ID> --log 2>&1 | tail -100
```

3. Analyze the logs. Common failure modes:
   - **Docker build failure**: Go compile errors, missing dependencies, Dockerfile syntax
   - **Docker push failure**: Authentication issue with `GITHUB_TOKEN` or `secrets.GITHUB_TOKEN`
   - **Helm package failure**: Chart.yaml syntax error, missing files
   - **Network / timeout**: GitHub Actions runner infrastructure issue — retry by re-pushing the tag

4. If the root cause is clear and fixable (e.g., a code issue in the repo), propose a fix and ask the user whether to patch and re-tag.
5. If the root cause is unclear or infrastructure-related, report the failure details to the user and suggest manual investigation.

**If `gh` CLI is not available or not authenticated**, fall back to the GitHub API via `curl` and the public runs endpoint. Note that without authentication, API rate limits (60 requests/hour) apply — poll less frequently (every 2-3 minutes).

### Step 5: Cluster detection and optional deployment

**GATE: Only run this step if Step 4 completed with `success`. If Step 4 failed, STOP — deployment is blocked until the CI pipeline is fixed.**

The GitHub Actions pipeline builds and pushes the Docker image and Helm chart. Step 4 ensures these artifacts exist before Step 5 attempts to pull them.

#### 5a: Detect available clusters

Check if `kubectl` and `helm` are installed and a cluster is reachable:

```bash
# Check kubectl
which kubectl && kubectl version --client --short 2>/dev/null

# Check helm
which helm && helm version --short 2>/dev/null

# Check for an active cluster
kubectl config current-context 2>/dev/null && kubectl cluster-info 2>/dev/null | head -1
```

If `kubectl config current-context` or `kubectl cluster-info` fails, there is no reachable cluster — skip the rest of Step 5 and inform the user.

#### 5b: Check existing deployment

Look for an existing Helm release of this operator:

```bash
helm list --all-namespaces 2>/dev/null | grep -i sing || echo "NO_HELM_RELEASE"
```

Also verify the namespace and controller:

```bash
kubectl get ns sing-box-operator 2>/dev/null
kubectl get deploy,sts -n sing-box-operator 2>/dev/null | head -10
```

If an existing deployment is found, note the current version (from `helm list` output) and the controller pod status.

#### 5c: Ask user about deployment

Using the `question` tool (NOT inline text), present a deployment choice.

**Scenario A — Existing deployment found:**

Ask the user:

> "Found existing Helm release `singbox-operator` at version `<CURRENT_VERSION>` in namespace `<NAMESPACE>`. The controller pod is `<STATUS>`.
>
> Cluster: `<CONTEXT>` at `<API_SERVER_URL>`
>
> New version: `<NEW_VERSION>`
>
> What should I do?"

Options:
- `"Deploy v<NEW_VERSION> via Helm upgrade (Recommended)"` — runs `helm upgrade` with the new version, reusing existing values
- `"Deploy fresh via Helm install"` — uninstalls existing release first, then installs fresh (⚠️ may cause downtime)
- `"Skip deployment for now"` — just report the cluster status, the user will deploy manually

**Scenario B — No existing deployment found:**

Ask the user:

> "No existing singbox-operator deployment found. Cluster `<CONTEXT>` is available at `<API_SERVER_URL>`.
>
> New version: `<NEW_VERSION>`
>
> Should I deploy?"

Options:
- `"Deploy v<NEW_VERSION> via Helm install"` — installs a fresh Helm release
- `"Skip deployment for now"` — just report the cluster status

**Scenario C — No cluster available:**

Just report: "No Kubernetes cluster reachable. Skipping deployment. You can deploy manually later with Helm."

Do NOT proceed with deployment without the user's explicit choice. Never deploy automatically.

#### 5d: Deploy the new version

**Always deploy from the OCI registry chart package**, NOT the local `charts/` directory. The CI pipeline bakes the correct image tag and CRDs into the packaged chart; the local directory has stale defaults (`tag: "latest"`, outdated CRDs).

**Helm upgrade (existing release):**
```bash
helm upgrade <RELEASE_NAME> \
  oci://ghcr.io/<owner>/charts/singbox-operator \
  --version <VERSION> \
  --namespace <NAMESPACE> \
  --reuse-values \
  --wait \
  --timeout 5m
```

**Helm install (fresh deployment):**
```bash
helm install <RELEASE_NAME> \
  oci://ghcr.io/<owner>/charts/singbox-operator \
  --version <VERSION> \
  --namespace <NAMESPACE> \
  --create-namespace \
  --wait \
  --timeout 5m
```

Where `<VERSION>` is the version WITHOUT `v` prefix (e.g. `0.4.0`, not `v0.4.0`).

After deployment, verify the rollout:

```bash
kubectl rollout status deployment/singbox-operator-controller-manager -n <NAMESPACE> --timeout=2m
kubectl get pods -n <NAMESPACE>
```

#### 5e: CRD upgrade caveat

**Helm does NOT upgrade CRDs in the `crds/` directory on `helm upgrade`** (by design, to prevent data loss). After a Helm upgrade that includes CRD schema changes, manually apply the new CRDs:

```bash
# Pull the chart and extract CRDs, OR apply from the local config/crd/bases/
kubectl apply -f config/crd/bases/
```

Verify the CRD schema updated:
```bash
kubectl get crd singboxnodes.singboxoperator.shlande.top -o json | jq '.spec.versions[0].schema.openAPIV3Schema.properties.spec.properties.tag'
```

Report the final status: new image tag, pod status, CRD schema, and any errors.

**Key details about this project's Helm chart:**
- Chart name: `singbox-operator`
- OCI registry path: `oci://ghcr.io/shlande/charts/singbox-operator`
- Default namespace: `sing-box-operator`
- Controller deployment: `singbox-operator-controller-manager`
- Image: `ghcr.io/shlande/singbox-operator:<tag>` (tag has NO `v` prefix)

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
- **NEVER** proceed to Step 5 (deployment) if Step 4 (CI monitoring) failed or is still running
- **NEVER** deploy without user's explicit confirmation in Step 5c
- **NEVER** deploy from the local `charts/` directory — always use the OCI registry package (local dir has stale `values.yaml` and CRDs)
- **NEVER** use `v0.4.0` as an image tag — image tags have NO `v` prefix (e.g. `0.4.0`)

## Rollback (if needed)

If a release needs to be rolled back:

```bash
# Delete the remote tag
git push origin --delete v<VERSION>

# Delete the local tag
git tag -d v<VERSION>
```

Note: this does NOT delete the GitHub Release or Docker images — those must be handled manually via GitHub UI / gh CLI.
