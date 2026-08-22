#!/usr/bin/env bash
# The design systems in design-system/ must still describe the running apps.
#
# Two ways a design system rots, and one guard each:
#
#   1. A token changes in the app and nobody updates the system. Then the
#      system is a lie that reads like a record — checked by comparing every
#      declared color against the stylesheet the system names as its source.
#   2. A system.mjs changes and the committed previews are not rebuilt. The
#      previews are committed so that pushing to Claude Design needs no
#      toolchain, which is exactly what makes them go stale silently — the
#      same trap the companion's .syso icon is held to in CI.
set -euo pipefail
cd "$(dirname "$0")/.."

node design-system/check.mjs
node design-system/build.mjs --check
echo "design systems: OK"
