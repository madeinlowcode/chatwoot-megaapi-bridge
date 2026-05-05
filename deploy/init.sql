-- Initial setup: create the bridge database/role if running outside the
-- default POSTGRES_DB. With the default compose setup, POSTGRES_DB already
-- creates "bridge", so this file is a no-op except for explicit grants.
GRANT ALL PRIVILEGES ON DATABASE bridge TO bridge;
