#!/usr/bin/env bash
CGO_ENABLED=0 go build

cd /home/hakastein/work/macrocrm
docker compose exec php /gospy/gospy \
  --pyroscope=https://monitoring.macrodom.ru:4040 \
  --tag="env=development" \
  --tag="host=macrocrm.loc" \
  --tag="uri=%server.REQUEST_URI%" \
  --app=test-app \
    phpspy --max-depth=-1 \
    --threads=100 \
    -H 25 \
    --buffer-size=65536 \
    --php-version=74 \
    --continue-on-error \
    --top \
    -r qcup \
    --peek-global=server.REQUEST_URI \
    -P '-x "php-fpm" '