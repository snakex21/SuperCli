# servecfg

Small development server.

## Usage

    python -m servecfg.cli [options]

## Options

- `--host` (default `127.0.0.1`): interface to bind.
- `--port` (default `8080`): TCP port.
- `--workers` (default `4`): number of worker processes.
- `--debug`: enable debug logging.

## Authentication

Set the `SERVECFG_TOKEN` environment variable to require a token.

## License

MIT
