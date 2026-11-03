# pgxmock v5.2.0, locally patched

Copy of `github.com/pashagolub/pgxmock/v5@v5.2.0` (non-test sources, BSD-3 `LICENSE` kept),
wired in by the `replace` directive in the root `go.mod`.

**Why:** pgx v5.12.0 added `Tx()` to `pgx.BatchResults`, and v5.2.0 (the newest pgxmock release)
does not implement it, so every test importing pgxmock fails to compile. pgx stays at v5.12.0
because it fixes GHSA-j38q-h3ph-jjpq and GHSA-95fj-3fxw-g62g.

**The only change:** `batchResults.Tx()` in `batch.go`. The repo's pre-commit `gofumpt -w .` also
reformats a few lines in `pgxmock.go`, `query.go` and `rows.go`; behaviour is unchanged.

**Remove this directory and the `replace`** once pgxmock ships a release that supports pgx
v5.12 (track https://github.com/pashagolub/pgxmock/issues/341).
