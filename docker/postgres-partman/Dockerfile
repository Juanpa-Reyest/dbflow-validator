# Ephemeral PostgreSQL image for dbflow-validator, for areas whose changelogs
# CREATE EXTENSION pg_partman.
#
# The stock postgres image already ships unaccent, pg_trgm and fuzzystrmatch
# (postgres-contrib); only pg_partman is third-party and must be installed.
# The package is major-versioned (postgresql-17-partman), so it works for any
# 17.x base image. Installing the package is enough for CREATE EXTENSION; the
# background worker (shared_preload_libraries=pg_partman_bgw) is not needed for
# schema validation.
#
# Build:   docker build -t dbflow-postgres-partman:17.7 docker/postgres-partman
# Use:     dbflow-validator --postgres-image dbflow-postgres-partman:17.7 ...
FROM postgres:17.7

RUN set -eux; \
    apt-get update; \
    apt-get install -y --no-install-recommends postgresql-17-partman; \
    rm -rf /var/lib/apt/lists/*
