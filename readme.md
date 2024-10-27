
# GoSpy

A CLI tool for collecting traces from sampling profilers (such as PHPSpy, currently the only supported profiler) and sending them to Pyroscope.

## Table of Contents

- [Introduction](#introduction)
- [Features](#features)
- [Installation](#installation)
- [Usage](#usage)
- [Configuration](#configuration)
- [Docker Usage](#docker-usage)

## Introduction

GoSpy is a standalone CLI tool designed to collect profiling data from sampling profilers like PHPSpy and send this data to Pyroscope, a continuous profiling platform. The tool can be used both inside Docker containers and as a standalone executable on your system.

## Features

- Easy integration with Pyroscope for performance profiling.
- Works with sampling profilers such as PHPSpy.
- Can be used both inside Docker containers and as a standalone binary.
- Provides a CLI interface for flexible usage.

## Installation

To install GoSpy, download the pre-built binary from the [releases page](https://github.com/hakastein/gospy/releases) and place it in your system's `PATH`.

### Requirements

- A sampling profiler (like PHPSpy) must be installed and available in your environment.

## Usage

To use GoSpy, run the following command with the appropriate options:

### Command-Line Options

```shell
   --pyroscope value                          Pyroscope server URL
   --pyroscopeAuth value                      Pyroscope authentication token
   --debug                                    Enable debug logging (default: false)
   --app value                                App name for Pyroscope
   --restart value                            Restart profiler on exit (always, onerror, onsuccess, no). Default: no (default: "no")
   --tag value [ --tag value ]                Static and dynamic tags (key=value or key=%value%)
   --accumulation-interval value              Interval between sending accumulated samples to Pyroscope (default: 10s)
   --rate-mb value                            Ingestion rate limit in MB (default: 4)
   --entrypoint value [ --entrypoint value ]  Entrypoint filenames to collect data from (e.g., index.php)
   --help, -h                                 show help
```

### Example

Here's a complete example of how to run GoSpy with all options:

```sh
gospy \
  --pyroscope=https://pyroscope.example.com \
  --pyroscopeAuth="your-auth-token" \
  --app="test-app" \
  --tag="env=development" \
  --tag="host=example.com" \
  --tag="uri=%server.REQUEST_URI%" \
  --restart="always" \
  --debug \
  phpspy \
    --max-depth=-1 \
    --threads=100 \
    -H 25 \
    --buffer-size=65536 \
    --php-version=74 \
    --continue-on-error \
    --top \
    -r qcup \
    --peek-global=server.REQUEST_URI \
    -P '-x "php-fpm"'
```

## Configuration

You can configure the behavior of GoSpy using the command-line options listed above. Refer to the example command for guidance on how to combine these options for your specific use case.

## Docker Usage

To use GoSpy with `php-fpm` in a Docker container, you need to run both the `php-fpm` service and GoSpy simultaneously. You can achieve this using a simple bash script with the `&` operator or by using a process manager like `supervisord`.

### Using Bash

Here is an example `Dockerfile` that sets up `php-fpm` with GoSpy using bash:

```Dockerfile
FROM php:7.4-fpm

# Install GoSpy and any necessary dependencies
COPY gospy /usr/local/bin/gospy

# Install PHPSpy or other necessary profiling tools

# Start both php-fpm and GoSpy
CMD ["sh", "-c", "php-fpm & gospy --pyroscope=https://pyroscope.example.com --app=test-app phpspy"]
```

### Using Supervisord

For a more robust solution, you can use `supervisord` to manage both processes. You can find more information about using `supervisord` in Docker [here](http://supervisord.org/running.html).

