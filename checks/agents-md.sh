#!/bin/sh
# Fails when AGENTS.md is longer than its line budget.
limit=150
lines=$(wc -l < AGENTS.md | tr -d ' ')
if [ "$lines" -gt "$limit" ]; then
  echo "AGENTS.md has $lines lines. The limit is $limit. Delete a line or turn one into a check." >&2
  exit 1
fi
