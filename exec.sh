#!/usr/bin/env bash
CGO_ENABLED=0 go build

cd /home/hakastein/work/macrocrm
docker compose exec php /gospy/gospy --pyroscope=exemaple.com --tag-from=server.REQUEST_URI-uri phpspy --max-depth=-1 \
  --threads=100 \
  --rate-hz=25 \
  --buffer-size=65536 \
  --php-version=74 \
  --continue-on-error \
  --request-info=qcup \
  --peek-global=server.REQUEST_URI \
  -P '-x "php-fpm" '