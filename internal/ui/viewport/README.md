# Viewport width reuse

This internal component derives from `github.com/charmbracelet/bubbles/viewport` at pinned version `v1.0.0` (MIT). Its upstream copyright and permission notice are preserved in `LICENSE` and the distribution root license.

The only behavioral implementation change is in `SetContent`: it reuses terminal widths when a line matches the previous immutable normalized content at the same position. Metadata uses two bytes per line. Widths of 65,535 columns or more always use the upstream width calculation. Each update owns fresh metadata so copied models and mutable public line slices cannot invalidate another model’s cache. Rendering, public methods, key mapping and scrolling stay as in the pinned implementation. No module or dependency is added.

`viewport_parity_test.go` compares generated fixtures directly against the pinned upstream dependency. When updating Bubbles, update this component deliberately and rerun the oracle comparisons.
