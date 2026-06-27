#!/usr/bin/env bash
# Verifies that gen/ matches the contract pinned in contract.lock.
exec "$(dirname "${BASH_SOURCE[0]}")/generate.sh" --check
