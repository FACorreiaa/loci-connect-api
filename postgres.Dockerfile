FROM postgis/postgis:18-3.6

# Install build dependencies
RUN apt-get update && apt-get install -y \
    build-essential \
    postgresql-server-dev-18 \
    curl \
    gnupg \
    lsb-release

# Install TimescaleDB
RUN curl -s https://packagecloud.io/install/repositories/timescale/timescaledb/script.deb.sh | bash \
    && apt-get install -y timescaledb-2-postgresql-18

# Download and extract latest pgvector (0.8.2)
RUN curl -L https://github.com/pgvector/pgvector/archive/refs/tags/v0.8.2.tar.gz | tar xz -C /tmp

# Build and install pgvector.
#
# pgvector's Makefile defaults to OPTFLAGS=-march=native, which bakes the
# GitHub runner's CPU into vector.so. A build that landed on a Sapphire
# Rapids runner shipped AVX-512 code, and on the cluster's AMD EPYC Rome
# (AVX2, no AVX-512) every `<=>` query killed its backend with SIGILL and
# took Postgres into recovery. x86-64-v3 is AVX2+FMA: what the node has, and
# what pgvector's own release packages target.
RUN cd /tmp/pgvector-0.8.2 && make OPTFLAGS="-march=x86-64-v3" && make install

# Clean up
RUN rm -rf /tmp/pgvector-0.8.2 && rm -rf /var/lib/apt/lists/*
