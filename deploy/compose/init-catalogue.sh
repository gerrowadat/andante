#!/bin/sh
# SPDX-License-Identifier: Apache-2.0
# Copyright (C) 2026 Dave O'Connor
#
# Give the catalogue a role and a database of its own. Postgres only runs this on
# an empty data directory.
set -eu

psql -v ON_ERROR_STOP=1 -v password="$CATALOGUE_DB_PASSWORD" --username "$POSTGRES_USER" --dbname "$POSTGRES_DB" <<'SQL'
CREATE ROLE catalogue LOGIN PASSWORD :'password';
CREATE DATABASE catalogue OWNER catalogue;
SQL
