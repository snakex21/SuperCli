# servecfg

Small development server.

## Usage

    python -m servecfg.cli [options]

## Options

- `--host` (default `0.0.0.0`): interface to bind.
- `--port` (default `8000`): TCP port.
- `--workers` (default `2`): number of worker processes.
- `--verbose`: enable debug logging.
- `--log-file PATH`: write logs to a file.

## Authentication

Set the `SERVE_TOKEN` environment variable to require a token.

## License

MIT
