-- One Postgres instance, one database per service (no cross-service access).
-- This only runs on an empty volume; existing volumes get new databases from the db-init compose job.
CREATE DATABASE identity_db;
CREATE DATABASE catalog_db;
CREATE DATABASE order_db;
CREATE DATABASE payment_db;
CREATE DATABASE ledger_db;
