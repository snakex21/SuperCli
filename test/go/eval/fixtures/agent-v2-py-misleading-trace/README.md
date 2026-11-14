# usage-cache

Known issue: cache files occasionally get corrupted when two workers
write the same entry; the reader then fails with a JSONDecodeError.
