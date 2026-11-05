# poolwarden test bed: poolwarden + pgpool-II 4.5.13 + PostgreSQL 18
# Build context: repo root.
FROM golang:1.27 AS builder
WORKDIR /src
COPY . .
RUN CGO_ENABLED=0 go build -trimpath \
    -ldflags "-s -w -X github.com/smarsh/poolwarden/internal/cli.version=testbed" \
    -o /poolwarden ./cmd/poolwarden

FROM postgres:18
ARG PGPOOL_VER=4_5_13
RUN apt-get update && apt-get install -y --no-install-recommends \
      autoconf automake libtool flex bison gcc make pkg-config libpq-dev \
      openssh-server openssh-client iproute2 iputils-arping procps sudo less \
      postgresql-server-dev-18 \
      curl ca-certificates && rm -rf /var/lib/apt/lists/*
RUN set -eux; \
    curl -fsSL "https://codeload.github.com/pgpool/pgpool2/tar.gz/refs/tags/V${PGPOOL_VER}" \
      | tar xz -C /tmp; \
    cd "/tmp/pgpool2-${PGPOOL_VER}"; \
    autoreconf -f -i; \
    ./configure --prefix=/usr --with-openssl; \
    make -j"$(nproc)"; make install; \
    make -C src/sql/pgpool-recovery install; \
    rm -rf "/tmp/pgpool2-${PGPOOL_VER}"
COPY --from=builder /poolwarden /usr/bin/poolwarden
RUN mkdir -p /var/lib/poolwarden /etc/pgpool-II /var/log/pgpool-II \
      /var/lib/postgresql/18 /run/sshd && \
    chmod 0700 /var/lib/poolwarden && \
    ssh-keygen -t ed25519 -N '' -f /etc/poolwarden_test_key && \
    mkdir -p /root/.ssh /var/lib/postgresql/.ssh && \
    cp /etc/poolwarden_test_key.pub /root/.ssh/authorized_keys && \
    cp /etc/poolwarden_test_key.pub /var/lib/postgresql/.ssh/authorized_keys && \
    cp /etc/poolwarden_test_key /root/.ssh/id_ed25519 && \
    cp /etc/poolwarden_test_key.pub /root/.ssh/id_ed25519.pub && \
    cp /etc/poolwarden_test_key /var/lib/postgresql/.ssh/id_ed25519 && \
    cp /etc/poolwarden_test_key.pub /var/lib/postgresql/.ssh/id_ed25519.pub && \
    printf 'Host *\n  StrictHostKeyChecking accept-new\n  UserKnownHostsFile /root/.ssh/known_hosts\n' \
      > /root/.ssh/config && \
    printf 'Host *\n  StrictHostKeyChecking accept-new\n' \
      > /var/lib/postgresql/.ssh/config && \
    chmod 600 /root/.ssh/id_ed25519 /root/.ssh/authorized_keys \
      /var/lib/postgresql/.ssh/id_ed25519 /var/lib/postgresql/.ssh/authorized_keys && \
    chown -R postgres:postgres /var/lib/postgresql/.ssh /var/lib/postgresql/18 \
      /var/log/pgpool-II && \
    chmod 750 /var/lib/postgresql && usermod -p '*' postgres && \
    echo 'postgres ALL=(root) NOPASSWD: /usr/bin/pg_ctl, /usr/bin/pgpool' >> /etc/sudoers
COPY dev/testbed/pgpool.conf.template.head dev/testbed/pgpool.conf.template.mid dev/testbed/pgpool.conf.template.tail dev/testbed/pool_hba.conf \
     dev/testbed/pcp.conf.tmpl dev/testbed/poolwarden.yaml.tmpl \
     dev/testbed/entrypoint.sh /opt/testbed/
COPY dev/testbed/scripts/ /opt/testbed/scripts/
COPY dev/testbed/scripts/svc /usr/local/bin/svc
RUN chmod +x /opt/testbed/entrypoint.sh /opt/testbed/scripts/*.sh
ENV PATH=/usr/bin:/usr/local/bin:$PATH
ENTRYPOINT ["/opt/testbed/entrypoint.sh"]
