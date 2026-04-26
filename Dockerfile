# =============================================================================
# Healarr — self-healing agent for Plex/*arr media stacks
# =============================================================================
# Slim multi-stage build. Outbound email reuses the host's msmtp via mounted
# binary + ~/.msmtprc; the image itself ships with the msmtp client as a
# fallback so users without host msmtp can still operate.

FROM python:3.12-slim AS builder

WORKDIR /build
COPY pyproject.toml README.md ./
COPY healarr/ ./healarr/

RUN pip install --no-cache-dir --upgrade pip build \
 && python -m build --wheel \
 && pip install --no-cache-dir --target /install dist/*.whl


FROM python:3.12-slim

# msmtp for outbound email; ca-certificates for HTTPS to *arr APIs.
# tini for proper signal handling under Docker.
RUN apt-get update \
 && apt-get install -y --no-install-recommends \
        msmtp \
        ca-certificates \
        tini \
 && rm -rf /var/lib/apt/lists/*

COPY --from=builder /install /usr/local/lib/python3.12/site-packages

# Non-root by default. State volume mounted at /var/lib/healarr.
RUN groupadd --system --gid 1000 healarr \
 && useradd --system --uid 1000 --gid 1000 --home-dir /home/healarr healarr \
 && mkdir -p /var/lib/healarr \
 && chown -R healarr:healarr /var/lib/healarr

USER healarr
WORKDIR /home/healarr

ENV HEALARR_STATE_DB=/var/lib/healarr/state.db \
    PYTHONDONTWRITEBYTECODE=1 \
    PYTHONUNBUFFERED=1

ENTRYPOINT ["/usr/bin/tini", "--", "python", "-m", "healarr"]
CMD ["--help"]
