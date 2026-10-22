FROM public.ecr.aws/docker/library/node:24.22.1-bookworm AS node
FROM public.ecr.aws/docker/library/golang:1.26.10-bookworm AS go
FROM public.ecr.aws/docker/library/rust:1.97.1-bookworm AS rust
RUN rustup component add clippy rustfmt \
  && rustup target add x86_64-unknown-linux-musl aarch64-unknown-linux-musl armv7-unknown-linux-gnueabihf
FROM public.ecr.aws/docker/library/node:24.22.1-bookworm
ARG RUNNER_VERSION
ARG RUNNER_SHA256
ENV DEBIAN_FRONTEND=noninteractive
COPY build-ca.crt /etc/ssl/certs/ca-certificates.crt
ENV NODE_EXTRA_CA_CERTS=/etc/ssl/certs/ca-certificates.crt
RUN apt-get update && apt-get install -y --no-install-recommends \
    ca-certificates curl git jq unzip xz-utils zstd tar gzip \
    build-essential pkg-config libssl-dev libicu72 liblttng-ust1 \
    python3 python3-venv rsync openssh-client && rm -rf /var/lib/apt/lists/*
COPY --from=node /usr/local/ /usr/local/
COPY --from=go /usr/local/go /usr/local/go
COPY --from=rust /usr/local/cargo /opt/cargo
COPY --from=rust /usr/local/rustup /opt/rustup
ENV PATH=/usr/local/go/bin:/opt/cargo/bin:$PATH \
    CARGO_HOME=/opt/cargo RUSTUP_HOME=/opt/rustup \
    COREPACK_HOME=/opt/corepack NPM_CONFIG_REGISTRY=https://registry.npmmirror.com \
    HOME=/home/runner RUNNER_TOOL_CACHE=/opt/hostedtoolcache \
    AGENT_TOOLSDIRECTORY=/opt/hostedtoolcache
RUN useradd --uid 1001 --create-home --shell /bin/bash runner \
    && mkdir -p /opt/hostedtoolcache /home/runner/_work /cache/go /cache/cargo /cache/npm \
    && chown -R 1001:1001 /cache \
    && curl -fsSL --connect-timeout 20 --max-time 600 -o /tmp/runner.tar.gz \
      "https://github.com/actions/runner/releases/download/v${RUNNER_VERSION}/actions-runner-linux-x64-${RUNNER_VERSION}.tar.gz" \
    && echo "${RUNNER_SHA256}  /tmp/runner.tar.gz" | sha256sum -c - \
    && tar -xzf /tmp/runner.tar.gz -C /home/runner && rm /tmp/runner.tar.gz
COPY build-ca.crt /etc/ssl/certs/ca-certificates.crt
RUN corepack enable && COREPACK_NPM_REGISTRY=https://registry.npmmirror.com corepack prepare pnpm@10.33.2 --activate \
    && chown -R 1001:1001 /home/runner /opt/hostedtoolcache /opt/cargo /opt/rustup /opt/corepack
RUN curl -fsSL --retry 2 --connect-timeout 20 --max-time 600 -o /tmp/sccache.tar.gz \
      https://github.com/mozilla/sccache/releases/download/v0.18.0/sccache-v0.18.0-x86_64-unknown-linux-musl.tar.gz \
    && echo '45f1447fbe231e3037bde351ef70677dd212216c8d62ae7ca409fecc4d6acc89  /tmp/sccache.tar.gz' | sha256sum -c - \
    && tar -xzf /tmp/sccache.tar.gz -C /tmp \
    && install -m 0755 /tmp/sccache-v0.18.0-x86_64-unknown-linux-musl/sccache /usr/local/bin/sccache \
    && rm -rf /tmp/sccache*
COPY --chmod=755 runner-entrypoint.sh /usr/local/bin/ci-entrypoint
COPY cache_trim.py /app/cache_trim.py
WORKDIR /home/runner
USER 1001:1001
ENTRYPOINT ["/usr/local/bin/ci-entrypoint"]
