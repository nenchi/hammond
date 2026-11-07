"""Runs on every push to main (docker-publish.yml, release job), normally after a Renovate merge.

Compares the current main with the last v* release tag. If a base image (Dockerfile FROM), a Go module
(server/go.mod) or an npm package (ui/package.json or ui/package-lock.json) changed since then: bump the
patch version in VERSION (skipping tags that already exist), commit, tag, write release-notes.md and
version=X.Y.Z to $GITHUB_OUTPUT. Pushing and the GitHub Release are done by the workflow.
Anything else (docs, workflows, tests): no release.
Comparing with the last release, not HEAD~1, makes several quick merges one release instead of a race.
"""
import json, os, re, subprocess, sys


def git(*args):
    return subprocess.run(["git", *args], check=True, capture_output=True, text=True).stdout


BASE = git("describe", "--tags", "--abbrev=0", "--match", "v[0-9]*", "HEAD").strip()


def old(path):
    try:
        return git("show", f"{BASE}:{path}")
    except subprocess.CalledProcessError:
        return ""


def new(path):
    return open(path).read() if os.path.exists(path) else ""


def diff(name, a, b):
    """'Bump x 1 → 2' lines for every key whose value changed between dicts a and b."""
    out = []
    for k in sorted(set(a) | set(b)):
        if a.get(k) == b.get(k):
            continue
        if k not in a:
            out.append(f"Add {name}{k} {b[k]}")
        elif k not in b:
            out.append(f"Remove {name}{k} {a[k]}")
        else:
            out.append(f"Bump {name}{k} {a[k]} → {b[k]}")
    return out


def froms(text):
    return {f"stage {i + 1} (`{m.split('@')[0].split(':')[0]}`)": f"`{m}`"
            for i, m in enumerate(re.findall(r"^FROM (\S+)", text, re.M))}


def gomod(text):
    # direct requirements only; indirect ones move with them
    return dict(re.findall(r"^\s+(\S+) (v\S+)$", text, re.M))


def npm(text):
    if not text:
        return {}
    p = json.loads(text)
    return {**p.get("dependencies", {}), **p.get("devDependencies", {})}


changes = (diff("base image ", froms(old("Dockerfile")), froms(new("Dockerfile")))
           + diff("Go module ", gomod(old("server/go.mod")), gomod(new("server/go.mod")))
           + diff("npm ", npm(old("ui/package.json")), npm(new("ui/package.json"))))
if not changes and old("ui/package-lock.json") != new("ui/package-lock.json"):
    changes.append("npm lock file maintenance (indirect dependency updates)")
if not changes and old("server/go.sum") != new("server/go.sum"):
    changes.append("Go indirect dependency updates")

if not changes:
    print(f"Dependencies unchanged since {BASE}: no release")
    sys.exit(0)

major, minor, patch = map(int, new("VERSION").strip().split("."))
taken = set(git("tag", "-l", "v*").split())
patch += 1
while f"v{major}.{minor}.{patch}" in taken:  # e.g. a tag left by a failed release
    patch += 1
ver = f"{major}.{minor}.{patch}"
open("VERSION", "w").write(ver + "\n")

git("add", "VERSION")
git("commit", "-q", "-m", f"release: v{ver} — {changes[0]}")
git("tag", "-a", f"v{ver}", "-m", f"v{ver}")

# merged PRs since the last release, by title: "Update dependency x to v2 (#5)"
prs = []
for sha in git("log", "--merges", "--format=%H", f"{BASE}..HEAD~1").split():
    subj, _, body = git("log", "-1", "--format=%s%n%b", sha).partition("\n")
    m = re.match(r"Merge pull request #(\d+)", subj)
    if m:
        prs.append(f"{(body.strip().splitlines() or [subj])[0]} (#{m.group(1)})")
src = ", ".join(prs) or git("log", "-1", "--format=%s", "HEAD~1").strip()

repo = os.environ.get("GITHUB_REPOSITORY", "nenchi/hammond")
open("release-notes.md", "w").write(
    "Automatic release after a dependency update. The API end-to-end test (setup, login, JWT checks, vehicles, "
    "fill-ups, stats) and the Cypress UI test passed before the merge.\n\n## Changed\n"
    + "".join(f"- {c}\n" for c in changes)
    + f"- Since {BASE}: {src}\n\nImage: `ghcr.io/{repo}:{ver}`\n")
with open(os.environ.get("GITHUB_OUTPUT", os.devnull), "a") as f:
    f.write(f"version={ver}\n")
print(f"Released v{ver}:\n" + "\n".join(changes))
