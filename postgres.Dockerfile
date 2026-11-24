# Loci's Postgres: PostGIS from the base image, pgvector built from source.
#
# PG_MAJOR and POSTGIS_VERSION pick the base and must name a published
# postgis/postgis tag. The defaults are what production runs; CI also builds
# 19 / 3.7 / 0.8.7 as an early warning for the next major. pgvector has to be
# rebuilt for each major, and 0.8.7 is the first release that supports 19.
#
# TimescaleDB is gone (migration 0138 drops the extension). Do not deploy this
# image to a server whose database still has it installed, or whose args still
# preload it: Postgres refuses to start without the library.
ARG PG_MAJOR=18
ARG POSTGIS_VERSION=3.6
FROM postgis/postgis:${PG_MAJOR}-${POSTGIS_VERSION}

ARG PG_MAJOR
ARG PGVECTOR_VERSION=0.8.2

RUN apt-get update && apt-get install -y \
    build-essential \
    postgresql-server-dev-${PG_MAJOR} \
    curl

RUN curl -L "https://github.com/pgvector/pgvector/archive/refs/tags/v${PGVECTOR_VERSION}.tar.gz" | tar xz -C /tmp

# Build and install pgvector.
#
# pgvector's Makefile defaults to OPTFLAGS=-march=native, which bakes the
# GitHub runner's CPU into vector.so. A build that landed on a Sapphire
# Rapids runner shipped AVX-512 code, and on the cluster's AMD EPYC Rome
# (AVX2, no AVX-512) every `<=>` query killed its backend with SIGILL and
# took Postgres into recovery. x86-64-v3 is AVX2+FMA: what the node has, and
# what pgvector's own release packages target.
RUN cd "/tmp/pgvector-${PGVECTOR_VERSION}" && make OPTFLAGS="-march=x86-64-v3" && make install

# Clean up
RUN rm -rf /tmp/pgvector-* && rm -rf /var/lib/apt/lists/*
