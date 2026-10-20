# The image scripts/rust-docker-gate.sh runs scripts/rust-gate.sh in: Rust
# with clippy, rustfmt and the three DSM targets. Its tag is this file's
# SHA-256 (scripts/docker-engine.sh), so editing it builds a new image.
FROM public.ecr.aws/docker/library/rust:1.97.1-bookworm
RUN rustup component add clippy rustfmt \
  && rustup target add x86_64-unknown-linux-musl aarch64-unknown-linux-musl armv7-unknown-linux-gnueabihf
