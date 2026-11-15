# Flag files

One JSON file per environment, `flags.<env>.json`, selected by `FLAGD_ENV`
(see src/load.js). `rollout` is the percentage of users (0-100) that get an
enabled flag.
