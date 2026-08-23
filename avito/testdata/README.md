# Fixtures

Pages captured from avito.ru on 2026-08-23 and gzipped as served. Each has, or
will have, two companions:

- `<name>.meta.json` — the surface, the URL, the capture time, and the **frozen
  clock** the test runs with, so a fixture whose content has a validity window
  cannot make the suite fail on a day nobody changed anything.
- `<name>.want.json` — the whole expected `Page`, including the
  `ExtractionReport`. Pinning only the listings would let the exact regression
  this design exists to catch pass green: the same fifty listings reached by the
  DOM path instead of the state path.

They carry third-party personal data and must be scrubbed before this
repository is published. See `docs/measurements/fixtures/README.md`.
