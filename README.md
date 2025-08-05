# gospy

[![Go Report Card](https://goreportcard.com/badge/github.com/hakastein/gospy)](https://goreportcard.com/report/github.com/hakastein/gospy)
![Coverage](https://img.shields.io/badge/Coverage-65.7%25-yellow)

![gospy.jpg](gospy.jpg)

**gospy** allows you to send traces from phpspy to pyroscope with easy and simple configuration by cli arguments. With
gospy, [phpspy](https://github.com/adsr/phpspy) and [Pyroscope](https://pyroscope.io/) you can easily profile your php
application right in production with minimum overhead.

CPU and RAM usage of gospy relates to phpspy. With phpspy running 75 threads at a 25 Hz rate, it consumes about 200%
CPU, while gospy consumes only 40% CPU and uses just 30 MB of RAM. So, the total overhead in this case is about 250%
CPU. It also allows for very flexible configuration to achieve around 15% total CPU usage.

With dynamic tags, you can profile the entire app or specific slices, like certain URLs.

## Table of Contents

- [Installation](#installation)
    - [Building from Source](#building-from-source)
    - [Using Pre-built Binaries](#using-pre-built-binary)
- [Usage](#usage)
    - [Basic Usage](#basic-usage)
    - [Running in a Container](#running-in-a-container)
        - [Dockerfile Example](#dockerfile-example)
        - [Docker Compose Example](#docker-compose-example)
- [Configuration](#configuration)
- [Supported Profilers](#supported-profilers)

## Installation

### Building from Source

You need 1.23.3 or higher

1. **Clone the Repository**

   ```bash
   git clone https://github.com/hakastein/gospy.git
   cd gospy
   ```

2. **Build the Application**

   ```bash
   go build -ldflags "-X main.Version=$(git describe --tags)" -o gospy
   ```

### Using Pre-built Binary

Pre-built binary can be found in the [Releases](https://github.com/hakastein/gospy/releases) section.

## Usage

### Basic Usage

Run `gospy` with the necessary flags to start profiling your application. Below is an example of how to use `gospy` to
profile a PHP application using `phpspy`:

```bash
gospy \
  --pyroscope=https://pyroscope.example.com:4040 \
  --pyroscope-auth=your_auth_token \
  --pyroscope-workers=2 \
  --tag="env=production" \
  --tag="host=yourhost.example.com" \
  --tag="uri={{ \"glopeek server.REQUEST_URI\" }} " \
  --tag="source=php-fpm" \
  --tag-entrypoint \
  --app=your-app \
  --entrypoint="index.php" \
  --entrypoint="dashboard.php" \
    phpspy --max-depth=-1 \
      --threads=100 \
      -H 25 \
      --buffer-size=65536 \
      -J m \
      --continue-on-error \
      --peek-global=server.REQUEST_URI \
      -P '-x "php-fpm"'
```

### Running in a Container

To run `gospy` inside a Docker container, ensure that the container has the necessary permissions by adding the
`SYS_PTRACE` capability.

#### Dockerfile Example

Below is an example Dockerfile that sets up `gospy` and `phpspy` by downloading them from GitHub. The versions of the
downloaded binaries are specified as build arguments.

```dockerfile
# Use the official PHP image as the base
FROM php:8.2-fpm

# Build arguments for versions
ARG GOSPY_VERSION=0.7.9
ARG PHPSPY_VERSION=0.7.0

# Download and install phpspy
RUN mkdir -p /tmp/phpspy \
    && wget -O - https://github.com/adsr/phpspy/archive/refs/tags/v${PHPSPY_VERSION}.tar.gz | tar xzf - -C /tmp/phpspy \
    && cd /tmp/phpspy/phpspy-${PHPSPY_VERSION} \
    && make \
    && mv phpspy /usr/local/bin

# Download and install gospy
RUN wget -O - https://github.com/hakastein/gospy/releases/download/v${GOSPY_VERSION}/v${GOSPY_VERSION}.tar.gz | tar xzf - -C /usr/local/bin

# Copy start.sh script
COPY start.sh /usr/local/bin/start.sh

ENTRYPOINT ["start.sh"]
```

#### Docker Compose Example

Ensure that the Docker container is run with the `SYS_PTRACE` capability. Below is an example `docker-compose.yml`
configuration:

```yaml
version: '3.8'

services:
  php-fpm:
    image: your-docker-image
    cap_add:
      - SYS_PTRACE
    environment:
      - PYROSCOPE_URL=https://pyroscope.example.com:4040
      - PYROSCOPE_AUTH=your_auth_token
      - APP_NAME=your-app
      - TAGS=env=production,host=yourhost.example.com,uri=%glopeek server.REQUEST_URI%,source=php-fpm
    # ...
```

#### start.sh Script Example

Create a `start.sh` script to start `gospy` and `php-fpm` together:

```bash
#!/bin/bash

# Start php-fpm in the background
php-fpm &

# Start gospy with phpspy
gospy --gospy --config --here phpspy --phpspy --config --here

# Wait for all background processes
wait
```

## Configuration

`gospy` provides a variety of flags to customize its behavior:

- `--pyroscope` **(Required)**: Pyroscope server URL.
- `--pyroscope-auth`: Authentication token for Pyroscope.
- `--pyroscope-timeput`: Timeout to pyroscope request (default: 10s)
- `--pyroscope-workers`: Amount of workers who sends data to pyroscope. Default is `5`.
- `--app`: App name for Pyroscope.
- `--tag`: Static and dynamic tags in `key=value` or `key={{ "value" }}` format. **Can be used multiple times**.
- `--tag-entrypoint`: Add entry point to tags.
- `--rate-mb`: Ingestion rate limit in MB. Default is `4`.
- `--rate-mb-burst`: Ingestion rate limit burst in MB. Default is `6`.
- `--restart`: Restart profiler on exit. Options:
    - `always`: Always restart the profiler.
    - `onerror`: Restart only if the profiler exits with an error.
    - `onsuccess`: Restart only if the profiler exits successfully.
    - `no`: Do not restart the profiler. *(Default)*
- `--entrypoint`: Limit traces to certain entry points (e.g., `index.php`), it
  supports [glob double](https://github.com/bmatcuk/doublestar) star expressions. **Can be used multiple times**.
- `--instance-name`: Name of the `gospy` instance for logging purposes. Default is `gospy`.
- `--stats-interval`: Interval at which the application will log its sending statistics. Default: `10`
- `--verbose` or `-v`: Increase verbosity. Use multiple times for higher verbosity levels (e.g., `-vv`).

### Detailed Parameter Descriptions

#### Tags

Tags provide metadata for your profiling data. Static tags have fixed values, while dynamic tags can incorporate runtime
data.

- **Static Tags**: Defined with fixed values.
    - Example: `--tag="env=production"` adds a static tag `env` with the value `production`.

- **Dynamic Tags**: Defined with values wrapped in `{{ "" }}`, allowing `phpspy` to append runtime data.
    - Simple usage:
      `gospy --tag="uri={{ \"glopeek server.REQUEST_URI\" }} " phpspy --peek-global=server.REQUEST_URI`.
      In this example, `phpspy` appends the value of `$_SERVER['REQUEST_URI']` to the trace, and `gospy` adds it as
      the `uri` tag
    - Regex usage:
      You can use regex in dynamic tag, second quoted string is a regex and third is a replace string
      `gospy --tag="uri={{ \"glopeek server.REQUEST_URI\" \"^([^?]+)\?.*$\" \"\$1\" }} " phpspy --peek-global=server.REQUEST_URI`.
      In this example, similar to the previous one, phpspy will add `$_SERVER['REQUEST_URI']` to the metadata.
      However, before converting it to a tag, we remove the query part with regex

#### Restart Options

- `always`: The profiler will restart regardless of the exit status.
- `onerror`: The profiler will only restart if it exits with an error.
- `onsuccess`: The profiler will only restart if it exits successfully.
- `no`: The profiler will not restart automatically.

#### Entry Points

Specify one or more entry points to limit profiling to specific parts of your application.
Example: `--entrypoint="index.php"` restricts profiling to the `index.php` entry point.
You can use glob patterns: `--entrypoint="/app/**/cron/*.php`

## Supported Profilers

Currently, `gospy` supports the following profiler:

- [phpspy](https://github.com/adsr/phpspy): low-overhead sampling profiler for PHP 7+

---

Feel free to open an issue or submit a pull request for any bugs or feature requests!