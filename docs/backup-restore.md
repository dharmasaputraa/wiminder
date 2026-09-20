# Backup & restore

The main data is a single file: `./data/wimember.db` (SQLite in WAL mode). While the container is running, copy the `./data` volume consistently, or use Litestream for continuous replication.

## Litestream (optional, recommended)

1. Fill in `deploy/litestream.yml` (DB path + S3/R2 bucket URL).
2. Fill in credentials in `.env`: `LITESTREAM_ACCESS_KEY_ID`, `LITESTREAM_SECRET_ACCESS_KEY`, and `LITESTREAM_ENDPOINT` (for R2; can be left empty for S3). The `litestream` container reads `.env` via `env_file`.
3. Run `docker compose --profile litestream up -d`.

## Restore

```bash
docker compose stop app
docker compose --profile litestream run --rm litestream \
  restore -o /data/wimember.db s3://your-bucket/wiminder/wimember.db
docker compose start app
```

For a one-off restore without filling in `.env`, pass the credentials directly to `docker compose run`, e.g. `docker compose --profile litestream run --rm -e LITESTREAM_ACCESS_KEY_ID=… -e LITESTREAM_SECRET_ACCESS_KEY=… litestream restore -o /data/wimember.db s3://your-bucket/wiminder/wimember.db`.

Alternative without Litestream: stop the app, copy `wimember.db` back, start the app.
