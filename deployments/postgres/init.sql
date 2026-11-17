-- Runs once, on first start of an empty Postgres volume.
-- Database-per-service: each microservice owns exactly one database and no
-- other service may read or write it directly (see docs/adr/0002).
CREATE DATABASE smarthire_jobs;
CREATE DATABASE smarthire_candidates;
