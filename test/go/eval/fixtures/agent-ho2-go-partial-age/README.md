# snapkeep

Keeps a directory of `snap-<UTC timestamp>.tar.zst` archives tidy.

## Usage

    snapkeep list  [--dir DIR] [--max-age AGE]
    snapkeep prune [--dir DIR] [--keep N] [--dry-run]

`list` prints every snapshot with its age; `--max-age` hides older ones.
`prune` deletes everything except the newest N snapshots (default 5);
`--dry-run` only prints what it would delete.
