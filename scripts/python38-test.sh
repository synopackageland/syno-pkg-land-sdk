#!/usr/bin/env bash
# Runs the Python App SDK tests on Python 3.8, DSM 7's system python3, in
# Docker (scripts/docker-tree.sh).
set -euo pipefail
exec "$(dirname "$0")/docker-tree.sh" "${SYNOPKGLAND_PYTHON38_TEST_CONTAINER:-syno-pkg-land-sdk-python38-test}" \
	public.ecr.aws/docker/library/python:3.8.20-slim-bookworm \
	'cd python && python -B -m unittest discover -s tests'
