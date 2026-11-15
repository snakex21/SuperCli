# snapkeep

Keeps a directory of `snap-<UTC timestamp>.tar.zst` archives tidy.

## Usage

    snapkeep list  [--dir DIR] [--max-age AGE]
    snapkeep prune [--dir DIR] [--keep N] [--older-than AGE] [--dry-run]

`list` prints every snapshot with its age; `--max-age` hides older ones.
`prune` deletes everything except the newest N snapshots (default 5);
`--older-than` additionally limits deletion to snapshots older than AGE
(the newest N are still kept). `--dry-run` only prints what it would delete.

AGE is a whole number with a unit: `36h`, `10d`, `2w`; a bare number means days.
