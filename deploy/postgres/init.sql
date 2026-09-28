-- One Postgres instance, one database per service (no cross-service access).
CREATE DATABASE identity_db;
CREATE DATABASE catalog_db;
CREATE DATABASE order_db;
