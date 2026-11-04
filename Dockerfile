FROM python:3.12-slim

RUN apt-get update \
    && apt-get install -y --no-install-recommends tini \
    && rm -rf /var/lib/apt/lists/* \
    && useradd --uid 10001 --create-home --shell /usr/sbin/nologin readyspec \
    && mkdir -p /workspace \
    && chown readyspec:readyspec /workspace \
    && chmod 0700 /workspace

WORKDIR /opt/readyspec
COPY pyproject.toml README.md LICENSE NOTICE ./
COPY src ./src
RUN pip install --no-cache-dir .

USER readyspec
ENV READYSPEC_WORKSPACE=/workspace \
    LANG=C.UTF-8 \
    PYTHONUTF8=1 \
    PYTHONDONTWRITEBYTECODE=1 \
    HOME=/home/readyspec
VOLUME /workspace
STOPSIGNAL SIGTERM
ENTRYPOINT ["/usr/bin/tini", "--"]
CMD ["sleep", "infinity"]
