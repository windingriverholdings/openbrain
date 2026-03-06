#!/usr/bin/env bash
# OpenBrain: Database setup script
# Creates the openbrain database, user, and runs all migrations.
# Run once on a fresh system, or re-run safely (all SQL is idempotent).

set -euo pipefail

DB_NAME="${OPENBRAIN_DB_NAME:-openbrain}"
DB_USER="${OPENBRAIN_DB_USER:-openbrain}"
DB_PASSWORD="${OPENBRAIN_DB_PASSWORD:-}"
DB_HOST="${OPENBRAIN_DB_HOST:-localhost}"
DB_PORT="${OPENBRAIN_DB_PORT:-5432}"

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SQL_DIR="$SCRIPT_DIR/../sql"

# ── Generate password if not set ────────────────────────────────────────────
if [[ -z "$DB_PASSWORD" ]]; then
    DB_PASSWORD="$(openssl rand -base64 24 | tr -d '/+=')"
    echo "Generated DB password: $DB_PASSWORD"
    echo "Add this to your .env:"
    echo "  OPENBRAIN_DB_PASSWORD=$DB_PASSWORD"
    echo ""
fi

# ── Create role and database (run as postgres superuser) ────────────────────
echo "Creating database user '$DB_USER'..."
sudo -u postgres psql -c "
    DO \$\$
    BEGIN
        IF NOT EXISTS (SELECT FROM pg_roles WHERE rolname = '$DB_USER') THEN
            CREATE ROLE $DB_USER LOGIN PASSWORD '$DB_PASSWORD';
        ELSE
            ALTER ROLE $DB_USER PASSWORD '$DB_PASSWORD';
        END IF;
    END
    \$\$;
"

echo "Creating database '$DB_NAME'..."
sudo -u postgres psql -c "
    SELECT 'CREATE DATABASE $DB_NAME OWNER $DB_USER'
    WHERE NOT EXISTS (SELECT FROM pg_database WHERE datname = '$DB_NAME')
" | grep -q "CREATE DATABASE" && \
    sudo -u postgres createdb -O "$DB_USER" "$DB_NAME" || \
    echo "Database '$DB_NAME' already exists, skipping."

# ── Run migrations in order ─────────────────────────────────────────────────
echo "Running SQL migrations..."
for sql_file in "$SQL_DIR"/*.sql; do
    echo "  → $(basename "$sql_file")"
    sudo -u postgres psql -d "$DB_NAME" -f "$sql_file"
done

# ── Grant privileges ─────────────────────────────────────────────────────────
sudo -u postgres psql -d "$DB_NAME" -c "
    GRANT ALL PRIVILEGES ON ALL TABLES IN SCHEMA public TO $DB_USER;
    GRANT ALL PRIVILEGES ON ALL SEQUENCES IN SCHEMA public TO $DB_USER;
    GRANT EXECUTE ON ALL FUNCTIONS IN SCHEMA public TO $DB_USER;
    ALTER DEFAULT PRIVILEGES IN SCHEMA public
        GRANT ALL ON TABLES TO $DB_USER;
"

echo ""
echo "✓ OpenBrain database ready."
echo "  Host:     $DB_HOST:$DB_PORT"
echo "  Database: $DB_NAME"
echo "  User:     $DB_USER"
echo ""
echo "Connection string:"
echo "  postgresql://$DB_USER:***@$DB_HOST:$DB_PORT/$DB_NAME"
