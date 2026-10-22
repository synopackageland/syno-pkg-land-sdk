FROM public.ecr.aws/docker/library/node:24.22.1-bookworm-slim
RUN apt-get update && apt-get install -y --no-install-recommends python3 ca-certificates git \
    && rm -rf /var/lib/apt/lists/*
COPY build-ca.crt /etc/ssl/certs/ca-certificates.crt
COPY controller.py /app/controller.py
COPY --chmod=755 git-credential-ci /app/git-credential-ci
ENTRYPOINT ["python3", "-B", "/app/controller.py"]
