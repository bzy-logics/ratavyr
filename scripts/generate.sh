#!/usr/bin/env bash
# Regenerates gen/ from the engine API contract pinned in contract.lock.
#
#   scripts/generate.sh          regenerate gen/
#   scripts/generate.sh --check  fail if gen/ differs from a fresh generation
#
# The proto is read with `git show <tag>:<path>` from the motioncore repository
# at $MOTIONCORE_REPO (default ../motioncore) and verified against the
# sha256 in contract.lock. It is never copied into the tracked tree; it is
# materialised under .contract/ (gitignored). Code generators are the versions
# pinned in go.mod via the `tool` directive and run with `go tool`.
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$root"

check=0
case "${1:-}" in
  --check) check=1 ;;
  "") ;;
  *) echo "usage: $0 [--check]" >&2; exit 2 ;;
esac

lock() { sed -n "s/^$1=//p" contract.lock | tr -d '\r'; }
tag="$(lock tag)"
src_path="$(lock path)"
import_path="$(lock import_path)"
want_sha="$(lock sha256)"
want_commit="$(lock commit)"
for v in tag src_path import_path want_sha; do
  [[ -n "${!v}" ]] || { echo "contract.lock: missing $v" >&2; exit 1; }
done

repo="${MOTIONCORE_REPO:-$root/../motioncore}"
if ! git -C "$repo" rev-parse --git-dir >/dev/null 2>&1; then
  echo "error: $repo is not a git repository (set MOTIONCORE_REPO)" >&2
  exit 1
fi
if ! git -C "$repo" rev-parse -q --verify "refs/tags/$tag" >/dev/null; then
  echo "error: tag $tag not found in $repo (git -C $repo fetch --tags?)" >&2
  exit 1
fi
commit="$(git -C "$repo" rev-parse "$tag^{commit}")"
if [[ -n "$want_commit" && "$commit" != "$want_commit" ]]; then
  echo "warning: $tag resolves to $commit, contract.lock records $want_commit" >&2
fi

rm -rf .contract
mkdir -p ".contract/$(dirname "$import_path")"
proto=".contract/$import_path"
git -C "$repo" show "$tag:$src_path" >"$proto"

got_sha="$(sha256sum "$proto" | cut -d' ' -f1)"
if [[ "$got_sha" != "$want_sha" ]]; then
  echo "error: sha256 mismatch for $tag:$src_path" >&2
  echo "  contract.lock: $want_sha" >&2
  echo "  actual:        $got_sha" >&2
  exit 1
fi
echo "contract $tag ($commit) sha256 ok"

if [[ $check -eq 1 ]]; then
  tmp="$(mktemp -d)"
  trap 'rm -rf "$tmp"' EXIT
  go tool buf generate --output "$tmp"
  if ! diff -r "$tmp/gen" gen; then
    echo "error: gen/ is out of date; run scripts/generate.sh" >&2
    exit 1
  fi
  echo "gen/ is up to date"
else
  rm -rf gen
  go tool buf generate
  echo "generated gen/"
fi
