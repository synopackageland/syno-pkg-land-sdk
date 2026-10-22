#!/usr/bin/env bash
# The caller installs the root-owned secret separately through stdin.
# Never pass a PAT in this script's arguments or environment.
set -euo pipefail
cd "$(dirname "$0")"
test "$(id -u)" = 0
test "$(stat -c '%a:%u:%g' /etc/ci-private/github_token)" = '600:0:0'
version=2.338.0
sha256=af4b794c1bc41d73d40535e3fe092a39f9679cd8d965954c2aca25a05ca41d32
cp /etc/ssl/certs/ca-certificates.crt build-ca.crt
docker build --build-arg RUNNER_VERSION="$version" --build-arg RUNNER_SHA256="$sha256" \
  -f Runner.Dockerfile -t ci-runner:1 .
docker build -f Controller.Dockerfile -t ci-controller:1 .
if ! docker network inspect ci-jobs >/dev/null 2>&1; then
  docker network create --label ci.managed=private-ci-v1 \
    --opt com.docker.network.bridge.enable_icc=false ci-jobs
fi
if docker container inspect ci-controller >/dev/null 2>&1; then
  test "$(docker inspect -f '{{index .Config.Labels "ci.managed"}}' ci-controller)" = private-ci-v1
  docker rm -f ci-controller
fi
docker run -d --name ci-controller --hostname ci-controller \
  --label ci.managed=private-ci-v1 --label ci.role=controller --restart unless-stopped \
  --cpus 0.5 --memory 256m --memory-swap 256m --pids-limit 64 \
  --cap-drop ALL --security-opt no-new-privileges --read-only \
  --tmpfs /tmp:rw,noexec,nosuid,size=256m \
  --log-driver local --log-opt max-size=10m --log-opt max-file=2 \
  --mount type=bind,src=/var/run/docker.sock,dst=/var/run/docker.sock \
  --mount type=bind,src=/etc/ci-private/github_token,dst=/run/secrets/github_token,readonly \
  ci-controller:1
