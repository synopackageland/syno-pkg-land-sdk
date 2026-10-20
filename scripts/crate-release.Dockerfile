# Build and run only on the Linux release builder. No credentials in this image.
FROM public.ecr.aws/docker/library/node:24.20.0-bookworm AS node
FROM public.ecr.aws/docker/library/rust:1.97.1-bookworm
COPY --from=node /usr/local/bin/node /usr/local/bin/node
