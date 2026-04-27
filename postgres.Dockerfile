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

# Build and install pgvector
RUN cd /tmp/pgvector-0.8.2 && make && make install

# Clean up
RUN rm -rf /tmp/pgvector-0.8.2 && rm -rf /var/lib/apt/lists/*
