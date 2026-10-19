# MyGo editor package patch

Source: github.com/egoist/mygo v0.5.5, `ui/editor`, MIT (LICENSE included).
Only this 600 KB package is copied; native UI, text shaping and rendering still use
MyGo v0.5.5. The module path remains within the upstream namespace because the
upstream editor imports MyGo internal packages. This is a local fork, not an
upstream publication. No module cache files are modified.

Changes: `reading.go` adds a buffer-free reading position, applied after layout;
`diffview.go` calls restoration after diff geometry is ready. Tests importing the
upstream editor use the local package. `reading_test.go` covers release/recreate
and both layouts. All other upstream files remain unchanged.

When upgrading MyGo, replace this package from the same version and reapply the
small `reading-position.patch`; run the package tests and application race tests.
