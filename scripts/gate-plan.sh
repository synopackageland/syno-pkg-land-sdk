#!/usr/bin/env bash
# Reads changed paths, one per line, and prints the gate segments they
# affect, one per line: go-root, go-executor and go-conformance (the Go
# modules), rust, python, node and ts. scripts/gate.sh --fast runs only those.
# A path's segments are the ones whose tests read it.
set -euo pipefail
all="go-root go-executor go-conformance rust python node ts"
while IFS= read -r path; do
	case $path in
	'') ;;
	# App-facing DTOs do not affect the executor, Rust or bridge App SDKs.
	spec/delegate/* | internal/delegategen/* | ts/src/delegate/dtos-gen.ts) echo go-root ts ;;
	spec/* | scripts/*) echo "$all" ;;
	README.md | CHANGELOG.md | LICENSE | docs/* | release/*) ;;
	rust/*) echo rust ;;
	ts/*) echo ts ;;
	# The Go bridge tests run the Python and Node examples.
	python/*) echo python go-root ;;
	node/*) echo node go-root ;;
	executor/*) echo go-executor go-conformance ;;
	conformance/*) echo go-conformance ;;
	# conformance reads delegate/; the executor reads dsm/native/.
	delegate/*) echo go-root go-conformance ;;
	dsm/native/*) echo go-root go-executor ;;
	*) echo go-root ;;
	esac
done | tr ' ' '\n' | sort -u
