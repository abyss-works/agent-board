FROM pgvector/pgvector:pg17

RUN set -eux; \
    apt-get update; \
    apt-get install -y --no-install-recommends ca-certificates curl; \
    . /etc/os-release; \
    DEB="groonga-apt-source-latest-${VERSION_CODENAME}.deb"; \
    curl -fsSL -o "/tmp/${DEB}" "https://packages.groonga.org/debian/${DEB}"; \
    apt-get install -y --no-install-recommends "/tmp/${DEB}"; \
    rm -f "/tmp/${DEB}"; \
    apt-get update; \
    apt-get install -y --no-install-recommends postgresql-17-pgdg-pgroonga; \
    apt-get purge -y --auto-remove curl; \
    rm -rf /var/lib/apt/lists/*

COPY postgresql.conf /etc/postgresql/postgresql.conf
RUN chmod 0644 /etc/postgresql/postgresql.conf

CMD ["postgres", "-c", "config_file=/etc/postgresql/postgresql.conf"]
