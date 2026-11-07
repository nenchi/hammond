"""Runs on every push to main (docker-publish.yml, release job), normally after a Renovate merge.

If a base image (Dockerfile FROM), a Go module (server/go.mod) or an npm package (ui/package.json or
ui/package-lock.json) changed in HEAD: bump the patch version in VERSION, commit, tag, write
release-notes.md and version=X.Y.Z to $GITHUB_OUTPUT. Pushing and the GitHub Release are done by the
workflow. Anything else (docs, workflows, tests): no release.
"""
import json, os, re, subprocess, sys


def git(*args):
    return subprocess.run(["git", *args], check=True, capture_output=True, text=True).stdout


def old(path):
    try:
        return git("show", f"HEAD~1:{path}")
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
    print("Dependencies unchanged: no release")
    sys.exit(0)

cur = new("VERSION").strip()
major, minor, patch = map(int, cur.split("."))
ver = f"{major}.{minor}.{patch + 1}"
open("VERSION", "w").write(ver + "\n")

git("add", "VERSION")
git("commit", "-q", "-m", f"release: v{ver} — {changes[0]}")
git("tag", "-a", f"v{ver}", "-m", f"v{ver}")

# a PR merged by hand is a merge commit: show the PR title, not "Merge pull request #N from ..."
src = git("log", "-1", "--format=%s", "HEAD~1").strip()
m = re.match(r"Merge pull request #(\d+)", src)
if m:
    src = f'{git("log", "-1", "--format=%b", "HEAD~1").splitlines()[0]} (#{m.group(1)})'

repo = os.environ.get("GITHUB_REPOSITORY", "nenchi/hammond")
open("release-notes.md", "w").write(
    "Automatic release after a dependency update. The end-to-end test (setup, login, JWT checks, vehicles, "
    "fill-ups, stats, UI assets) passed before the merge.\n\n## Changed\n"
    + "".join(f"- {c}\n" for c in changes)
    + f"- From: {src}\n\nImage: `ghcr.io/{repo}:{ver}`\n")
with open(os.environ.get("GITHUB_OUTPUT", os.devnull), "a") as f:
    f.write(f"version={ver}\n")
print(f"Released v{ver}:\n" + "\n".join(changes))
