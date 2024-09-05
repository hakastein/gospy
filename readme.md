# GO SPY

Send phpspy samples to pyroscope without pain

## Usage

```sh

gospy \
  --pyroscope=https://pyroscope.example.com \
  --tag="env=development" \
  --tag="host=exacmple.com" \
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
```