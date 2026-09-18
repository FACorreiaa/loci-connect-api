#!/usr/bin/env bash
# Refuse two migrations that claim the same version number.
#
# goose keys a migration by the number at the front of its filename and rejects
# a duplicate outright — "duplicate version 87 detected" — at startup, before it
# applies anything. So a collision does not degrade the service, it stops it
# booting, and it takes the integration suite with it.
#
# Git will not warn you: the filenames differ, so two branches that both picked
# 0087 merge cleanly and the collision only appears at run time, on main, after
# both PRs are green. That has happened three times in one day. The check is a
# directory scan precisely because the problem is invisible in a diff — you have
# to look at the whole tree at once to see it.
#
# Kept to POSIX-ish bash rather than bash 4 builtins (no mapfile, no associative
# arrays): macOS still ships bash 3.2, and a check that only runs in CI is one
# you find out about after pushing.
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
MIGRATIONS="${ROOT}/pkg/db/migrations"

if [[ ! -d "${MIGRATIONS}" ]]; then
  echo "check-migrations: no migrations directory at ${MIGRATIONS}" >&2
  exit 1
fi

# One migration is a version plus a name; goose pairs .up.sql with .down.sql of
# the same version, so those are one migration, not two. What must be unique is
# the version. .bak files are not migrations and goose never reads them.
#
# Output: "<version> <name>", deduplicated, so 0042_foo.up.sql and
# 0042_foo.down.sql collapse to a single "0042 foo".
all="$(
  find "${MIGRATIONS}" -maxdepth 1 -type f -name '*.sql' -print0 |
    while IFS= read -r -d '' path; do
      base="$(basename "${path}")"
      [[ "${base}" =~ ^([0-9]+)_(.+)\.(up|down)\.sql$ ]] || continue
      printf '%s %s\n' "${BASH_REMATCH[1]}" "${BASH_REMATCH[2]}"
    done | sort -u
)"

if [[ -z "${all}" ]]; then
  echo "check-migrations: found no migrations under ${MIGRATIONS}" >&2
  exit 1
fi

count="$(printf '%s\n' "${all}" | wc -l | tr -d ' ')"

# Already sorted by version, so uniq -d on the version column finds collisions.
duplicates="$(printf '%s\n' "${all}" | awk '{print $1}' | uniq -d)"

if [[ -n "${duplicates}" ]]; then
  echo "check-migrations: two migrations claim the same version." >&2
  echo >&2
  while IFS= read -r version; do
    [[ -z "${version}" ]] && continue
    echo "  version ${version}:" >&2
    printf '%s\n' "${all}" | awk -v v="${version}" '$1 == v {print "    " $1 "_" $2}' >&2
  done <<< "${duplicates}"
  echo >&2
  echo "goose refuses to start with a duplicate version, so this would break" >&2
  echo "migrations on main and the integration suite with them." >&2
  echo >&2
  highest="$(printf '%s\n' "${all}" | awk '{print $1}' | sort -n | tail -1)"
  next="$(printf '%04d' "$((10#${highest} + 1))")"
  echo "Renumber the newer one. The next free version is ${next}." >&2
  echo "A migration that has not been applied anywhere is safe to renumber;" >&2
  echo "one already recorded in goose's version table is not — check first." >&2
  exit 1
fi

echo "check-migrations: ${count} migrations, no duplicate versions."
