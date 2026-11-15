# Pinned Odoo 19.0 excerpts

These files are verbatim excerpts of `odoo/odoo`, branch `19.0`, commit
`a157617c921669c5c036509b341be6a07ad1069d`:

| File | Upstream path |
| --- | --- |
| `epson_printer.js` | `addons/point_of_sale/static/src/app/utils/printer/epson_printer.js` |
| `init_lna.js` | `addons/point_of_sale/static/src/app/utils/init_lna.js` |
| `pos_store.js` | `addons/point_of_sale/static/src/app/services/pos_store.js` |

Odoo is licensed under LGPL-3.0. Only the lines that define the printer URL,
the LNA reset and the printer/initLNA call order are reproduced.

`TestOdoo19SourceContract` checks this snapshot, not a live upstream checkout.
A change in Odoo is only noticed after this procedure:

1. Pick the new commit (for example the new `19.0` head) and record its SHA.
2. Re-fetch the three files at that commit and diff them against these
   excerpts.
3. Update the excerpts, `odoo19Commit` in `odoo19_test.go` and the SHA quoted
   in `docs/macos11-odoo-https.md`.
4. Re-run `go test ./internal/compat` and revisit `ClassifyAtLoad`, the matrix
   and the guide if the contract changed.
